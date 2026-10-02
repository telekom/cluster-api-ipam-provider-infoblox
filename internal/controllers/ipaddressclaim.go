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

	"github.com/go-logr/logr"
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
	// hostnameAnnotation caches the claim's hostname, so releasing does not depend on its owners still existing.
	hostnameAnnotation = "ipam.cluster.x-k8s.io/hostname"

	// The following annotations record on an IPAddress where in Infoblox its reservation lives.
	infobloxInstanceAnnotation = "ipam.cluster.x-k8s.io/infoblox-instance"
	networkViewAnnotation      = "ipam.cluster.x-k8s.io/network-view"
	dnsViewAnnotation          = "ipam.cluster.x-k8s.io/dns-view"
)

// GetInfobloxClientForInstanceFn resolves the Infoblox client for the instance a pool refers to.
type GetInfobloxClientForInstanceFn func(ctx context.Context, c client.Reader, instanceName, operatorNamespace string, getClient infoblox.GetClientFunc) (infoblox.Client, error)

// NewHostnameResolverFn builds the resolver used to derive a hostname for a claim.
type NewHostnameResolverFn func(c client.Client, claim *ipamv1.IPAddressClaim) (hostname.Resolver, error)

// InfobloxProviderAdapter plugs InfobloxIPPools into the generic IPAddressClaim reconciler.
type InfobloxProviderAdapter struct {
	GetInfobloxClientFunc   infoblox.GetClientFunc
	OperatorNamespace       string
	MaxConcurrentReconciles int
	// K8sClient is the cached client used to map pool events to claims.
	K8sClient client.Client
	// K8sReader reads straight from the API server, bypassing the cache.
	K8sReader client.Reader

	// GetInfobloxClientForInstanceFunc resolves the Infoblox client for the instance a pool refers to.
	GetInfobloxClientForInstanceFunc GetInfobloxClientForInstanceFn
	// NewHostnameResolverFunc builds the hostname resolver for a claim.
	NewHostnameResolverFunc NewHostnameResolverFn
}

var _ ipamutil.ProviderAdapter = &InfobloxProviderAdapter{}

// InfobloxClaimHandler allocates and releases the address of a single IPAddressClaim in Infoblox.
type InfobloxClaimHandler struct {
	k8sClient client.Client
	k8sReader client.Reader

	claim             *ipamv1.IPAddressClaim
	pool              *v1alpha1.InfobloxIPPool
	operatorNamespace string

	getInfobloxClientFunc        infoblox.GetClientFunc
	getInfobloxClientForInstance GetInfobloxClientForInstanceFn
	newHostnameResolver          NewHostnameResolverFn
}

var _ ipamutil.ClaimHandler = &InfobloxClaimHandler{}

// SetupWithManager adds the Infoblox specific watches and options to the claim controller.
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

// infobloxIPPoolToIPClaims maps an InfobloxIPPool to requests for all claims referencing it.
func (r *InfobloxProviderAdapter) infobloxIPPoolToIPClaims(ctx context.Context, obj client.Object) []reconcile.Request {
	if r.K8sClient == nil {
		return nil
	}

	pool, ok := obj.(*v1alpha1.InfobloxIPPool)
	if !ok {
		return nil
	}

	logger := log.FromContext(ctx)
	claims := &ipamv1.IPAddressClaimList{}
	err := r.K8sClient.List(ctx, claims,
		client.MatchingFields{
			index.IPAddressClaimPoolRefCombinedField: index.IPPoolRefValue(ipamv1.IPPoolReference{
				APIGroup: v1alpha1.GroupVersion.Group,
				Kind:     v1alpha1.InfobloxIPPoolKind,
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

// ClaimHandlerFor returns the handler for a single reconciliation of the given claim.
func (r *InfobloxProviderAdapter) ClaimHandlerFor(cl client.Client, claim *ipamv1.IPAddressClaim) ipamutil.ClaimHandler {
	return &InfobloxClaimHandler{
		k8sClient:                    cl,
		k8sReader:                    r.K8sReader,
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

// FetchPool fetches the claim's pool. Unless the claim is being deleted, a paused claim or a pool that is not
// ready stops the reconciliation here. h.pool stays nil if the pool cannot be fetched.
func (h *InfobloxClaimHandler) FetchPool(ctx context.Context) (_ client.Object, _ *ctrl.Result, err error) {
	pool := &v1alpha1.InfobloxIPPool{}
	if err = h.k8sClient.Get(ctx, types.NamespacedName{Namespace: h.claim.Namespace, Name: h.claim.Spec.PoolRef.Name}, pool); err != nil {
		return nil, nil, err
	}
	h.pool = pool

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
	if h.claim.GetDeletionTimestamp().IsZero() &&
		!conditions.IsTrue(h.pool, clusterv1.ReadyCondition) {
		message := fmt.Sprintf("InfobloxIPPool %q is not ready", h.pool.Name)
		if conditions.Get(h.pool, clusterv1.ReadyCondition) == nil {
			message = fmt.Sprintf("InfobloxIPPool %q has not been validated yet, it has no Ready condition", h.pool.Name)
		}
		conditions.Set(h.claim, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.PoolNotReadyReason,
			Message: message,
		})
		return h.pool, nil, errors.New(message)
	}
	return h.pool, nil, nil
}

// newIBClient returns the Infoblox client for the given instance.
func (h *InfobloxClaimHandler) newIBClient(ctx context.Context, instanceName string) (infoblox.Client, error) {
	ibc, err := h.getInfobloxClientForInstance(ctx, h.k8sClient, instanceName, h.operatorNamespace, h.getInfobloxClientFunc)
	if err != nil {
		return nil, fmt.Errorf("failed to create Infoblox client for instance %q: %w", instanceName, err)
	}
	return ibc, nil
}

// EnsureAddress allocates an address for a claim that has none yet, and verifies the address of a claim that has one.
func (h *InfobloxClaimHandler) EnsureAddress(ctx context.Context, address *ipamv1.IPAddress) (*ctrl.Result, error) {
	hostName, err := h.ensureHostname(ctx)
	if err != nil {
		return nil, err
	}

	// An existing address is checked where it was allocated, which is not necessarily the pool's instance.
	instanceName := h.pool.Spec.InstanceRef.Name
	if address.Spec.Address != "" && address.Annotations[infobloxInstanceAnnotation] != "" {
		instanceName = address.Annotations[infobloxInstanceAnnotation]
	}
	ibc, err := h.newIBClient(ctx, instanceName)
	if err != nil {
		conditions.Set(h.claim, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ConfigurationInvalidReason,
			Message: err.Error(),
		})
		return nil, err
	}

	logger := log.FromContext(ctx).WithValues("instance", instanceName, "hostname", hostName)
	if address.Spec.Address == "" {
		err = h.allocateNewAddress(ctx, logger, ibc, address, hostName)
	} else {
		err = h.verifyAllocatedAddress(logger, ibc, address, hostName, instanceName)
	}
	if err != nil {
		return nil, err
	}
	conditions.Set(h.claim, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: v1alpha1.AddressAllocatedReason,
	})
	return nil, nil
}

// allocateNewAddress reserves an address for the claim's host in the first pool subnet that has one available.
func (h *InfobloxClaimHandler) allocateNewAddress(ctx context.Context, logger logr.Logger, ibc infoblox.Client, address *ipamv1.IPAddress, hostName string) error {
	// The cache may not show an existing IPAddress yet; only an error keeps CreateOrPatch from allocating and creating it again.
	err := h.k8sReader.Get(ctx, client.ObjectKeyFromObject(address), &ipamv1.IPAddress{})
	if err == nil {
		return fmt.Errorf("IPAddress %q exists but is not in the cache yet", address.Name)
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to read IPAddress %q from the API server: %w", address.Name, err)
	}

	dnsView := determineDNSView(h.pool.Spec.DNSView, ibc.GetHostConfig().DefaultDNSView, h.pool.Spec.NetworkView)
	logger = logger.WithValues("networkView", h.pool.Spec.NetworkView, "dnsView", dnsView)

	var errs []error
	fullSubnets := 0
	for _, sub := range h.pool.Spec.Subnets {
		logger := logger.WithValues("subnet", sub.CIDR)
		subnet, err := netip.ParsePrefix(sub.CIDR)
		if err != nil {
			// We won't set a condition here since this should be caught by validation
			logger.Error(err, "failed to parse subnet")
			continue
		}

		allocatedAddr, err := ibc.GetOrAllocateAddress(h.pool.Spec.NetworkView, dnsView, subnet, hostName, h.pool.Spec.DNSZone, logger)
		if err != nil {
			errs = append(errs, fmt.Errorf("subnet %s: %w", subnet, err))
			if infoblox.IsNoAddressAvailableError(err) {
				fullSubnets++
			}
			continue
		}

		address.Spec.Address = allocatedAddr.String()
		address.Spec.Prefix = ptr.To(int32(subnet.Bits())) //nolint:gosec // subnet prefix bits are always 0-128
		address.Spec.Gateway = sub.Gateway
		recordAllocation(address, h.pool.Spec.InstanceRef.Name, h.pool.Spec.NetworkView, dnsView)

		logger.Info("new address allocated", "address", address.Spec.Address)
		return nil
	}

	reason := v1alpha1.AllocationFailedReason
	switch {
	case len(errs) > 0:
		err = fmt.Errorf("failed to allocate an address for host %q from InfobloxIPPool %q: %w", hostName, h.pool.Name, errors.Join(errs...))
		// With any other error, the pool is not proven to be exhausted, and that error needs attention first.
		if fullSubnets == len(errs) {
			reason = v1alpha1.NoAddressAvailableReason
		}
	default:
		err = fmt.Errorf("InfobloxIPPool %q has no valid subnets", h.pool.Name)
	}
	conditions.Set(h.claim, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	})
	logger.Error(err, "unable to ensure address allocated")
	return err
}

// verifyAllocatedAddress checks that the address of an existing IPAddress is still assigned to the claim's host in Infoblox.
// A missing reservation is reported, not replaced: the IPAddress is immutable and the address is likely still in use.
func (h *InfobloxClaimHandler) verifyAllocatedAddress(logger logr.Logger, ibc infoblox.Client, address *ipamv1.IPAddress, hostName, instanceName string) error {
	addr, err := netip.ParseAddr(address.Spec.Address)
	if err != nil {
		err = fmt.Errorf("IPAddress %q holds an invalid address %q: %w", address.Name, address.Spec.Address, err)
		conditions.Set(h.claim, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.AddressInvalidReason,
			Message: err.Error(),
		})
		return err
	}

	networkView := h.pool.Spec.NetworkView
	annotated := address.Annotations[infobloxInstanceAnnotation] != ""
	if annotated {
		networkView = address.Annotations[networkViewAnnotation]
	}

	assigned, err := ibc.IsAddressAssigned(networkView, hostName, addr)
	if err != nil {
		err = fmt.Errorf("failed to verify address %s of IPAddress %q in Infoblox: %w", addr, address.Name, err)
		conditions.Set(h.claim, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.VerificationFailedReason,
			Message: err.Error(),
		})
		return err
	}
	if !assigned {
		err := fmt.Errorf("address %s of IPAddress %q is not assigned to host %q in Infoblox instance %q, network view %q; "+
			"not allocating a replacement, the Infoblox reservation has to be restored", addr, address.Name, hostName, instanceName, networkView)
		conditions.Set(h.claim, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.AddressDriftedReason,
			Message: err.Error(),
		})
		return err
	}

	// Addresses allocated before the annotations were introduced get them now. They were checked against the pool's instance.
	if !annotated {
		dnsView := determineDNSView(h.pool.Spec.DNSView, ibc.GetHostConfig().DefaultDNSView, h.pool.Spec.NetworkView)
		recordAllocation(address, h.pool.Spec.InstanceRef.Name, h.pool.Spec.NetworkView, dnsView)
	}
	logger.V(1).Info("address verification successful", "address", address.Spec.Address)
	return nil
}

// ReleaseAddress releases the address recorded on the claim's IPAddress back to Infoblox.
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

	// The claim keeps its finalizer on any error: a reservation that cannot be released must not be dropped silently.
	releaseFailed := func(err error) (*ctrl.Result, error) {
		conditions.Set(h.claim, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ReleaseFailedReason,
			Message: err.Error(),
		})
		return nil, err
	}

	subnet, err := allocatedSubnet(address)
	if err != nil {
		return releaseFailed(err)
	}

	instanceName, networkView, dnsView, ibc, err := h.releaseCoordinates(ctx, address)
	if err != nil {
		return releaseFailed(err)
	}

	hostName, err := h.getHostname(ctx)
	if err != nil {
		return releaseFailed(fmt.Errorf("failed to get hostname: %w", err))
	}

	logger = logger.WithValues(
		"instance", instanceName,
		"address", address.Spec.Address,
		"networkView", networkView,
		"dnsView", dnsView,
		"subnet", subnet,
		"hostname", hostName,
	)

	if ibc == nil {
		ibc, err = h.newIBClient(ctx, instanceName)
		if err != nil {
			return releaseFailed(err)
		}
	}

	err = ibc.ReleaseAddress(networkView, dnsView, subnet, hostName, logger)
	if err != nil {
		return releaseFailed(fmt.Errorf("failed to release address %s of host %q: %w", address.Spec.Address, hostName, err))
	}

	logger.Info("Successfully released address")
	return nil, nil
}

// allocatedAddress returns the IPAddress belonging to the claim, or nil if there is none.
// It is looked up by the claim's name like upstream does, as status.addressRef may not have caught up with its creation yet.
func (h *InfobloxClaimHandler) allocatedAddress(ctx context.Context) (*ipamv1.IPAddress, error) {
	address := &ipamv1.IPAddress{}
	key := types.NamespacedName{Namespace: h.claim.Namespace, Name: h.claim.Name}
	if err := h.k8sClient.Get(ctx, key, address); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get IPAddress %q of the claim: %w", key.Name, err)
	}
	return address, nil
}

// allocatedSubnet reconstructs the subnet an address was allocated from out of its address and prefix,
// as the pool may no longer contain that subnet.
func allocatedSubnet(address *ipamv1.IPAddress) (netip.Prefix, error) {
	addr, err := netip.ParseAddr(address.Spec.Address)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("IPAddress %q holds an invalid address %q: %w", address.Name, address.Spec.Address, err)
	}
	if address.Spec.Prefix == nil {
		return netip.Prefix{}, fmt.Errorf("IPAddress %q has no prefix length recorded", address.Name)
	}
	prefix := netip.PrefixFrom(addr, int(*address.Spec.Prefix)).Masked()
	if !prefix.IsValid() {
		return netip.Prefix{}, fmt.Errorf("IPAddress %q: address %s with prefix length %d does not form a valid subnet", address.Name, addr, *address.Spec.Prefix)
	}
	return prefix, nil
}

// releaseCoordinates returns the Infoblox instance, network view and DNS view to release against, as recorded on
// the IPAddress, falling back to the pool for addresses allocated before these were recorded. In the fallback, it
// also returns the client of the pool's instance, which it needs for the default DNS view.
func (h *InfobloxClaimHandler) releaseCoordinates(ctx context.Context, address *ipamv1.IPAddress) (instanceName, networkView, dnsView string, ibc infoblox.Client, err error) {
	instanceName = address.Annotations[infobloxInstanceAnnotation]
	networkView = address.Annotations[networkViewAnnotation]
	dnsView = address.Annotations[dnsViewAnnotation]
	if instanceName != "" && networkView != "" && dnsView != "" {
		return instanceName, networkView, dnsView, nil, nil
	}

	if h.pool == nil || h.pool.Spec.NetworkView == "" {
		return "", "", "", nil, fmt.Errorf(
			"cannot determine the Infoblox views this address was allocated from: it predates the annotations "+
				"recording them, and pool %q is gone or no longer describes an allocation. Restore it to let deletion proceed",
			h.claim.Spec.PoolRef.Name)
	}
	ibc, err = h.newIBClient(ctx, h.pool.Spec.InstanceRef.Name)
	if err != nil {
		return "", "", "", nil, err
	}

	return h.pool.Spec.InstanceRef.Name,
		h.pool.Spec.NetworkView,
		determineDNSView(h.pool.Spec.DNSView, ibc.GetHostConfig().DefaultDNSView, h.pool.Spec.NetworkView),
		ibc,
		nil
}

// GetPool returns the pool fetched by FetchPool.
func (h *InfobloxClaimHandler) GetPool() client.Object {
	return h.pool
}

// ensureHostname returns the claim's hostname, caches it on the claim and checks that it is within the pool's DNS zone.
func (h *InfobloxClaimHandler) ensureHostname(ctx context.Context) (string, error) {
	hostname, err := h.getHostname(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get hostname: %w", err)
	}

	// Resolving the hostname may fail once the claim's owners are deleted, so releasing relies on this cached copy.
	if h.claim.Annotations == nil {
		h.claim.Annotations = map[string]string{}
	}
	h.claim.Annotations[hostnameAnnotation] = hostname

	if !strings.HasSuffix(hostname, h.pool.Spec.DNSZone) {
		return "", fmt.Errorf("hostname %q must have DNS zone %q as suffix", hostname, h.pool.Spec.DNSZone)
	}

	return hostname, nil
}

// getHostname returns the hostname cached on the claim. Without one, it is the claim's name if the pool has
// no DNS zone, and the name of the Machine owning the claim within the zone otherwise.
func (h *InfobloxClaimHandler) getHostname(ctx context.Context) (string, error) {
	// always prefer the annotation if set
	hostName := h.claim.Annotations[hostnameAnnotation]
	if hostName != "" {
		return hostName, nil
	}

	// Without the pool's DNS zone, the claim's name is only a guess, and a wrong one releases nothing.
	if h.pool == nil {
		return "", fmt.Errorf("the claim has no %q annotation and InfobloxIPPool %q is gone; "+
			"restore the pool or set the annotation to the name of the Infoblox host record",
			hostnameAnnotation, h.claim.Spec.PoolRef.Name)
	}

	// If the pool has no DNS zone, the claim's name is used as hostname and we are done.
	if h.pool.Spec.DNSZone == "" {
		return h.claim.Name, nil
	}

	resolver, err := h.newHostnameResolver(h.k8sClient, h.claim)
	if err != nil {
		return "", fmt.Errorf("failed to create hostname handler: %w", err)
	}

	hostName, err = resolver.GetHostname(ctx, h.claim)
	if err != nil {
		return "", err
	}
	return hostName + "." + h.pool.Spec.DNSZone, nil
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

// recordAllocation records on an IPAddress where in Infoblox its reservation lives, so releasing it does not
// depend on the pool, which may have changed or be gone by then.
func recordAllocation(address *ipamv1.IPAddress, instance, networkView, dnsView string) {
	if address.Annotations == nil {
		address.Annotations = map[string]string{}
	}
	address.Annotations[infobloxInstanceAnnotation] = instance
	address.Annotations[networkViewAnnotation] = networkView
	address.Annotations[dnsViewAnnotation] = dnsView
}
