# environment

Recorded the first time each tool/image is actually used. Host: macOS (Darwin
25.6.0), Apple Silicon (arm64), Docker running under Colima.

## Host tools

| Tool | Version | Command used to check |
|---|---|---|
| Go | go1.27.0 darwin/arm64 | `go version` |
| Docker (via Colima) | 28.4.0 | `docker version --format '{{.Server.Version}}'` |
| Colima | active docker context `colima` (default profile) | `docker context ls` |
| kind | v0.32.0 (go1.26.3 darwin/arm64) | `kind version` |
| kubectl (client) | v1.34.1 | `kubectl version --client` |
| kubebuilder | v4.15.0 | `kubebuilder version` |
| psql (host client) | 17.7 (Homebrew) | `psql --version` |
| gh (GitHub CLI) | account `kirPoNik`, scopes: gist, project, read:org, repo, workflow | `gh auth status` |

## kind cluster

- Cluster name: `pg-lab` (context `kind-pg-lab`), created via `deploy/kind-config.yaml`.
- Node image: `kindest/node:v1.36.1` (kind's default for kind v0.32.0 at the time of creation).
- Kubernetes server version: **v1.36.1** — note this is *two* minor versions ahead of the `kubectl` client (v1.34.1), which exceeds kubectl's documented +/-1 supported skew (`kubectl version` prints this warning explicitly). Nothing broke because of it in Phase 1; flagged here so a future weird `kubectl` behavior gets checked against this first instead of assumed to be a controller bug.
- Storage: kind's built-in default `StorageClass` (`rancher.io/local-path` provisioner), used unmodified by the Postgres `StatefulSet`'s `volumeClaimTemplates`.
- Port mapping: `deploy/kind-config.yaml` maps container port `30432` (the Postgres `Service`'s NodePort) to host port `55432`, so a controller running on the host (`make run`, not yet deployed in-cluster) can reach Postgres at `localhost:55432`.

## Postgres

- Image: `postgres:16.4` (official image, Debian-based, pinned — not `latest`).
- Deployed as a single-replica `StatefulSet` in namespace `postgres`, per `deploy/postgres.yaml`.
- Superuser password: generated with `openssl rand -base64 24` by `make setup`, stored only in the Kubernetes `Secret` `postgres/postgres-superuser`, never written to a file or committed.

## Go module dependencies (key ones)

| Module | Version | Why |
|---|---|---|
| `sigs.k8s.io/controller-runtime` | v0.24.1 | kubebuilder v4.15.0's default scaffold. |
| `k8s.io/client-go` | v0.36.0 | Same scaffold; also used directly in `internal/postgres` to read the superuser Secret. |
| `k8s.io/apimachinery` | v0.36.0 | Same scaffold. |
| `github.com/jackc/pgx/v5` | v5.11.0 | Postgres driver, used via its `database/sql` (`pgx/v5/stdlib`) adapter rather than `pgxpool`, so the controller gets a plain `*sql.DB` with `SetMaxOpenConns` — see `docs/brief.md` stuck-risk 2. Chosen over `lib/pq` (effectively unmaintained) per the org's dependency policy (actively maintained, popular, no open CVEs at the time of adding). |
| `controller-gen` | v0.21.0 | Downloaded by `make manifests`/`make generate` into `bin/` (gitignored). |
| `kustomize` | v5.8.1 | Downloaded by the kubebuilder Makefile into `bin/` (gitignored); note this differs from the `kubectl`-bundled Kustomize v5.7.1 shown by `kubectl version --client` — they are independent binaries. |

## kubebuilder project settings

- Domain: `pglab.dev` (a placeholder used only to namespace the API group as `lab.pglab.dev` — not a real, owned domain, and no DNS record for it exists or is implied).
- Repo/module: `github.com/kirPoNik/pg-database-operator`.
- Group/Version/Kind: `lab`/`v1alpha1`/`PgDatabase`, per `docs/brief.md`.
- License flag: `--license none` (the repo already has its own MIT `LICENSE`; kubebuilder defaults to Apache-2 headers on generated Go files, which we don't want).
