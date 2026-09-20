// Package postgres holds the controller's connection to the Postgres server
// it manages. Phase 1 only opens the pool and reads the server version; the
// same pooled *sql.DB is what Phase 2's reconciler will run catalog queries
// against, and the pool cap exists from the start on purpose - see
// "stuck-risk 2" in docs/brief.md.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Config is the non-secret connection configuration, sourced from env vars
// so no connection detail is hardcoded. The password itself is never an env
// var - it is read fresh from a Kubernetes Secret on every startup.
type Config struct {
	Host              string
	Port              string
	User              string
	SecretNamespace   string
	SecretName        string
	SecretPasswordKey string
	MaxOpenConns      int
}

// LoadConfigFromEnv reads Config from the environment, falling back to
// defaults that match a `make setup` + `make run` local kind session.
func LoadConfigFromEnv() Config {
	return Config{
		Host:              getEnv("PG_HOST", "localhost"),
		Port:              getEnv("PG_PORT", "5432"),
		User:              getEnv("PG_SUPERUSER_USER", "postgres"),
		SecretNamespace:   getEnv("PG_SUPERUSER_SECRET_NAMESPACE", "postgres"),
		SecretName:        getEnv("PG_SUPERUSER_SECRET_NAME", "postgres-superuser"),
		SecretPasswordKey: getEnv("PG_SUPERUSER_SECRET_KEY", "password"),
		MaxOpenConns:      5,
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// ReadSuperuserPassword fetches the superuser password from the Kubernetes
// Secret named by cfg. The password is never logged and never written to
// disk or an env var.
func ReadSuperuserPassword(ctx context.Context, clientset kubernetes.Interface, cfg Config) (string, error) {
	secret, err := clientset.CoreV1().Secrets(cfg.SecretNamespace).Get(ctx, cfg.SecretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("reading secret %s/%s: %w", cfg.SecretNamespace, cfg.SecretName, err)
	}
	password, ok := secret.Data[cfg.SecretPasswordKey]
	if !ok {
		return "", fmt.Errorf("secret %s/%s has no key %q", cfg.SecretNamespace, cfg.SecretName, cfg.SecretPasswordKey)
	}
	return string(password), nil
}

// Connect opens a pooled connection to the Postgres server described by cfg
// and pings it. Callers own the returned *sql.DB and must close it.
func Connect(ctx context.Context, cfg Config, password string) (*sql.DB, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		url.QueryEscape(cfg.User), url.QueryEscape(password), cfg.Host, cfg.Port, cfg.User)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening pool: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging %s:%s: %w", cfg.Host, cfg.Port, err)
	}
	return db, nil
}

// Version runs SELECT version() and returns the result verbatim.
func Version(ctx context.Context, db *sql.DB) (string, error) {
	var version string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		return "", fmt.Errorf("querying version: %w", err)
	}
	return version, nil
}
