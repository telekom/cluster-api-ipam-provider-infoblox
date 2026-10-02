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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/hostname"
	hostnamemock "github.com/telekom/cluster-api-ipam-provider-infoblox/internal/hostname/mock"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox/ibmock"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

// IgnoreUIDsOnIPAddress drops the fields that reference generated UIDs from an IPAddress
// comparison.
var IgnoreUIDsOnIPAddress = IgnorePaths{
	"TypeMeta",
	"ObjectMeta.OwnerReferences[0].UID",
	"ObjectMeta.OwnerReferences[1].UID",
	"ObjectMeta.OwnerReferences[2].UID",
	"Spec.Claim.UID",
	"Spec.Pool.UID",
}

var ipamAPIVersion = ipamv1.GroupVersion.String()

// defaultSubnets is the subnet layout used by most pool fixtures.
var defaultSubnets = []v1alpha1.Subnet{{CIDR: "10.0.0.0/24", Gateway: "10.0.0.1"}}

// networkFullError is what the Infoblox client returns for a full network, as observed on WAPI 2.11.2.
var networkFullError = infoblox.RequestError{
	Endpoint:  "infoblox.example:443",
	Operation: "UpdateHostRecord",
	Err: infoblox.WapiError{
		HTTPError: infoblox.HTTPError{StatusCode: 400},
		Code:      "Client.Ibap.Data.Conflict",
		Message:   "Cannot find 1 available IP address(es) in this network.",
	},
}

// failingReader fails every Get with err.
type failingReader struct {
	client.Reader
	err error
}

func (r failingReader) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return r.err
}

// The claim reconciler is driven directly, one reconciliation at a time. Everything it depends on
// is injected through the adapter the spec builds, so there is no shared mock state and no
// controller running in the background.
//
// Claim pausing and pool kind filtering are enforced by watch predicates rather than by Reconcile,
// so they are not covered here. ClaimReferencesPoolKind is unit tested in pkg/predicates, and the
// paused predicate belongs to the upstream ClaimReconciler.
var _ = Describe("IPAddressClaimReconciler", func() {
	const (
		poolName     = "test-pool"
		claimName    = "test-claim"
		instanceName = "test-instance"
	)

	var (
		namespace    string
		infobloxMock *ibmock.MockClient
		resolverMock *hostnamemock.MockResolver
		// resolverErr makes building the hostname resolver fail. Specs that exercise that path set
		// it before reconciling.
		resolverErr error
		// requestedInstances records the Infoblox instances the reconciler asked a client for.
		requestedInstances []string
		// clientErr makes creating an Infoblox client fail.
		clientErr  error
		reconciler *ipamutil.ClaimReconciler
	)

	// reconcileClaim runs a single reconciliation for the named claim.
	reconcileClaim := func(name string) (ctrl.Result, error) {
		return reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: client.ObjectKey{Name: name, Namespace: namespace},
		})
	}

	// reconcileAllocatedClaim runs the two reconciliations an allocation takes: the first only adds
	// the release finalizer and returns, the second does the work.
	reconcileAllocatedClaim := func(name string) (ctrl.Result, error) {
		By("running the reconciliation that adds the release finalizer")
		res, err := reconcileClaim(name)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		ExpectWithOffset(1, res).To(Equal(ctrl.Result{}))

		By("running the reconciliation that allocates the address")
		return reconcileClaim(name)
	}

	// getAddress reads the IPAddress allocated for a claim of the same name.
	getAddress := func(name string) *ipamv1.IPAddress {
		address := &ipamv1.IPAddress{}
		ExpectWithOffset(1, apiClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, address)).To(Succeed())
		return address
	}

	// expectNoAddress asserts that no IPAddress exists in the spec's namespace.
	expectNoAddress := func() {
		addresses := &ipamv1.IPAddressList{}
		ExpectWithOffset(1, apiClient.List(ctx, addresses, client.InNamespace(namespace))).To(Succeed())
		ExpectWithOffset(1, addresses.Items).To(BeEmpty())
	}

	// stripAllocationAnnotations removes the annotations the reconciler records on an IPAddress,
	// reproducing an address that was allocated before they were introduced.
	stripAllocationAnnotations := func() {
		address := &ipamv1.IPAddress{}
		ExpectWithOffset(1, apiClient.Get(ctx, client.ObjectKey{Name: claimName, Namespace: namespace}, address)).To(Succeed())
		address.Annotations = nil
		ExpectWithOffset(1, apiClient.Update(ctx, address)).To(Succeed())
	}

	// expectAllocationSucceeds makes the Infoblox mock hand out the given address, and asserts that
	// the reconciler asks for one.
	expectAllocationSucceeds := func(address string) {
		infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(netip.MustParseAddr(address), nil).MinTimes(1)
	}

	// expectAddressVerified makes the Infoblox mock report whether the given address is still assigned
	// to the claim's host, and asserts that no replacement is allocated.
	expectAddressVerified := func(networkView, address string, assigned bool) {
		infobloxMock.EXPECT().IsAddressAssigned(networkView, claimName, netip.MustParseAddr(address)).
			Return(assigned, nil).MinTimes(1)
		infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)
	}

	// expectReleaseSucceeds asserts that the address of the claim under test is released against
	// Infoblox, with the pool's view and subnet and the claim's hostname.
	expectReleaseSucceeds := func() {
		infobloxMock.EXPECT().
			ReleaseAddress("default", "default", netip.MustParsePrefix("10.0.0.0/24"), claimName, gomock.Any()).
			Return(nil).MinTimes(1)
	}

	// markPoolReady sets a pool's Ready condition to true, which is what the pool reconciler does
	// for a pool it validated successfully.
	markPoolReady := func(pool *v1alpha1.InfobloxIPPool) {
		pool.Status.Conditions = []metav1.Condition{{
			Type:               clusterv1.ReadyCondition,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReadyReason,
			Message:            "pool is ready",
			LastTransitionTime: metav1.Now(),
		}}
		ExpectWithOffset(1, apiClient.Status().Update(ctx, pool)).To(Succeed())
	}

	// markPoolNotReady sets a pool's Ready condition to false, which is what the pool reconciler
	// does for a pool whose validation failed.
	markPoolNotReady := func(pool *v1alpha1.InfobloxIPPool) {
		pool.Status.Conditions = []metav1.Condition{{
			Type:               clusterv1.ReadyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.NetworkNotFoundReason,
			Message:            "could not find network",
			LastTransitionTime: metav1.Now(),
		}}
		ExpectWithOffset(1, apiClient.Status().Update(ctx, pool)).To(Succeed())
	}

	// createPool creates a ready pool fixture in the spec's namespace.
	//
	// The Ready condition has to be set explicitly: claims are only served from pools whose Ready
	// condition is true, and the InfobloxIPPool reconciler that would normally set it is driven by
	// its own specs rather than running in the background here.
	createPool := func(subnets ...v1alpha1.Subnet) *v1alpha1.InfobloxIPPool {
		if len(subnets) == 0 {
			subnets = defaultSubnets
		}
		pool := &v1alpha1.InfobloxIPPool{
			ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: namespace},
			Spec: v1alpha1.InfobloxIPPoolSpec{
				InstanceRef: v1alpha1.InstanceReference{Name: instanceName},
				Subnets:     subnets,
				NetworkView: "default",
			},
		}
		ExpectWithOffset(1, apiClient.Create(ctx, pool)).To(Succeed())
		markPoolReady(pool)
		return pool
	}

	// expectedAddress builds the IPAddress the reconciler is expected to end up with for a claim of
	// the same name. Owner references that already existed are kept in front of the ones the
	// reconciler adds, matching the order it produces.
	expectedAddress := func(name, address, gateway string, existingOwnerRefs ...metav1.OwnerReference) ipamv1.IPAddress {
		ownerRefs := append([]metav1.OwnerReference{}, existingOwnerRefs...)
		ownerRefs = append(ownerRefs,
			metav1.OwnerReference{
				APIVersion:         ipamAPIVersion,
				BlockOwnerDeletion: ptr.To(true),
				Controller:         ptr.To(true),
				Kind:               "IPAddressClaim",
				Name:               name,
			},
			metav1.OwnerReference{
				APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
				BlockOwnerDeletion: ptr.To(true),
				Controller:         ptr.To(false),
				Kind:               "InfobloxIPPool",
				Name:               poolName,
			},
		)

		return ipamv1.IPAddress{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       namespace,
				Finalizers:      []string{ipamutil.ProtectAddressFinalizer},
				OwnerReferences: ownerRefs,
				Annotations: map[string]string{
					infobloxInstanceAnnotation: instanceName,
					networkViewAnnotation:      "default",
					dnsViewAnnotation:          "default",
				},
			},
			Spec: ipamv1.IPAddressSpec{
				ClaimRef: ipamv1.IPAddressClaimReference{Name: name},
				PoolRef: ipamv1.IPPoolReference{
					APIGroup: "ipam.cluster.x-k8s.io",
					Kind:     "InfobloxIPPool",
					Name:     poolName,
				},
				Address: address,
				Prefix:  ptr.To[int32](24),
				Gateway: gateway,
			},
		}
	}

	BeforeEach(func() {
		namespace = createNamespace()

		// Everything the reconciler talks to is built here and injected, so a spec never touches
		// shared state. A gomock controller scoped to the spec verifies the expectations when the
		// spec ends.
		mockCtrl := gomock.NewController(GinkgoT())
		infobloxMock = ibmock.NewMockClient(mockCtrl)
		infobloxMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).AnyTimes()
		resolverMock = hostnamemock.NewMockResolver(mockCtrl)
		resolverErr = nil
		requestedInstances = nil
		clientErr = nil

		reconciler = &ipamutil.ClaimReconciler{
			Client: apiClient,
			Scheme: apiClient.Scheme(),
			Adapter: &InfobloxProviderAdapter{
				K8sReader: apiClient,
				GetInfobloxClientForInstanceFunc: func(_ context.Context, _ client.Reader, instance, _ string, _ infoblox.GetClientFunc) (infoblox.Client, error) {
					requestedInstances = append(requestedInstances, instance)
					if clientErr != nil {
						return nil, clientErr
					}
					return infobloxMock, nil
				},
				NewHostnameResolverFunc: func(_ client.Client, _ *ipamv1.IPAddressClaim) (hostname.Resolver, error) {
					if resolverErr != nil {
						return nil, resolverErr
					}
					return resolverMock, nil
				},
			},
		}
	})

	When("a claim references a pool with a single subnet", func() {
		BeforeEach(func() {
			createPool()
		})

		It("should allocate an Address from the Pool", func() {
			expectAllocationSucceeds("10.0.0.2")
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			expected := expectedAddress(claimName, "10.0.0.2", "10.0.0.1")
			Expect(getAddress(claimName)).To(EqualObject(&expected, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
		})

		It("should mark the claim ready", func() {
			expectAllocationSucceeds("10.0.0.2")
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			Expect(Object(&claim)()).To(HaveField("Status.Conditions", ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionTrue)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.AddressAllocatedReason)),
			))))
		})

		// This spec guards a single line: the SetGroupVersionKind call in FetchPool.
		//
		// Why it can break. The upstream ClaimReconciler does not resolve the pool's kind from the
		// scheme. NewIPAddress reads it off the pool object we return from FetchPool to build
		// spec.poolRef, and ensureIPAddressOwnerReferences matches against it to find the pool's
		// owner reference. controller-runtime only populates that field on objects read through a
		// cache; the client used here reads straight from the API server, which decodes with
		// runtime.WithoutVersionDecoder and deliberately clears it. So FetchPool has to set it.
		//
		// What you will see if that line is removed. Allocation fails with
		//
		//	IPAddress "test-claim" is invalid:
		//	  [spec.poolRef.apiGroup: Required value, spec.poolRef.kind: Required value]
		//
		// and, were the CRD to stop requiring those fields, the owner reference flags below would
		// land on the claim's reference instead of the pool's, quietly violating the IPAM contract.
		It("should identify the pool on the IPAddress even though the API server clears TypeMeta", func() {
			expectAllocationSucceeds("10.0.0.2")
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			By("confirming the precondition: a pool read with this client carries no kind")
			fetched := &v1alpha1.InfobloxIPPool{}
			Expect(apiClient.Get(ctx, client.ObjectKey{Name: poolName, Namespace: namespace}, fetched)).To(Succeed())
			Expect(fetched.GetObjectKind().GroupVersionKind()).To(Equal(schema.GroupVersionKind{}),
				"controller-runtime clears TypeMeta on reads that bypass the cache, so FetchPool must set it")

			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())

			By("resolving spec.poolRef to the pool's group and kind")
			address := getAddress(claimName)
			Expect(address.Spec.PoolRef).To(Equal(ipamv1.IPPoolReference{
				APIGroup: v1alpha1.GroupVersion.Group,
				Kind:     "InfobloxIPPool",
				Name:     poolName,
			}))

			By("flagging the pool owner reference, not the claim's, per the IPAM contract")
			Expect(address.OwnerReferences).To(ContainElement(And(
				HaveField("Kind", Equal("InfobloxIPPool")),
				HaveField("Controller", HaveValue(BeFalse())),
				HaveField("BlockOwnerDeletion", HaveValue(BeTrue())),
			)))
			Expect(address.OwnerReferences).To(ContainElement(And(
				HaveField("Kind", Equal("IPAddressClaim")),
				HaveField("Controller", HaveValue(BeTrue())),
				HaveField("BlockOwnerDeletion", HaveValue(BeTrue())),
			)))
		})
	})

	When("the pool has several subnets", func() {
		var pool *v1alpha1.InfobloxIPPool

		BeforeEach(func() {
			pool = createPool(
				v1alpha1.Subnet{CIDR: "10.0.0.0/24", Gateway: "10.0.0.1"},
				v1alpha1.Subnet{CIDR: "10.0.1.0/24", Gateway: "10.0.1.1"},
			)
		})

		It("should allocate an Address from second subnet if there are no available addresses in first subnet", func() {
			subnet0 := netip.MustParsePrefix(pool.Spec.Subnets[0].CIDR)
			subnet1 := netip.MustParsePrefix(pool.Spec.Subnets[1].CIDR)
			infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), subnet0, gomock.Any(), gomock.Any(), gomock.Any()).
				Return(netip.Addr{}, errors.New("no available addresses")).MinTimes(1)
			infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), subnet1, gomock.Any(), gomock.Any(), gomock.Any()).
				Return(netip.MustParseAddr("10.0.1.2"), nil).MinTimes(1)

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			expected := expectedAddress(claimName, "10.0.1.2", "10.0.1.1")
			Expect(getAddress(claimName)).To(EqualObject(&expected, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
		})

		It("should not allocate an Address if the allocation fails in every subnet", func() {
			infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(netip.Addr{}, errors.New("no available addresses")).MinTimes(1)

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(And(
				ContainSubstring(`failed to allocate an address for host "test-claim" from InfobloxIPPool "test-pool"`),
				ContainSubstring("subnet 10.0.0.0/24: no available addresses"),
				ContainSubstring("subnet 10.0.1.0/24: no available addresses"),
			)))
			expectNoAddress()
			Expect(Object(&claim)()).To(HaveField("Status.Conditions", ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.AllocationFailedReason)),
			))))
		})

		It("should report NoAddressAvailable if every subnet is full", func() {
			infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(netip.Addr{}, networkFullError).MinTimes(1)

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring("Cannot find 1 available IP address(es) in this network.")))
			expectNoAddress()
			Expect(Object(&claim)()).To(HaveField("Status.Conditions", ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.NoAddressAvailableReason)),
			))))
		})

		// With another error, the pool is not proven to be exhausted.
		It("should report AllocationFailed if only some subnets are full", func() {
			subnet0 := netip.MustParsePrefix(pool.Spec.Subnets[0].CIDR)
			subnet1 := netip.MustParsePrefix(pool.Spec.Subnets[1].CIDR)
			infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), subnet0, gomock.Any(), gomock.Any(), gomock.Any()).
				Return(netip.Addr{}, networkFullError).MinTimes(1)
			infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), subnet1, gomock.Any(), gomock.Any(), gomock.Any()).
				Return(netip.Addr{}, errors.New("infoblox unavailable")).MinTimes(1)

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(HaveOccurred())
			expectNoAddress()
			Expect(Object(&claim)()).To(HaveField("Status.Conditions", ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.AllocationFailedReason)),
			))))
		})
	})

	When("the pool does not define a gateway for its subnets", func() {
		BeforeEach(func() {
			createPool(v1alpha1.Subnet{CIDR: "10.0.0.0/24"})
		})

		It("should allocate an Address without a gateway", func() {
			expectAllocationSucceeds("10.0.0.2")
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			expected := expectedAddress(claimName, "10.0.0.2", "")
			Expect(getAddress(claimName)).To(EqualObject(&expected, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
		})
	})

	When("the pool defines a DNS zone", func() {
		BeforeEach(func() {
			pool := &v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: namespace},
				Spec: v1alpha1.InfobloxIPPoolSpec{
					InstanceRef: v1alpha1.InstanceReference{Name: instanceName},
					Subnets:     defaultSubnets,
					NetworkView: "default",
					DNSZone:     "example.com",
				},
			}
			Expect(apiClient.Create(ctx, pool)).To(Succeed())
			markPoolReady(pool)
		})

		It("should allocate using the resolved hostname qualified with the zone", func() {
			resolverMock.EXPECT().GetHostname(gomock.Any(), gomock.Any()).Return("resolved-host", nil).MinTimes(1)
			infobloxMock.EXPECT().
				GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), "resolved-host.example.com", "example.com", gomock.Any()).
				Return(netip.MustParseAddr("10.0.0.2"), nil).MinTimes(1)

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			Expect(getAddress(claimName).Spec.Address).To(Equal("10.0.0.2"))

			By("caching the hostname on the claim so deletion does not depend on the resolver")
			Expect(Object(&claim)()).To(HaveField("ObjectMeta.Annotations",
				HaveKeyWithValue(hostnameAnnotation, "resolved-host.example.com")))
		})

		It("should prefer a hostname already annotated on the claim over the resolver", func() {
			infobloxMock.EXPECT().
				GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), "cached-host.example.com", "example.com", gomock.Any()).
				Return(netip.MustParseAddr("10.0.0.2"), nil).
				MinTimes(1)
			// expect resolver to never be hit
			resolverMock.EXPECT().
				GetHostname(gomock.Any(), gomock.Any()).
				Times(0)

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			claim.Annotations = map[string]string{hostnameAnnotation: "cached-host.example.com"}
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			Expect(getAddress(claimName).Spec.Address).To(Equal("10.0.0.2"))
		})

		It("should not allocate when the annotated hostname is outside the zone", func() {
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			claim.Annotations = map[string]string{hostnameAnnotation: "cached-host.other.example"}
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring(`must have DNS zone "example.com" as suffix`)))
			expectNoAddress()
		})

		It("should not allocate when the hostname resolver cannot be built", func() {
			resolverErr = errors.New("no resolver for you")

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring("failed to create hostname handler")))
			Expect(err).To(MatchError(ContainSubstring("no resolver for you")))
			expectNoAddress()
		})

		It("should not allocate when the hostname cannot be resolved", func() {
			resolverMock.EXPECT().GetHostname(gomock.Any(), gomock.Any()).
				Return("", errors.New("no machine owns this claim")).MinTimes(1)

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring("no machine owns this claim")))
			expectNoAddress()
		})

		It("should keep the claim when its hostname cannot be resolved for the release", func() {
			gomock.InOrder(
				resolverMock.EXPECT().GetHostname(gomock.Any(), gomock.Any()).Return("resolved-host", nil),
				resolverMock.EXPECT().GetHostname(gomock.Any(), gomock.Any()).
					Return("", errors.New("no machine owns this claim")).MinTimes(1),
			)
			expectAllocationSucceeds("10.0.0.2")
			infobloxMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())

			By("losing the cached hostname, as claims allocated by old versions never had it")
			Expect(apiClient.Get(ctx, client.ObjectKeyFromObject(&claim), &claim)).To(Succeed())
			delete(claim.Annotations, hostnameAnnotation)
			Expect(apiClient.Update(ctx, &claim)).To(Succeed())

			By("deleting the claim")
			Expect(apiClient.Delete(ctx, &claim)).To(Succeed())

			_, err = reconcileClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring("failed to get hostname: no machine owns this claim")))
			Expect(getAddress(claimName).Spec.Address).To(Equal("10.0.0.2"))
			Expect(Object(&claim)()).To(And(
				HaveField("ObjectMeta.Finalizers", ContainElement(ipamutil.ReleaseAddressFinalizer)),
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Reason", BeEquivalentTo(v1alpha1.ReleaseFailedReason)),
					HaveField("Message", ContainSubstring("failed to get hostname: no machine owns this claim")),
				))),
			))
		})
	})

	When("the API server cannot be asked whether the claim already has an IPAddress", func() {
		It("should not allocate an Address", func() {
			createPool()
			infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Times(0)
			reconciler.Adapter.(*InfobloxProviderAdapter).K8sReader = failingReader{Reader: apiClient, err: errors.New("api server unavailable")}
			claim := newClaim(claimName, namespace, v1alpha1.InfobloxIPPoolKind, poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring(`failed to read IPAddress "test-claim" from the API server: api server unavailable`)))
			expectNoAddress()
		})
	})

	When("the referenced pool does not exist", func() {
		It("should not allocate an Address", func() {
			claim := newClaim(claimName, namespace, "InfobloxIPPool", "no-such-pool")
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			expectNoAddress()
		})
	})

	When("the referenced pool has never been reconciled", func() {
		It("should not allocate an Address", func() {
			// No status at all, which is what a pool looks like between being created and being
			// validated by its own reconciler. Nothing has checked that its network view, DNS view
			// or subnets exist, so it must not be served from.
			Expect(apiClient.Create(ctx, &v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: namespace},
				Spec: v1alpha1.InfobloxIPPoolSpec{
					InstanceRef: v1alpha1.InstanceReference{Name: instanceName},
					Subnets:     defaultSubnets,
					NetworkView: "default",
				},
			})).To(Succeed())

			claim := newClaim(claimName, namespace, v1alpha1.InfobloxIPPoolKind, poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring(`InfobloxIPPool "test-pool" has not been validated yet`)))
			expectNoAddress()
			Expect(Object(&claim)()).To(HaveField("Status.Conditions", ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.PoolNotReadyReason)),
			))))
		})
	})

	When("the referenced pool is no longer ready", func() {
		// Readiness gates allocation, not release. A pool that went not ready for, say, a network
		// that disappeared would otherwise make every claim against it undeletable, and with it
		// every Machine and Cluster holding one.
		It("should still release the address and delete the claim", func() {
			expectAllocationSucceeds("10.0.0.2")
			expectReleaseSucceeds()
			pool := createPool()
			claim := newClaim(claimName, namespace, v1alpha1.InfobloxIPPoolKind, poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())

			By("failing the pool after the address was allocated")
			Expect(apiClient.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
			markPoolNotReady(pool)

			By("deleting the claim")
			Expect(apiClient.Delete(ctx, &claim)).To(Succeed())

			_, err = reconcileClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			err = apiClient.Get(ctx, client.ObjectKeyFromObject(&claim), &ipamv1.IPAddressClaim{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the claim to be gone, got %v", err)
			expectNoAddress()
		})
	})

	When("the pool is deleted before the claim", func() {
		var claim ipamv1.IPAddressClaim

		BeforeEach(func() {
			expectAllocationSucceeds("10.0.0.2")
			pool := createPool()
			claim = newClaim(claimName, namespace, v1alpha1.InfobloxIPPoolKind, poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())

			By("deleting the pool out from under the claim")
			Expect(apiClient.Delete(ctx, pool)).To(Succeed())

			By("deleting the claim")
			Expect(apiClient.Delete(ctx, &claim)).To(Succeed())
		})

		// Releasing goes by what the IPAddress records - the instance, the views, and the address
		// with its prefix - so a pool that is gone, or whose subnets have been edited since the
		// allocation, does not stand in the way of handing the reservation back.
		It("should still release the address and delete the claim", func() {
			expectReleaseSucceeds()

			_, err := reconcileClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			err = apiClient.Get(ctx, client.ObjectKeyFromObject(&claim), &ipamv1.IPAddressClaim{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the claim to be gone, got %v", err)
			expectNoAddress()
		})

		// Addresses allocated by an earlier version of this provider carry no annotations. Without
		// them, and without the pool, nothing says which Infoblox instance and view hold the
		// reservation. Releasing against a guess would be worse than not releasing at all, so the
		// claim is kept instead - which is recoverable, as the next spec shows.
		It("should refuse to release an address allocated before the annotations existed", func() {
			// Pinned at zero: no guessed release may reach Infoblox.
			infobloxMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			stripAllocationAnnotations()

			_, err := reconcileClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring("cannot determine the Infoblox views")))

			keptClaim := &ipamv1.IPAddressClaim{}
			Expect(apiClient.Get(ctx, client.ObjectKeyFromObject(&claim), keptClaim)).To(Succeed())
			Expect(keptClaim.Finalizers).To(ContainElement(ipamutil.ReleaseAddressFinalizer))

			By("saying so on the claim, so the stuck state is not only visible in the logs")
			Expect(keptClaim.Status.Conditions).To(ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.ReleaseFailedReason)),
				HaveField("Message", ContainSubstring("Restore it to let deletion proceed")),
			)))
		})

		It("should release such an address and delete the claim once the pool is restored", func() {
			expectReleaseSucceeds()
			stripAllocationAnnotations()

			_, err := reconcileClaim(claimName)
			Expect(err).To(HaveOccurred())

			By("restoring the pool")
			createPool()

			_, err = reconcileClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			err = apiClient.Get(ctx, client.ObjectKeyFromObject(&claim), &ipamv1.IPAddressClaim{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the claim to be gone, got %v", err)
			expectNoAddress()
		})

		// Without the pool, its DNS zone is unknown, so the claim's name may not be the host record's name.
		It("should refuse to release when the claim has no cached hostname", func() {
			infobloxMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			Expect(apiClient.Get(ctx, client.ObjectKeyFromObject(&claim), &claim)).To(Succeed())
			delete(claim.Annotations, hostnameAnnotation)
			Expect(apiClient.Update(ctx, &claim)).To(Succeed())

			_, err := reconcileClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring(`InfobloxIPPool "test-pool" is gone; restore the pool or set the annotation`)))
			Expect(getAddress(claimName).Spec.Address).To(Equal("10.0.0.2"))
			Expect(Object(&claim)()).To(And(
				HaveField("ObjectMeta.Finalizers", ContainElement(ipamutil.ReleaseAddressFinalizer)),
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
					HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
					HaveField("Reason", BeEquivalentTo(v1alpha1.ReleaseFailedReason)),
				))),
			))
		})
	})

	// A missing instance or credentials Secret must not be taken for a missing pool.
	When("the pool's Infoblox instance is missing", func() {
		var claim ipamv1.IPAddressClaim

		missingInstance := func() {
			clientErr = fmt.Errorf("failed to fetch instance: %w", apierrors.NewNotFound(
				schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "infobloxinstances"}, instanceName))
		}

		// expectReleaseFailed asserts that the claim is kept with a ReleaseFailed condition naming the instance.
		expectReleaseFailed := func(err error) {
			ExpectWithOffset(1, err).To(MatchError(ContainSubstring(`failed to create Infoblox client for instance "test-instance"`)))
			kept, getErr := Object(&claim)()
			ExpectWithOffset(1, getErr).NotTo(HaveOccurred())
			ExpectWithOffset(1, kept).To(And(
				HaveField("ObjectMeta.Finalizers", ContainElement(ipamutil.ReleaseAddressFinalizer)),
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Reason", BeEquivalentTo(v1alpha1.ReleaseFailedReason)),
					HaveField("Message", And(
						ContainSubstring(`failed to create Infoblox client for instance "test-instance"`),
						Not(ContainSubstring("is gone")),
					)),
				))),
			))
		}

		BeforeEach(func() {
			createPool()
			claim = newClaim(claimName, namespace, v1alpha1.InfobloxIPPoolKind, poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
		})

		It("should report a live claim as not ready instead of ignoring it", func() {
			missingInstance()

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring(`failed to create Infoblox client for instance "test-instance"`)))
			expectNoAddress()
			Expect(Object(&claim)()).To(HaveField("Status.Conditions", ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.ConfigurationInvalidReason)),
			))))
		})

		When("the claim is deleted", func() {
			BeforeEach(func() {
				expectAllocationSucceeds("10.0.0.2")
				infobloxMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				_, err := reconcileAllocatedClaim(claimName)
				Expect(err).NotTo(HaveOccurred())
				Expect(apiClient.Delete(ctx, &claim)).To(Succeed())
				missingInstance()
			})

			It("should keep the claim and report the instance", func() {
				_, err := reconcileClaim(claimName)

				expectReleaseFailed(err)
			})

			It("should report the instance, not a missing pool, for an address without annotations", func() {
				stripAllocationAnnotations()

				_, err := reconcileClaim(claimName)

				expectReleaseFailed(err)
			})
		})
	})

	// The Claim to IPAddress match is done by resource names as is done upstream, not by any status refs or similar.
	When("the claim status does not name the allocated address", func() {
		It("should still release the address and delete the claim", func() {
			expectAllocationSucceeds("10.0.0.2")
			expectReleaseSucceeds()
			createPool()
			claim := newClaim(claimName, namespace, v1alpha1.InfobloxIPPoolKind, poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())

			By("dropping the address reference from the claim status")
			Expect(apiClient.Get(ctx, client.ObjectKeyFromObject(&claim), &claim)).To(Succeed())
			Expect(claim.Status.AddressRef.Name).NotTo(BeEmpty(), "the reconciler should have recorded the address")
			claim.Status.AddressRef = ipamv1.IPAddressReference{}
			Expect(apiClient.Status().Update(ctx, &claim)).To(Succeed())

			By("deleting the claim")
			Expect(apiClient.Delete(ctx, &claim)).To(Succeed())

			_, err = reconcileClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			err = apiClient.Get(ctx, client.ObjectKeyFromObject(&claim), &ipamv1.IPAddressClaim{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the claim to be gone, got %v", err)
			expectNoAddress()
		})
	})

	When("the pool is paused", func() {
		BeforeEach(func() {
			pool := &v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:        poolName,
					Namespace:   namespace,
					Annotations: map[string]string{clusterv1.PausedAnnotation: ""},
				},
				Spec: v1alpha1.InfobloxIPPoolSpec{
					InstanceRef: v1alpha1.InstanceReference{Name: instanceName},
					Subnets:     defaultSubnets,
					NetworkView: "default",
				},
			}
			Expect(apiClient.Create(ctx, pool)).To(Succeed())
			markPoolReady(pool)

			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(apiClient.Delete(ctx, pool))).To(Succeed())
			})
		})

		It("should not create an IPAddress until the pool is unpaused", func() {
			expectAllocationSucceeds("10.0.0.2")
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())
			expectNoAddress()

			By("unpausing the pool")
			pool := &v1alpha1.InfobloxIPPool{}
			Expect(apiClient.Get(ctx, client.ObjectKey{Name: poolName, Namespace: namespace}, pool)).To(Succeed())
			pool.Annotations = nil
			Expect(apiClient.Update(ctx, pool)).To(Succeed())

			_, err = reconcileClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			Expect(getAddress(claimName).Spec.Address).To(Equal("10.0.0.2"))
		})

		It("should prevent deletion of claims", func() {
			// The pool is paused before the claim is first reconciled, so the claim never receives
			// an address. Nothing was reserved, so nothing is released: pinned at zero.
			infobloxMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())

			// What is under test here is the finalizer gate, not the release itself.
			By("noting the claim never got an address, because the pool was paused throughout")
			expectNoAddress()

			By("deleting the claim while the pool is paused")
			Expect(apiClient.Delete(ctx, &claim)).To(Succeed())

			_, err = reconcileClaim(claimName)
			Expect(err).NotTo(HaveOccurred())

			By("keeping the claim alive on its finalizer")
			Expect(Object(&claim)()).To(HaveField("ObjectMeta.Finalizers",
				ContainElement(ipamutil.ReleaseAddressFinalizer)))

			By("unpausing the pool, which lets the deletion proceed")
			pool := &v1alpha1.InfobloxIPPool{}
			Expect(apiClient.Get(ctx, client.ObjectKey{Name: poolName, Namespace: namespace}, pool)).To(Succeed())
			pool.Annotations = nil
			Expect(apiClient.Update(ctx, pool)).To(Succeed())

			_, err = reconcileClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			err = apiClient.Get(ctx, client.ObjectKeyFromObject(&claim), &ipamv1.IPAddressClaim{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the claim to be gone, got %v", err)
		})
	})

	When("an existing IPAddress is missing finalizers and owner references", func() {
		BeforeEach(func() {
			createPool()
		})

		It("should add the owner references and finalizer", func() {
			expectAddressVerified("default", "10.0.0.2", true)
			expected := expectedAddress(claimName, "10.0.0.2", "10.0.0.1")
			Expect(apiClient.Create(ctx, &ipamv1.IPAddress{
				ObjectMeta: metav1.ObjectMeta{Name: claimName, Namespace: namespace},
				Spec:       expected.Spec,
			})).To(Succeed())

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			Expect(getAddress(claimName)).To(EqualObject(&expected, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
		})

		It("should keep an unrelated owner reference", func() {
			expectAddressVerified("default", "10.0.0.2", true)
			unrelatedOwnerRef := metav1.OwnerReference{
				APIVersion: "alpha-dummy",
				Kind:       "dummy-kind",
				Name:       "dummy-name",
				UID:        "abc-dummy-123",
			}
			expected := expectedAddress(claimName, "10.0.0.2", "10.0.0.1", unrelatedOwnerRef)
			Expect(apiClient.Create(ctx, &ipamv1.IPAddress{
				ObjectMeta: metav1.ObjectMeta{
					Name:            claimName,
					Namespace:       namespace,
					OwnerReferences: []metav1.OwnerReference{unrelatedOwnerRef},
				},
				Spec: expected.Spec,
			})).To(Succeed())

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			Expect(getAddress(claimName)).To(EqualObject(&expected, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
			// IgnoreUIDsOnIPAddress covers OwnerReferences[0].UID, which here is the unrelated
			// reference rather than a generated one, so assert it separately.
			By("keeping the UID of the reference it did not create")
			Expect(getAddress(claimName).OwnerReferences[0].UID).To(BeEquivalentTo("abc-dummy-123"))
		})
	})

	// Every reconciliation of a live claim runs EnsureAddress again. For an address that was allocated
	// before, Infoblox is only asked whether it still holds it; a missing reservation must never be
	// replaced, as the IPAddress is immutable and the machine most likely still uses the address.
	When("the claim already has an IPAddress", func() {
		var existing ipamv1.IPAddress

		BeforeEach(func() {
			createPool()
			expected := expectedAddress(claimName, "10.0.0.2", "10.0.0.1")
			existing = ipamv1.IPAddress{
				ObjectMeta: metav1.ObjectMeta{Name: claimName, Namespace: namespace},
				Spec:       expected.Spec,
			}
		})

		createClaim := func() ipamv1.IPAddressClaim {
			Expect(apiClient.Create(ctx, &existing)).To(Succeed())
			claim := newClaim(claimName, namespace, v1alpha1.InfobloxIPPoolKind, poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			return claim
		}

		It("should keep the address and mark the claim ready when Infoblox still holds it", func() {
			expectAddressVerified("default", "10.0.0.2", true)
			claim := createClaim()

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			expected := expectedAddress(claimName, "10.0.0.2", "10.0.0.1")
			Expect(getAddress(claimName)).To(EqualObject(&expected, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
			Expect(Object(&claim)()).To(HaveField("Status.Conditions", ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionTrue)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.AddressAllocatedReason)),
			))))
		})

		It("should not allocate a replacement when Infoblox no longer holds the address", func() {
			expectAddressVerified("default", "10.0.0.2", false)
			claim := createClaim()

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring(`address 10.0.0.2 of IPAddress "test-claim" is not assigned to host "test-claim"`)))
			Expect(getAddress(claimName).Spec.Address).To(Equal("10.0.0.2"))
			Expect(Object(&claim)()).To(HaveField("Status.Conditions", ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.AddressDriftedReason)),
				HaveField("Message", ContainSubstring("not allocating a replacement")),
			))))
		})

		It("should report a failed check without allocating", func() {
			infobloxMock.EXPECT().IsAddressAssigned(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(false, errors.New("infoblox unreachable")).MinTimes(1)
			infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Times(0)
			claim := createClaim()

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring("infoblox unreachable")))
			Expect(getAddress(claimName).Spec.Address).To(Equal("10.0.0.2"))
			Expect(Object(&claim)()).To(HaveField("Status.Conditions", ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.VerificationFailedReason)),
				HaveField("Message", ContainSubstring(`failed to verify address 10.0.0.2 of IPAddress "test-claim" in Infoblox: infoblox unreachable`)),
			))))
		})

		It("should report an invalid address without asking Infoblox", func() {
			infobloxMock.EXPECT().IsAddressAssigned(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			infobloxMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Times(0)
			existing.Spec.Address = "not-an-ip"
			claim := createClaim()

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring(`IPAddress "test-claim" holds an invalid address "not-an-ip"`)))
			Expect(getAddress(claimName).Spec.Address).To(Equal("not-an-ip"))
			Expect(Object(&claim)()).To(HaveField("Status.Conditions", ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.AddressInvalidReason)),
			))))
		})

		It("should check the address where it was recorded to be allocated", func() {
			expectAddressVerified("recorded-view", "10.0.0.2", true)
			existing.Annotations = map[string]string{
				infobloxInstanceAnnotation: "recorded-instance",
				networkViewAnnotation:      "recorded-view",
				dnsViewAnnotation:          "recorded-dns-view",
			}
			createClaim()

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			Expect(requestedInstances).To(ContainElement("recorded-instance"))
			By("keeping the recorded location instead of overwriting it with the pool's")
			Expect(getAddress(claimName).Annotations).To(Equal(existing.Annotations))
		})
	})

	When("the claim is linked to a cluster", func() {
		const clusterName = "test-cluster"

		// createCluster creates the cluster the claim links to.
		createCluster := func(paused bool, annotated bool) {
			cluster := &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: namespace},
				Spec:       clusterv1.ClusterSpec{Paused: ptr.To(paused)},
			}
			if annotated {
				cluster.Annotations = map[string]string{clusterv1.PausedAnnotation: ""}
			}
			Expect(apiClient.Create(ctx, cluster)).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(apiClient.Delete(ctx, cluster))).To(Succeed())
			})
		}

		// createLinkedClaim creates a claim carrying the cluster name label.
		createLinkedClaim := func() ipamv1.IPAddressClaim {
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			claim.Labels = map[string]string{clusterv1.ClusterNameLabel: clusterName}
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			return claim
		}

		BeforeEach(func() {
			createPool()
		})

		It("does not allocate an ipaddress when the cluster has spec.paused", func() {
			createCluster(true, false)
			createLinkedClaim()

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			expectNoAddress()
		})

		It("does not allocate an ipaddress when the cluster has the paused annotation", func() {
			createCluster(false, true)
			createLinkedClaim()

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			expectNoAddress()
		})

		It("allocates an ipaddress once the cluster is unpaused", func() {
			expectAllocationSucceeds("10.0.0.2")
			createCluster(true, false)
			createLinkedClaim()

			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())
			expectNoAddress()

			By("unpausing the cluster")
			cluster := &clusterv1.Cluster{}
			Expect(apiClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: namespace}, cluster)).To(Succeed())
			cluster.Spec.Paused = ptr.To(false)
			Expect(apiClient.Update(ctx, cluster)).To(Succeed())

			_, err = reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			Expect(getAddress(claimName).Spec.Address).To(Equal("10.0.0.2"))
		})

		It("does not allocate an ipaddress when the cluster cannot be retrieved", func() {
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			claim.Labels = map[string]string{clusterv1.ClusterNameLabel: "an-unfindable-cluster"}
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			_, err := reconcileAllocatedClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			expectNoAddress()
		})
	})

	When("a claim is deleted", func() {
		BeforeEach(func() {
			createPool()
		})

		It("should release the address and remove the claim", func() {
			expectAllocationSucceeds("10.0.0.2")
			expectReleaseSucceeds()
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())

			By("deleting the claim")
			Expect(apiClient.Delete(ctx, &claim)).To(Succeed())

			_, err = reconcileClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			err = apiClient.Get(ctx, client.ObjectKeyFromObject(&claim), &ipamv1.IPAddressClaim{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the claim to be gone, got %v", err)
			expectNoAddress()
		})

		It("should keep the claim and its address when Infoblox cannot release it", func() {
			expectAllocationSucceeds("10.0.0.2")
			infobloxMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(errors.New("infoblox unavailable")).MinTimes(1)
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())

			By("deleting the claim")
			Expect(apiClient.Delete(ctx, &claim)).To(Succeed())

			_, err = reconcileClaim(claimName)

			Expect(err).To(MatchError(ContainSubstring(`failed to release address 10.0.0.2 of host "test-claim": infoblox unavailable`)))
			Expect(getAddress(claimName).Spec.Address).To(Equal("10.0.0.2"))
			Expect(Object(&claim)()).To(And(
				HaveField("ObjectMeta.Finalizers", ContainElement(ipamutil.ReleaseAddressFinalizer)),
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
					HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
					HaveField("Reason", BeEquivalentTo(v1alpha1.ReleaseFailedReason)),
					HaveField("Message", ContainSubstring(`failed to release address 10.0.0.2 of host "test-claim": infoblox unavailable`)),
				))),
			))
		})

		// The pool's instance may have changed since the allocation, but the reservation stays where it was made.
		It("should release against the Infoblox instance recorded on the IPAddress", func() {
			expectAllocationSucceeds("10.0.0.2")
			expectReleaseSucceeds()
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			_, err := reconcileAllocatedClaim(claimName)
			Expect(err).NotTo(HaveOccurred())

			By("recording another instance than the pool's on the IPAddress")
			address := getAddress(claimName)
			address.Annotations[infobloxInstanceAnnotation] = "recorded-instance"
			Expect(apiClient.Update(ctx, address)).To(Succeed())

			By("deleting the claim")
			Expect(apiClient.Delete(ctx, &claim)).To(Succeed())
			requestedInstances = nil

			_, err = reconcileClaim(claimName)

			Expect(err).NotTo(HaveOccurred())
			Expect(requestedInstances).To(Equal([]string{"recorded-instance"}),
				"expected only a client for the recorded instance to release against")
		})
	})

	It("returns no requests when listing claims by pool index fails", func() {
		pool := &v1alpha1.InfobloxIPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pool",
				Namespace: "default",
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()
		adapter := &InfobloxProviderAdapter{K8sClient: fakeClient}

		requests := adapter.infobloxIPPoolToIPClaims(context.Background(), pool)

		Expect(requests).To(BeEmpty())
	})
})
