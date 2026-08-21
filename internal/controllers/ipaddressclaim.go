/*
Copyright 2023 Deutsche Telekom AG.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/hostname"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	ipampredicates "github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/predicates"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	hostnameAnnotation         = "ipam.cluster.x-k8s.io/hostname"
	infobloxInstanceAnnotation = "ipam.cluster.x-k8s.io/infoblox-instance"
	networkViewAnnotation      = "ipam.cluster.x-k8s.io/network-view"
	dnsViewAnnotation          = "ipam.cluster.x-k8s.io/dns-view"
)

// GetInfobloxClientForInstanceFn resolves the Infoblox client for the instance a pool refers to.
type GetInfobloxClientForInstanceFn func(ctx context.Context, c client.Reader, instanceName, operatorNamespace string, getClient infoblox.GetClientFunc) (infoblox.Client, error)

// NewHostnameResolverFn builds the resolver used to derive a hostname for a claim.
type NewHostnameResolverFn func(c client.Client, claim *ipamv1.IPAddressClaim) (hostname.Resolver, error)

// InfobloxProviderAdapter reconciles a InfobloxIPPool object.
type InfobloxProviderAdapter struct {
	GetInfobloxClientFunc   infoblox.GetClientFunc
	OperatorNamespace       string
	MaxConcurrentReconciles int
	Client                  client.Client

	// GetInfobloxClientForInstanceFunc resolves the Infoblox client for the instance a pool refers to.
	GetInfobloxClientForInstanceFunc GetInfobloxClientForInstanceFn
	// NewHostnameResolverFunc builds the hostname resolver for a claim.
	NewHostnameResolverFunc NewHostnameResolverFn
}

var _ ipamutil.ProviderAdapter = &InfobloxProviderAdapter{}

// InfobloxClaimHandler handles infoblox claims.
type InfobloxClaimHandler struct {
	Client            client.Client
	claim             *ipamv1.IPAddressClaim
	pool              *v1alpha1.InfobloxIPPool
	operatorNamespace string
	ibclient          infoblox.Client

	getInfobloxClientFunc        infoblox.GetClientFunc
	getInfobloxClientForInstance GetInfobloxClientForInstanceFn
	newHostnameResolver          NewHostnameResolverFn
}

var _ ipamutil.ClaimHandler = &InfobloxClaimHandler{}

// SetupWithManager sets up the controller with the Manager.
func (r *InfobloxProviderAdapter) SetupWithManager(_ context.Context, b *ctrl.Builder) error {
	b.
		For(&ipamv1.IPAddressClaim{}, builder.WithPredicates(
			ipampredicates.ClaimReferencesPoolKind(metav1.GroupKind{
				Group: v1alpha1.GroupVersion.Group,
				Kind:  v1alpha1.InfobloxIPPoolKind,
			}),
		)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.MaxConcurrentReconciles,
		}).
		Watches(
			&v1alpha1.InfobloxIPPool{},
			handler.EnqueueRequestsFromMapFunc(r.infobloxIPPoolToIPClaims),
		).
		Owns(&ipamv1.IPAddress{}, builder.WithPredicates(
			ipampredicates.AddressReferencesPoolKind(metav1.GroupKind{
				Group: v1alpha1.GroupVersion.Group,
				Kind:  v1alpha1.InfobloxIPPoolKind,
			}),
		))
	return nil
}

func (r *InfobloxProviderAdapter) infobloxIPPoolToIPClaims(ctx context.Context, obj client.Object) []reconcile.Request {
	if r.Client == nil {
		return nil
	}

	pool, ok := obj.(*v1alpha1.InfobloxIPPool)
	if !ok {
		return nil
	}

	logger := log.FromContext(ctx)
	claims := &ipamv1.IPAddressClaimList{}
	err := r.Client.List(ctx, claims,
		client.MatchingFields{
			index.IPAddressClaimPoolRefCombinedField: index.IPPoolRefValue(ipamv1.IPPoolReference{
				APIGroup: v1alpha1.GroupVersion.Group,
				Kind:     "InfobloxIPPool",
				Name:     pool.Name,
			}),
		},
		client.InNamespace(pool.Namespace),
	)
	if err != nil {
		logger.Error(err, "failed to list IPAddressClaims for InfobloxIPPool", "namespace", pool.Namespace, "name", pool.Name)
		return nil
	}

	requests := make([]reconcile.Request, 0, len(claims.Items))
	for _, claim := range claims.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      claim.Name,
				Namespace: claim.Namespace,
			},
		})
	}

	return requests
}

// ClaimHandlerFor returns handler for claim.
func (r *InfobloxProviderAdapter) ClaimHandlerFor(cl client.Client, claim *ipamv1.IPAddressClaim) ipamutil.ClaimHandler {
	return &InfobloxClaimHandler{
		Client:                       cl,
		claim:                        claim,
		getInfobloxClientFunc:        r.GetInfobloxClientFunc,
		operatorNamespace:            r.OperatorNamespace,
		getInfobloxClientForInstance: r.GetInfobloxClientForInstanceFunc,
		newHostnameResolver:          r.NewHostnameResolverFunc,
	}
}

//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims/status;ipaddresses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims/status;ipaddresses/finalizers,verbs=update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch

// for resolving hostnames
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metal3datas;metal3machines,verbs=get;list;watch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=vspheremachines;vspherevms,verbs=get;list;watch

// FetchPool fetches pool from cluster.
func (h *InfobloxClaimHandler) FetchPool(ctx context.Context) (_ client.Object, _ *ctrl.Result, err error) {
	h.pool = &v1alpha1.InfobloxIPPool{}
	if err = h.Client.Get(ctx, types.NamespacedName{Namespace: h.claim.Namespace, Name: h.claim.Spec.PoolRef.Name}, h.pool); err != nil {
		return nil, nil, err
	}

	// FetchPool's caller implementation currently reads the GroupVersionKind off the pool
	// object rather than resolving it from the scheme. Different client implementations give no guarantee
	// on whether they populate or (intentionally) discard these fields on get calls though.
	// See: https://github.com/kubernetes-sigs/controller-runtime/pull/2943#pullrequestreview-2305262466
	h.pool.GetObjectKind().SetGroupVersionKind(v1alpha1.GroupVersion.WithKind(v1alpha1.InfobloxIPPoolKind))

	if annotations.HasPaused(h.claim) && h.claim.DeletionTimestamp.IsZero() {
		log.FromContext(ctx).Info("IPAddressClaim is paused, skipping reconciliation", "IPAddressClaim", h.claim.Name)
		return h.pool, &ctrl.Result{}, nil
	}

	// Readiness describes whether the pool can hand out new addresses. It says nothing about
	// whether an address already taken from it can be released. So the gate applies to allocation only.
	//
	// An absent condition counts as not ready: a pool that has never been reconciled has not been
	// validated against Infoblox, and its network view, DNS view and subnets may not exist.
	// Block new allocations against a pool being deleted, but still allow existing
	// claims to be cleaned up (ReleaseAddress still needs to run for them).
	if !h.pool.GetDeletionTimestamp().IsZero() && h.claim.GetDeletionTimestamp().IsZero() {
		conditions.Set(h.claim, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.PoolNotReadyReason,
			Message: "the referenced pool is being deleted",
		})
		return h.pool, nil, fmt.Errorf("pool is being deleted")
	}

	// Block new allocations against unready pools, but allow deleting claims to
	// proceed so ReleaseAddress can clean up allocations during pool deletion.
	if h.claim.GetDeletionTimestamp().IsZero() && !conditions.IsTrue(h.pool, clusterv1.ReadyCondition) {
		message := "the referenced pool is not ready"
		if conditions.Get(h.pool, clusterv1.ReadyCondition) == nil {
			message = "the referenced pool does not have a Ready condition"
		}
		conditions.Set(h.claim, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.PoolNotReadyReason,
			Message: message,
		})
		return h.pool, nil, fmt.Errorf("pool not ready: %s", message)
	}
	return h.pool, nil, h.ensureIBClient(ctx, h.pool.Spec.InstanceRef.Name)
}

func (h *InfobloxClaimHandler) ensureIBClient(ctx context.Context, instanceName string) (err error) {
	if h.ibclient != nil {
		return nil
	}
	if h.ibclient, err = h.getInfobloxClientForInstance(ctx, h.Client, instanceName, h.operatorNamespace, h.getInfobloxClientFunc); err != nil {
		return fmt.Errorf("failed to create Infoblox client for instance %q: %w", instanceName, err)
	}
	return nil
}

// EnsureAddress ensures address.
func (h *InfobloxClaimHandler) EnsureAddress(ctx context.Context, address *ipamv1.IPAddress) (*ctrl.Result, error) {
	if h.pool == nil {
		return nil, errors.New("pool not found")
	}

	hostName, err := h.ensureHostname(ctx)
	if err != nil {
		return nil, err
	}

	err = h.ensureIBClient(ctx, h.pool.Spec.InstanceRef.Name)
	if err != nil {
		return nil, err
	}

	addressWasAllocated := address.Spec.Address != ""
	var errs []error
	dnsView := determineDNSView(h.pool.Spec.DNSView, h.ibclient.GetHostConfig().DefaultDNSView, h.pool.Spec.NetworkView)
	logger := log.FromContext(ctx).WithValues("hostname", hostName)
	for _, sub := range h.pool.Spec.Subnets {
		subnet, err := netip.ParsePrefix(sub.CIDR)
		if err != nil {
			// We won't set a condition here since this should be caught by validation
			logger.Error(err, "failed to parse subnet", "subnet", subnet)
			continue
		}

		allocatedAddr, err := h.ibclient.GetOrAllocateAddress(h.pool.Spec.NetworkView, dnsView, subnet, hostName, h.pool.Spec.DNSZone, logger)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		address.Spec.Address = allocatedAddr.String()
		address.Spec.Prefix = ptr.To(int32(subnet.Bits())) //nolint:gosec // subnet prefix bits are always 0-128
		address.Spec.Gateway = sub.Gateway

		// Note where in Infoblox this reservation lives, so that releasing it does not require the pool.
		// This is kept on the object for the same reason the hostname is cached on
		// the claim: by the time the address is released, the pool is not guaranteed to still
		// cotain this allocation in its subnets list, or even to still exist.
		if address.Annotations == nil {
			address.Annotations = map[string]string{}
		}
		address.Annotations[infobloxInstanceAnnotation] = h.pool.Spec.InstanceRef.Name
		address.Annotations[networkViewAnnotation] = h.pool.Spec.NetworkView
		address.Annotations[dnsViewAnnotation] = dnsView

		conditions.Set(h.claim, metav1.Condition{
			Type:   clusterv1.ReadyCondition,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.AddressAllocatedReason,
		})

		return nil, nil
	}

	switch {
	case len(errs) > 0:
		err = errors.Join(errs...)
	default:
		if addressWasAllocated {
			err = fmt.Errorf("allocated address %q is not in any subnet in the referenced pool", address.Spec.Address)
		} else {
			err = errors.New("no (valid) subnets in IPPool")
		}
	}
	conditions.Set(h.claim, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha1.AllocationFailedReason,
		Message: err.Error(),
	})
	logger.Error(err, "unable to ensure address allocated")
	return nil, err
}

// ReleaseAddress releases address.
func (h *InfobloxClaimHandler) ReleaseAddress(ctx context.Context) (*ctrl.Result, error) {
	logger := log.FromContext(ctx)

	address, err := h.allocatedAddress(ctx)
	if err != nil {
		return nil, err
	}
	if address == nil {
		// Nothing was ever allocated for this claim, or it has already been released and the
		// IPAddress removed. Either way there is no reservation left to leak.
		logger.Info("Claim holds no address, nothing to release")
		return nil, nil
	}

	subnet, err := allocatedSubnet(address)
	if err != nil {
		return nil, h.releaseFailed(err)
	}

	instanceName, networkView, dnsView, err := h.releaseCoordinates(address)
	if err != nil {
		return nil, h.releaseFailed(err)
	}

	hostName, err := h.getHostname(ctx)
	if err != nil {
		return nil, h.releaseFailed(fmt.Errorf("failed to get hostname: %w", err))
	}

	logger = logger.WithValues(
		"instance", instanceName,
		"address", address.Spec.Address,
		"networkView", networkView,
		"dnsView", dnsView,
		"subnet", subnet,
		"hostname", hostName,
	)

	err = h.ensureIBClient(ctx, instanceName)
	if err != nil {
		return nil, err
	}
	if err := h.ibclient.ReleaseAddress(networkView, dnsView, subnet, hostName, logger); err != nil {
		return nil, h.releaseFailed(fmt.Errorf("failed to release address %q: %w", address.Spec.Address, err))
	}

	logger.Info("Successfully released address")
	return nil, nil
}

// allocatedAddress returns the IPAddress belonging to the claim, or nil if there is none.
//
// The address is looked up by the claim's own name rather than through status.addressRef. Upstream
// derives the name of an IPAddress from its claim and fetches it the same way when deleting, and
// unlike the status field - which is written by a patch that trails the creation of the address -
// the name cannot go stale. A status that has not caught up would otherwise read as "nothing was
// ever allocated" and let the claim go while its reservation stays behind in Infoblox.
func (h *InfobloxClaimHandler) allocatedAddress(ctx context.Context) (*ipamv1.IPAddress, error) {
	address := &ipamv1.IPAddress{}
	key := types.NamespacedName{Namespace: h.claim.Namespace, Name: h.claim.Name}
	if err := h.Client.Get(ctx, key, address); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to fetch the address of the claim: %w", err)
	}
	return address, nil
}

// allocatedSubnet reconstructs the subnet an address was allocated from. Looking a host record up
// by hostname finds all of its addresses, and the subnet is what selects the one to drop, so it has
// to describe what was allocated rather than what the pool currently offers.
func allocatedSubnet(address *ipamv1.IPAddress) (netip.Prefix, error) {
	addr, err := netip.ParseAddr(address.Spec.Address)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("address %q is not an IP address: %w", address.Spec.Address, err)
	}
	if address.Spec.Prefix == nil {
		return netip.Prefix{}, fmt.Errorf("address %q has no prefix length recorded", address.Spec.Address)
	}
	prefix := netip.PrefixFrom(addr, int(*address.Spec.Prefix)).Masked()
	if !prefix.IsValid() {
		return netip.Prefix{}, fmt.Errorf("address %q with prefix length %d does not form a valid subnet", address.Spec.Address, *address.Spec.Prefix)
	}
	return prefix, nil
}

// releaseCoordinates returns the Infoblox instance, network view and DNS view to release against,
// as recorded on the IPAddress when it was allocated.
func (h *InfobloxClaimHandler) releaseCoordinates(address *ipamv1.IPAddress) (instanceName, networkView, dnsView string, err error) {
	instanceName = address.Annotations[infobloxInstanceAnnotation]
	networkView = address.Annotations[networkViewAnnotation]
	dnsView = address.Annotations[dnsViewAnnotation]
	if instanceName != "" && networkView != "" && dnsView != "" {
		return instanceName, networkView, dnsView, nil
	}

	// fallback to the pools info, which is what was used before the annotations were added. The pool may be gone though.
	if h.ibclient == nil || h.pool == nil || h.pool.Spec.NetworkView == "" {
		return "", "", "", fmt.Errorf(
			"cannot determine the Infoblox views this address was allocated from: it predates the annotations "+
				"recording them, and pool %q is gone or no longer describes an allocation. Restore it to let deletion proceed",
			h.claim.Spec.PoolRef.Name)
	}

	return h.pool.Spec.InstanceRef.Name,
		h.pool.Spec.NetworkView,
		determineDNSView(h.pool.Spec.DNSView, h.ibclient.GetHostConfig().DefaultDNSView, h.pool.Spec.NetworkView),
		nil
}

// releaseFailed records on the claim why its address could not be released. The claim keeps its
// finalizer: a reservation that cannot be released must not be dropped silently.
func (h *InfobloxClaimHandler) releaseFailed(err error) error {
	conditions.Set(h.claim, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha1.ReleaseFailedReason,
		Message: err.Error(),
	})
	return err
}

// GetPool returns local pool.
func (h *InfobloxClaimHandler) GetPool() client.Object {
	return h.pool
}

// ensureHostname gets the hostname from the claim and
// ensures it's compatible with the DNS setting of the references IPPool.
func (h *InfobloxClaimHandler) ensureHostname(ctx context.Context) (string, error) {
	hostname, err := h.getHostname(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get hostname: %w", err)
	}

	// Since we can't guarantee that resolving the hostname during machine deletion will succeed, we store it as an annotation
	// on the claim, and retrieve it during deletion to delete the infoblox record.
	if h.claim.Annotations == nil {
		h.claim.Annotations = map[string]string{}
	}
	h.claim.Annotations[hostnameAnnotation] = hostname

	// ensure that the hostnames suffix matches the given zone
	if !strings.HasSuffix(hostname, h.pool.Spec.DNSZone) {
		return "", fmt.Errorf("hostname %q must have DNS zone %q as suffix", hostname, h.pool.Spec.DNSZone)
	}

	return hostname, nil
}

func (h *InfobloxClaimHandler) getHostname(ctx context.Context) (string, error) {
	// always prefer the annotation if set
	hostName := h.claim.Annotations[hostnameAnnotation]
	if hostName != "" {
		return hostName, nil
	}

	if h.pool.Spec.DNSZone == "" {
		return h.claim.Name, nil
	}

	hostnameHandler, err := h.newHostnameResolver(h.Client, h.claim)
	if err != nil {
		return "", fmt.Errorf("failed to create hostname handler: %w", err)
	}

	hostName, err = hostnameHandler.GetHostname(ctx, h.claim)
	if err != nil {
		return "", err
	}

	if h.pool.Spec.DNSZone != "" {
		hostName += "." + h.pool.Spec.DNSZone
	}

	return hostName, nil
}

// NewHostnameResolver returns the resolver used to derive a hostname for a claim, which searches
// the claim's owner references for the Machine it belongs to.
func NewHostnameResolver(cl client.Client, _ *ipamv1.IPAddressClaim) (hostname.Resolver, error) {
	return &hostname.SearchOwnerReferenceResolver{
		Client:    cl,
		SearchFor: metav1.GroupKind{Group: "cluster.x-k8s.io", Kind: "Machine"},
		MaxDepth:  5,
	}, nil
}
