package ipamutil

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1alpha1"
	clusterutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// ReleaseAddressFinalizer is used to release an IP address before cleaning up the claim.
	ReleaseAddressFinalizer = "ipam.cluster.x-k8s.io/ReleaseAddress"

	// ProtectAddressFinalizer is used to prevent deletion of an IPAddress object while its claim is not deleted.
	ProtectAddressFinalizer = "ipam.cluster.x-k8s.io/ProtectAddress"
)

// ClaimReconciler reconciles a IPAddressClaim object using a ProviderIntegration.
type ClaimReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	WatchFilterValue string

	Provider ProviderIntegration
}

// ProviderIntegration is an interface that must be implemented by an IPAM provider.
type ProviderIntegration interface {
	// SetupWithManager allows the integration to configure the controller.
	SetupWithManager(context.Context, *ctrl.Builder) error
	// ClaimHandlerFor needs to return a ClaimHandler for the provider.
	ClaimHandlerFor(client.Client, *ipamv1.IPAddressClaim) ClaimHandler
}

// ClaimHandler knows how to allocate and release IP addresses for a specific provider.
type ClaimHandler interface {
	FetchPool(ctx context.Context) (*ctrl.Result, error)
	EnsureAddress(ctx context.Context, address *ipamv1.IPAddress) (*ctrl.Result, error)
	ReleaseAddress() (*ctrl.Result, error)
	GetPool() client.Object
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClaimReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		WithEventFilter(predicates.ResourceNotPausedAndHasFilterLabel(ctrl.LoggerFrom(ctx), r.WatchFilterValue))

	if err := r.Provider.SetupWithManager(ctx, b); err != nil {
		return err
	}
	return b.Complete(r)
}

// Reconcile is called by the controller to reconcile a claim.
func (r *ClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling claim")

	// Fetch the IPAddressClaim
	claim := &ipamv1.IPAddressClaim{}
	if err := r.Get(ctx, req.NamespacedName, claim); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("Reconciling claim 2")
	if _, ok := claim.GetLabels()[clusterv1.ClusterNameLabel]; ok {
		cluster, err := clusterutil.GetClusterFromMetadata(ctx, r.Client, claim.ObjectMeta)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("IPAddressClaim linked to a cluster that is not found, unable to determine cluster's paused state, skipping reconciliation")
				return ctrl.Result{}, nil
			}

			log.Error(err, "error fetching cluster linked to IPAddressClaim")
			return ctrl.Result{}, err
		}

		if annotations.IsPaused(cluster, cluster) {
			log.Info("IPAddressClaim linked to a cluster that is paused, skipping reconciliation")
			return ctrl.Result{}, nil
		}
	}

	log.Info("Reconciling claim 3")
	// Create a patch helper for the claim.
	patchHelper, err := patch.NewHelper(claim, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Reconciling claim 4")
	defer func() {
		if err := patchHelper.Patch(ctx, claim); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
	}()

	// Ensure the claim has a finalizer.
	controllerutil.AddFinalizer(claim, ReleaseAddressFinalizer)

	log.Info("Reconciling claim 5")
	// Create the provider handler and fetch the pool.
	handler := r.Provider.ClaimHandlerFor(r.Client, claim)
	if res, err := handler.FetchPool(ctx); err != nil || res != nil {
		if apierrors.IsNotFound(err) {
			// err := errors.New("pool not found")
			log.Error(err, "the referenced pool could not be found")
			if !claim.ObjectMeta.DeletionTimestamp.IsZero() {
				return r.reconcileDelete(ctx, claim)
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, errors.Wrap(err, "failed to fetch pool")
	}

	log.Info("Reconciling claim 6")
	pool := handler.GetPool()
	if pool != nil && annotations.HasPaused(pool) {
		log.Info("IPAddressClaim references Pool which is paused, skipping reconciliation.", "IPAddressClaim", claim.GetName(), "Pool", pool.GetName())
		return ctrl.Result{}, nil
	}

	if !claim.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, claim)
	}

	log.Info("Reconciling claim 7")
	address := ipamv1.IPAddress{}
	if err := r.Client.Get(ctx, req.NamespacedName, &address); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, errors.Wrap(err, "failed to fetch address")
	}
	// } else if err != nil && apierrors.IsNotFound(err) {
	// 	return ctrl.Result{}, errors.Wrap(err, "not found LOL")
	// }

	log.Info("Reconciling claim 8")

	// If the claim is marked for deletion, release the address.
	if !claim.ObjectMeta.DeletionTimestamp.IsZero() {
		fmt.Println("DeletionTimestamp.IsZero")
		if res, err := handler.ReleaseAddress(); err != nil || res != nil {
			fmt.Printf("ReleaseAddress: %s\n", err.Error())
			return unwrapResult(res), err
		}
		return r.reconcileDelete(ctx, claim)
	}

	log.Info("Reconciling claim 9")

	log.Info("claim", "name", claim.Name)

	// We always ensure there is a valid address object passed to the handler.
	// The handler will complete it with the ip address.
	if address.Name == "" {
		address = NewIPAddress(claim, handler.GetPool())
	}

	log.Info("Reconciling claim 10")
	if res, err := handler.EnsureAddress(ctx, &address); err != nil || res != nil {
		if err != nil {
			log.Error(err, "EnsureAddress")
		}
		log.Info("EnsureAddress - no error, result not nil")
		return unwrapResult(res), err
	}

	log.Info("Reconciling claim 11")
	// Patch or create the address, ensuring necessary owner references are set.
	operationResult, err := controllerutil.CreateOrPatch(ctx, r.Client, &address, func() error {
		if err := ensureIPAddressOwnerReferences(r.Scheme, &address, claim, handler.GetPool()); err != nil {
			log.Error(err, "EnsureAddress")
			return errors.Wrap(err, "failed to ensure owner references on address")
		}

		log.Info("patch function before finalizer add")

		_ = controllerutil.AddFinalizer(&address, ProtectAddressFinalizer)

		log.Info("patch function after finalizer add")

		return nil
	})
	if err != nil {
		log.Error(err, "failed to create or patch address")
		return ctrl.Result{}, errors.Wrap(err, "failed to create or patch address")
	}

	log.Info("Reconciling claim 12")

	log.Info(fmt.Sprintf("IPAddress %s/%s (%s) has been %s", address.Namespace, address.Name, address.Spec.Address, operationResult),
		"IPAddressClaim", fmt.Sprintf("%s/%s", claim.Namespace, claim.Name))

	if !address.DeletionTimestamp.IsZero() {
		// We prevent deleting IPAddresses while their corresponding IPClaim still exists since we cannot guarantee that the IP
		// wil remain the same when we recreate it.
		log.Info("Address is marked for deletion, but deletion is prevented until the claim is deleted as well.", "address", address.Name)
	}

	log.Info("Reconciling claim 13")
	claim.Status.AddressRef = corev1.LocalObjectReference{Name: address.Name}

	return ctrl.Result{}, nil
}

func (r *ClaimReconciler) reconcileDelete(ctx context.Context, claim *ipamv1.IPAddressClaim) (ctrl.Result, error) {
	address := &ipamv1.IPAddress{}
	namespacedName := types.NamespacedName{
		Namespace: claim.Namespace,
		Name:      claim.Name,
	}
	if err := r.Client.Get(ctx, namespacedName, address); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, errors.Wrap(err, "failed to fetch address")
	}

	if address.Name != "" {
		var err error
		if controllerutil.RemoveFinalizer(address, ProtectAddressFinalizer) {
			if err = r.Client.Update(ctx, address); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, errors.Wrap(err, "failed to remove address finalizer")
			}
		}

		if err == nil {
			if err := r.Client.Delete(ctx, address); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
	}

	controllerutil.RemoveFinalizer(claim, ReleaseAddressFinalizer)
	return ctrl.Result{}, nil
}

func unwrapResult(res *ctrl.Result) ctrl.Result {
	if res == nil {
		return ctrl.Result{}
	}
	return *res
}
