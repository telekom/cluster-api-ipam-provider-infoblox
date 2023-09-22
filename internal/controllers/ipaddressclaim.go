package controllers

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/pkg/errors"
	"github.com/telekom/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1alpha1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	ipampredicates "github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/predicates"
)

const (
	// ReleaseAddressFinalizer is used to release an IP address before cleaning up the claim.
	ReleaseAddressFinalizer = "ipam.cluster.x-k8s.io/ReleaseAddress"

	// ProtectAddressFinalizer is used to prevent deletion of an IPAddress object while its claim is not deleted.
	ProtectAddressFinalizer = "ipam.cluster.x-k8s.io/ProtectAddress"
)

// IPAddressClaimReconciler reconciles a InfobloxIPPool object.
type IPAddressClaimReconciler struct {
	newInfobloxClientFunc func(config infoblox.Config) (infoblox.Client, error)
}

var _ ipamutil.ProviderIntegration = &IPAddressClaimReconciler{}

type InfobloxClaimHandler struct {
	client.Client
	claim                 *ipamv1.IPAddressClaim
	pool                  *v1alpha1.InfobloxIPPool
	newInfobloxClientFunc func(config infoblox.Config) (infoblox.Client, error)
	ibclient              infoblox.Client
}

var _ ipamutil.ClaimHandler = &InfobloxClaimHandler{}

// SetupWithManager sets up the controller with the Manager.
func (r *IPAddressClaimReconciler) SetupWithManager(ctx context.Context, b *ctrl.Builder) error {
	b.
		For(&ipamv1.IPAddressClaim{}, builder.WithPredicates(
			ipampredicates.ClaimReferencesPoolKind(metav1.GroupKind{
				Group: v1alpha1.GroupVersion.Group,
				Kind:  "InfobloxIPPool",
			}),
		)).
		WithOptions(controller.Options{
			// To avoid race conditions when allocating IP Addresses, we explicitly set this to 1
			MaxConcurrentReconciles: 1,
		}).
		Owns(&ipamv1.IPAddress{}, builder.WithPredicates(
			ipampredicates.AddressReferencesPoolKind(metav1.GroupKind{
				Group: v1alpha1.GroupVersion.Group,
				Kind:  "InfobloxIPPool",
			}),
		))
	return nil
}

func (r *IPAddressClaimReconciler) ClaimHandlerFor(cl client.Client, claim *ipamv1.IPAddressClaim) ipamutil.ClaimHandler {
	return &InfobloxClaimHandler{
		Client:                cl,
		claim:                 claim,
		newInfobloxClientFunc: r.newInfobloxClientFunc,
	}
}

//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims/status;ipaddresses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims/status;ipaddresses/finalizers,verbs=update

func (h *InfobloxClaimHandler) FetchPool(ctx context.Context) (*ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	h.pool = &v1alpha1.InfobloxIPPool{}
	if err := h.Client.Get(ctx, types.NamespacedName{Namespace: h.claim.Namespace, Name: h.claim.Spec.PoolRef.Name}, h.pool); err != nil && !apierrors.IsNotFound(err) {
		return nil, errors.Wrap(err, "failed to fetch pool")
	}
	if h.pool == nil {
		err := errors.New("pool not found")
		log.Error(err, "the referenced pool could not be found")
		return nil, nil
	}

	// todo: ensure pool is ready

	var err error
	h.ibclient, err = getInfobloxClientForInstance(ctx, h.Client, h.pool.Spec.InstanceRef.Name, h.pool.Namespace, h.newInfobloxClientFunc)
	if err != nil {
		return nil, fmt.Errorf("failed to get infoblox client: %w", err)
	}
	return nil, nil
}

func (h *InfobloxClaimHandler) EnsureAddress(ctx context.Context, address *ipamv1.IPAddress) (*ctrl.Result, error) {
	subnet, err := netip.ParsePrefix(h.pool.Spec.Subnet)
	if err != nil {
		// We won't set a condition here since this should be caught by validation
		return nil, fmt.Errorf("failed to parse subnet: %w", err)
	}
	ipaddr, err := h.ibclient.GetOrAllocateAddress(h.pool.Spec.NetworkView, subnet, "", h.pool.Spec.DNSZone)
	if err != nil {
		conditions.MarkFalse(h.claim,
			v1beta1.ReadyCondition,
			v1alpha1.InfobloxAddressAllocationFailedReason,
			v1beta1.ConditionSeverityError,
			"could not allocate address: %s", err)
	}

	address.Spec.Address = ipaddr.String()
	//address.Spec.Gateway = h.pool.Spec.

	return nil, nil
}

func (h *InfobloxClaimHandler) ReleaseAddress() (*ctrl.Result, error) {
	panic("not implemented") // TODO: Implement
}

func (h *InfobloxClaimHandler) GetPool() client.Object {
	return h.pool
}
