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
	"net/netip"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// This file holds the manager driven half of the integration tests. The sibling files drive
// Reconcile directly, which keeps them simpler, but it leaves the wiring untested:
// whether SetupWithManager succeeds, whether the watch predicates are attached, and whether
// the controllers behave when their client reads through the manager's cache the way it does in production.
//
// The specs here cover exactly that plumbing in for the positive case.
//
// Error branches are not repeated here.

const (
	// managerTimeout bounds how long a spec waits for a controller to act on an object.
	managerTimeout = 20 * time.Second

	// poolDeletionTimeout bounds the wait for a pool to disappear once nothing references it any
	// more. Nothing wakes the pool reconciler when the last claim goes, so this wait is dominated by
	// the requeue cadence rather than by how fast the controller works - hence the extra headroom,
	// which is expressed in terms of PoolDeletionRetry so that changing it cannot turn this into a
	// flake.
	poolDeletionTimeout = managerTimeout + PoolDeletionRetry
	managerPolling      = 50 * time.Millisecond
	// quietPeriod is how long a spec observes that nothing happens when nothing should.
	quietPeriod = 2 * time.Second
)

// stubInfobloxClient answers every Infoblox call successfully. Controllers run concurrently with
// the specs here, so the stub has to be safe to call from any goroutine at any time. A gomock mock
// would be reconfigured per spec, which is exactly the race this avoids.
type stubInfobloxClient struct{}

var _ infoblox.Client = stubInfobloxClient{}

func (stubInfobloxClient) GetOrAllocateAddress(_, _ string, _ netip.Prefix, _, _ string, _ logr.Logger) (netip.Addr, error) {
	return netip.MustParseAddr("10.0.0.2"), nil
}

func (stubInfobloxClient) IsAddressAssigned(_, _ string, _ netip.Addr) (bool, error) {
	return true, nil
}

func (stubInfobloxClient) ReleaseAddress(_, _ string, _ netip.Prefix, _ string, _ logr.Logger) error {
	return nil
}

func (stubInfobloxClient) CheckNetworkViewExists(_ string) (bool, error) { return true, nil }

func (stubInfobloxClient) CheckDNSViewExists(_ string) (bool, error) { return true, nil }

func (stubInfobloxClient) CheckNetworkExists(_ string, _ netip.Prefix) (bool, error) {
	return true, nil
}

func (stubInfobloxClient) GetHostConfig() *infoblox.HostConfig { return &infoblox.HostConfig{} }

// The container is Ordered because the pool and claim lifecycle below is one continuous scenario:
// each step builds on the state the previous one left behind. ContinueOnFailure keeps a break in
// that scenario from hiding the specs that do not belong to it, which sit outside the nested
// container.
var _ = Describe("controllers running under a manager", Ordered, ContinueOnFailure, func() {
	const (
		poolName   = "managed-pool"
		claimName  = "managed-claim"
		secretName = "managed-pool-credentials" //nolint:gosec // G101 matches the identifier, not the value
	)

	var (
		mgrCtx       context.Context
		mgrCancel    context.CancelFunc
		namespace    string
		instanceName string
		poolKey      client.ObjectKey
		claimKey     client.ObjectKey
	)

	BeforeAll(func() {
		mgrCtx, mgrCancel = context.WithCancel(ctx)
		DeferCleanup(mgrCancel)

		namespace = createNamespace()
		instanceName = namespace + "-instance"
		poolKey = client.ObjectKey{Name: poolName, Namespace: namespace}
		claimKey = client.ObjectKey{Name: claimName, Namespace: namespace}

		By("wiring up a manager the same way main does")
		syncPeriod := 100 * time.Millisecond
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: scheme.Scheme,
			Cache: cache.Options{
				SyncPeriod: &syncPeriod,
				// Confine the controllers to this spec's namespace. Without this they would also
				// reconcile the fixtures other spec files leave behind, since envtest namespaces
				// are never torn down. Cluster-scoped objects, InfobloxInstance among them, are
				// unaffected and stay cluster-wide.
				DefaultNamespaces: map[string]cache.Config{namespace: {}},
			},
			// The suite runs no metrics endpoint, and binding one would clash between runs.
			Metrics: metricsserver.Options{BindAddress: "0"},
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(index.SetupIndexes(mgrCtx, mgr.GetFieldIndexer())).To(Succeed())

		stub := stubInfobloxClient{}
		getInfobloxClient := func(_, _ string, _ types.UID, _ string, _ infoblox.Config) (infoblox.Client, error) {
			return stub, nil
		}

		// Every reconciler is registered through the same SetupWithManager the binary calls, so a
		// broken builder configuration fails here rather than in production.
		Expect((&ipamutil.ClaimReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Adapter: &InfobloxProviderAdapter{
				K8sReader:                        mgr.GetAPIReader(),
				OperatorNamespace:                namespace,
				GetInfobloxClientFunc:            getInfobloxClient,
				GetInfobloxClientForInstanceFunc: GetInfobloxClientForInstance,
				NewHostnameResolverFunc:          NewHostnameResolver,
			},
		}).SetupWithManager(mgrCtx, mgr)).To(Succeed())

		Expect((&InfobloxInstanceReconciler{
			Client:                   mgr.GetClient(),
			Scheme:                   mgr.GetScheme(),
			OperatorNamespace:        namespace,
			GetInfobloxClientFunc:    getInfobloxClient,
			DeleteInfobloxClientFunc: func(string) {},
		}).SetupWithManager(mgrCtx, mgr)).To(Succeed())

		Expect((&InfobloxIPPoolReconciler{
			Client:                mgr.GetClient(),
			APIReader:             mgr.GetAPIReader(),
			Scheme:                mgr.GetScheme(),
			OperatorNamespace:     namespace,
			GetInfobloxClientFunc: getInfobloxClient,
		}).SetupWithManager(mgr)).To(Succeed())

		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()
		Expect(mgr.GetCache().WaitForCacheSync(mgrCtx)).To(BeTrue())

		By("creating the Infoblox instance the pool refers to")
		createObj(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
			StringData: map[string]string{"username": "user", "password": "pass"},
		})
		createObj(&v1alpha1.InfobloxInstance{
			ObjectMeta: metav1.ObjectMeta{Name: instanceName},
			Spec: v1alpha1.InfobloxInstanceSpec{
				Host:                 "somehost",
				WAPIVersion:          "1.2.3",
				CredentialsSecretRef: v1alpha1.CredentialsReferece{Name: secretName},
			},
		})
		DeferCleanup(func() {
			instance := &v1alpha1.InfobloxInstance{ObjectMeta: metav1.ObjectMeta{Name: instanceName}}
			Expect(client.IgnoreNotFound(apiClient.Delete(ctx, instance))).To(Succeed())
		})
	})

	// Each spec in this container depends on the state the previous one left behind, so it is
	// Ordered without ContinueOnFailure: once a step breaks, the ones after it can only report the
	// same failure again.
	Describe("the pool and claim lifecycle", Ordered, func() {
		It("marks a pool ready once the controller validated it", func() {
			createObj(&v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: namespace},
				Spec: v1alpha1.InfobloxIPPoolSpec{
					InstanceRef: v1alpha1.InstanceReference{Name: instanceName},
					Subnets:     []v1alpha1.Subnet{{CIDR: "10.0.0.0/24", Gateway: "10.0.0.1"}},
					NetworkView: "test-view",
				},
			})

			Eventually(func(g Gomega) {
				pool := &v1alpha1.InfobloxIPPool{}
				g.Expect(apiClient.Get(ctx, poolKey, pool)).To(Succeed())
				g.Expect(pool.Finalizers).To(ContainElement(ProtectPoolFinalizer))
				g.Expect(pool).To(haveReadyCondition(metav1.ConditionTrue, v1alpha1.ReadyReason))
			}).WithTimeout(managerTimeout).WithPolling(managerPolling).Should(Succeed())
		})

		It("allocates an address for a claim referencing the pool", func() {
			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			createObj(&claim)

			Eventually(func(g Gomega) {
				address := &ipamv1.IPAddress{}
				g.Expect(apiClient.Get(ctx, claimKey, address)).To(Succeed())
				g.Expect(address.Spec.Address).To(Equal("10.0.0.2"))
				g.Expect(address.Spec.PoolRef.Kind).To(Equal("InfobloxIPPool"))
				g.Expect(address.Spec.PoolRef.Name).To(Equal(poolName))
			}).WithTimeout(managerTimeout).WithPolling(managerPolling).Should(Succeed())
		})

		It("blocks deleting the pool while the claim still references it", func() {
			pool := &v1alpha1.InfobloxIPPool{}
			Expect(apiClient.Get(ctx, poolKey, pool)).To(Succeed())
			Expect(apiClient.Delete(ctx, pool)).To(Succeed())

			// The finalizer has to keep the pool alive. Consistently rather than Eventually, because the
			// interesting failure is the pool disappearing a moment later.
			Consistently(func(g Gomega) {
				remaining := &v1alpha1.InfobloxIPPool{}
				g.Expect(apiClient.Get(ctx, poolKey, remaining)).To(Succeed())
				g.Expect(remaining.Finalizers).To(ContainElement(ProtectPoolFinalizer))
			}).WithTimeout(quietPeriod).WithPolling(managerPolling).Should(Succeed())
		})

		It("releases the address and frees the pool when the claim is deleted", func() {
			claim := &ipamv1.IPAddressClaim{}
			Expect(apiClient.Get(ctx, claimKey, claim)).To(Succeed())
			Expect(apiClient.Delete(ctx, claim)).To(Succeed())

			By("removing the address along with the claim")
			Eventually(func(g Gomega) {
				err := apiClient.Get(ctx, claimKey, &ipamv1.IPAddressClaim{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the claim to be gone, got %v", err)

				err = apiClient.Get(ctx, claimKey, &ipamv1.IPAddress{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the address to be gone, got %v", err)
			}).WithTimeout(managerTimeout).WithPolling(managerPolling).Should(Succeed())

			By("letting the pool deletion that was blocked earlier complete")
			Eventually(func(g Gomega) {
				err := apiClient.Get(ctx, poolKey, &v1alpha1.InfobloxIPPool{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the pool to be gone, got %v", err)
			}).WithTimeout(poolDeletionTimeout).WithPolling(managerPolling).Should(Succeed())
		})
	})

	It("ignores claims that reference a pool of another kind", func() {
		// The claim reconciler only watches claims referencing an InfobloxIPPool. Without that
		// predicate attached, this claim would be reconciled and reported as a missing pool.
		foreign := newClaim("foreign-claim", namespace, "InClusterIPPool", "some-other-pool")
		createObj(&foreign)

		foreignKey := client.ObjectKeyFromObject(&foreign)
		Consistently(func(g Gomega) {
			current := &ipamv1.IPAddressClaim{}
			g.Expect(apiClient.Get(ctx, foreignKey, current)).To(Succeed())
			g.Expect(current.Finalizers).To(BeEmpty())
			g.Expect(current.Status.Conditions).To(BeEmpty())

			err := apiClient.Get(ctx, foreignKey, &ipamv1.IPAddress{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected no address, got %v", err)
		}).WithTimeout(quietPeriod).WithPolling(managerPolling).Should(Succeed())
	})

	It("keeps the instance ready while the manager runs", func() {
		Eventually(func(g Gomega) {
			instance := &v1alpha1.InfobloxInstance{}
			g.Expect(apiClient.Get(ctx, client.ObjectKey{Name: instanceName}, instance)).To(Succeed())
			g.Expect(instance.Status.Conditions).To(ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionTrue)),
			)))
		}).WithTimeout(managerTimeout).WithPolling(managerPolling).Should(Succeed())
	})
})
