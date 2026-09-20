package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	labv1alpha1 "github.com/kirPoNik/pg-database-operator/api/v1alpha1"
)

// PgDatabaseReconciler reconciles a PgDatabase object
type PgDatabaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=lab.pglab.dev,resources=pgdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lab.pglab.dev,resources=pgdatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lab.pglab.dev,resources=pgdatabases/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PgDatabase object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/reconcile
func (r *PgDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	// TODO(user): your logic here

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PgDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&labv1alpha1.PgDatabase{}).
		Named("pgdatabase").
		Complete(r)
}
