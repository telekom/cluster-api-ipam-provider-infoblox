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
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox/ibmock"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// In production a reconciler reads through the manager's cache, so the object it acts on can be
// older than what the API server holds. Running the specs against a cache would only expose that
// occasionally, whenever the race happened to occur, which is a flake rather than a test. These
// specs inject the staleness instead, using the interceptor client controller-runtime ships for
// exactly this purpose, so the adverse ordering happens on every run.
//
// staleReads makes the first n reads of the given object return the copy handed in, after which
// reads fall through to the API server. n < 0 keeps serving the stale copy forever, which models a
// cache that never catches up.
func staleReads(stale client.Object, n int) client.Client {
	base, err := client.NewWithWatch(cfg, client.Options{Scheme: scheme.Scheme})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())

	key := client.ObjectKeyFromObject(stale)
	var served atomic.Int64

	return interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if k != key || (n >= 0 && served.Load() >= int64(n)) {
				return c.Get(ctx, k, obj, opts...)
			}

			switch target := obj.(type) {
			case *v1alpha1.InfobloxIPPool:
				pinned, ok := stale.(*v1alpha1.InfobloxIPPool)
				if !ok {
					return c.Get(ctx, k, obj, opts...)
				}
				served.Add(1)
				pinned.DeepCopyInto(target)
				return nil
			case *ipamv1.IPAddressClaim:
				pinned, ok := stale.(*ipamv1.IPAddressClaim)
				if !ok {
					return c.Get(ctx, k, obj, opts...)
				}
				served.Add(1)
				pinned.DeepCopyInto(target)
				return nil
			default:
				return c.Get(ctx, k, obj, opts...)
			}
		},
	})
}

// staleEmptyClaimList makes the first n IPAddressClaim list calls come back empty, modelling an
// informer that has not observed a claim which already exists in the API server. Every other read
// falls through.
func staleEmptyClaimList(n int) client.Client {
	cacheCtx, cacheCancel := context.WithCancel(ctx)
	DeferCleanup(cacheCancel)

	syncPeriod := 100 * time.Millisecond
	indexedCache, err := cache.New(cfg, cache.Options{Scheme: scheme.Scheme, SyncPeriod: &syncPeriod})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, index.SetupIndexes(cacheCtx, indexedCache)).To(Succeed())
	go func() {
		defer GinkgoRecover()
		Expect(indexedCache.Start(cacheCtx)).To(Succeed())
	}()
	ExpectWithOffset(1, indexedCache.WaitForCacheSync(cacheCtx)).To(BeTrue())

	base, err := client.NewWithWatch(cfg, client.Options{
		Scheme: scheme.Scheme,
		Cache: &client.CacheOptions{
			Reader:     indexedCache,
			DisableFor: []client.Object{&v1alpha1.InfobloxIPPool{}},
		},
	})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())

	var served atomic.Int64

	return interceptor.NewClient(base, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*ipamv1.IPAddressClaimList); ok && served.Add(1) <= int64(n) {
				return nil
			}
			return c.List(ctx, list, opts...)
		},
	})
}

var _ = Describe("reconciling from a stale read", func() {
	const (
		poolName   = "stale-pool"
		claimName  = "stale-claim"
		secretName = "stale-pool-credentials" //nolint:gosec // G101 matches the identifier, not the value
	)

	var (
		namespace    string
		instanceName string
		infobloxMock *ibmock.MockClient
		poolKey      client.ObjectKey
		// networkViewExists is what the Infoblox stub reports for the pool's network view. A spec
		// flips it to make the next validation fail. gomock matches expectations in the order they
		// were declared, so a second AnyTimes expectation would never be reached, and the reconciler
		// is driven directly here, so a plain variable needs no synchronisation.
		networkViewExists bool
	)

	BeforeEach(func() {
		namespace = createNamespace()
		instanceName = namespace + "-instance"
		poolKey = client.ObjectKey{Name: poolName, Namespace: namespace}
		networkViewExists = true

		infobloxMock = ibmock.NewMockClient(gomock.NewController(GinkgoT()))
		infobloxMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).AnyTimes()
		infobloxMock.EXPECT().CheckNetworkViewExists(gomock.Any()).
			DoAndReturn(func(string) (bool, error) { return networkViewExists, nil }).AnyTimes()
		infobloxMock.EXPECT().CheckDNSViewExists(gomock.Any()).Return(true, nil).AnyTimes()
		infobloxMock.EXPECT().CheckNetworkExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

		createObj(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
			StringData: map[string]string{"username": "user", "password": "pass"},
		})
		instance := &v1alpha1.InfobloxInstance{
			ObjectMeta: metav1.ObjectMeta{Name: instanceName},
			Spec: v1alpha1.InfobloxInstanceSpec{
				Host:                 "somehost",
				WAPIVersion:          "1.2.3",
				CredentialsSecretRef: v1alpha1.CredentialsReferece{Name: secretName},
			},
		}
		createObj(instance)
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(apiClient.Delete(ctx, instance))).To(Succeed())
		})
	})

	// newPoolReconciler builds a pool reconciler reading through the given client.
	newPoolReconciler := func(c client.Client) *InfobloxIPPoolReconciler {
		return &InfobloxIPPoolReconciler{
			Client:            c,
			APIReader:         apiClient,
			Scheme:            apiClient.Scheme(),
			OperatorNamespace: namespace,
			GetInfobloxClientFunc: func(_, _ string, _ types.UID, _ string, _ infoblox.Config) (infoblox.Client, error) {
				return infobloxMock, nil
			},
		}
	}

	// createReadyPool creates a pool and reconciles it until it is validated and ready.
	createReadyPool := func() *v1alpha1.InfobloxIPPool {
		pool := &v1alpha1.InfobloxIPPool{
			ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: namespace},
			Spec: v1alpha1.InfobloxIPPoolSpec{
				InstanceRef: v1alpha1.InstanceReference{Name: instanceName},
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.0.0.0/24", Gateway: "10.0.0.1"}},
				NetworkView: "test-view",
			},
		}
		ExpectWithOffset(1, apiClient.Create(ctx, pool)).To(Succeed())
		DeferCleanup(func() {
			current := &v1alpha1.InfobloxIPPool{}
			if err := apiClient.Get(ctx, poolKey, current); err == nil {
				current.Finalizers = nil
				Expect(client.IgnoreNotFound(apiClient.Update(ctx, current))).To(Succeed())
				Expect(client.IgnoreNotFound(apiClient.Delete(ctx, current))).To(Succeed())
			}
		})

		reconciler := newPoolReconciler(apiClient)
		request := ctrl.Request{NamespacedName: poolKey}
		_, err := reconciler.Reconcile(ctx, request)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, request)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		reconciled := &v1alpha1.InfobloxIPPool{}
		ExpectWithOffset(1, apiClient.Get(ctx, poolKey, reconciled)).To(Succeed())
		ExpectWithOffset(1, reconciled).To(haveReadyCondition(metav1.ConditionTrue, v1alpha1.ReadyReason))
		return reconciled
	}

	When("the read never catches up", func() {
		It("should surface an error rather than silently dropping the write", func() {
			const concurrentCondition = "ConcurrentWriter"

			stale := createReadyPool()

			current := &v1alpha1.InfobloxIPPool{}
			ExpectWithOffset(1, apiClient.Get(ctx, poolKey, current)).To(Succeed())
			current.Status.Conditions = append(current.Status.Conditions, metav1.Condition{
				Type:               concurrentCondition,
				Status:             metav1.ConditionTrue,
				Reason:             "WrittenByAnotherController",
				Message:            "set while the reconciler was holding an older copy",
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: current.Generation,
			})
			ExpectWithOffset(1, apiClient.Status().Update(ctx, current)).To(Succeed())

			networkViewExists = false

			_, err := newPoolReconciler(staleReads(stale, -1)).Reconcile(ctx, ctrl.Request{NamespacedName: poolKey})

			// CAPI's patch helper treats a conflict on the conditions patch as retryable,
			// re-reads the object and tries again with a bounded backoff, so a client
			// that keeps serving the same stale copy exhausts the retries and
			// what surfaces is the abandoned attempt.
			Expect(err).To(MatchError(ContainSubstring("failed to patch InfobloxIPPool")))
			Expect(apierrors.IsConflict(err)).To(BeFalse(), "the helper swallows the conflict, so this must not be asserted on")
			Expect(wait.Interrupted(err)).To(BeTrue(), "expected the patch to give up after retrying conflicts, got %v", err)
			By("leaving the concurrent change intact")
			updated := &v1alpha1.InfobloxIPPool{}
			Expect(apiClient.Get(ctx, poolKey, updated)).To(Succeed())
			Expect(updated.Status.Conditions).To(ContainElement(HaveField("Type", Equal(concurrentCondition))))
		})
	})

	When("a claim is reconciled again from the copy it had before its address existed", func() {
		It("should not allocate a second address", func() {
			createReadyPool()
			infobloxMock.EXPECT().
				GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(netip.MustParseAddr("10.0.0.2"), nil).AnyTimes()

			claimReconciler := func(c client.Client) *ipamutil.ClaimReconciler {
				return &ipamutil.ClaimReconciler{
					Client: c,
					Scheme: apiClient.Scheme(),
					Adapter: &InfobloxProviderAdapter{
						OperatorNamespace: namespace,
						GetInfobloxClientForInstanceFunc: func(_ context.Context, _ client.Reader, _, _ string, _ infoblox.GetClientFunc) (infoblox.Client, error) {
							return infobloxMock, nil
						},
						NewHostnameResolverFunc: NewHostnameResolver,
					},
				}
			}

			claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())
			claimKey := client.ObjectKeyFromObject(&claim)
			request := ctrl.Request{NamespacedName: claimKey}

			By("capturing the claim as it looks before anything was allocated for it")
			beforeAllocation := &ipamv1.IPAddressClaim{}
			Expect(apiClient.Get(ctx, claimKey, beforeAllocation)).To(Succeed())

			By("allocating an address for it")
			_, err := claimReconciler(apiClient).Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			_, err = claimReconciler(apiClient).Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			addresses := &ipamv1.IPAddressList{}
			Expect(apiClient.List(ctx, addresses, client.InNamespace(namespace))).To(Succeed())
			Expect(addresses.Items).To(HaveLen(1))

			By("reconciling once more from the copy that predates the allocation")
			_, err = claimReconciler(staleReads(beforeAllocation, -1)).Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			Expect(apiClient.List(ctx, addresses, client.InNamespace(namespace))).To(Succeed())
			Expect(addresses.Items).To(HaveLen(1), "a stale read must not produce a second address")
			Expect(addresses.Items[0].Spec.Address).To(Equal("10.0.0.2"))
		})
	})

	When("the informer has not yet observed a claim against a pool being deleted", func() {
		// The gate that protects a pool from being deleted while claims still reference it reads
		// through the cache, so an empty result means "the cache has not told me about any claims".
		// Acting on that is not reversible: the pool disappears, and the reservation the claim holds
		// in Infoblox is left with nothing that knows how to release it. A pool is most likely to be
		// deleted during a teardown that is also creating and destroying claims, which is exactly
		// when the informer is behind.
		It("should not release the pool", func() {
			pool := createReadyPool()

			claim := newClaim(claimName, namespace, v1alpha1.InfobloxIPPoolKind, poolName)
			Expect(apiClient.Create(ctx, &claim)).To(Succeed())

			By("deleting the pool")
			Expect(apiClient.Delete(ctx, pool)).To(Succeed())

			res, err := newPoolReconciler(staleEmptyClaimList(1)).Reconcile(ctx, ctrl.Request{NamespacedName: poolKey})

			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(ctrl.Result{RequeueAfter: PoolDeletionRetry}))

			By("keeping the pool alive on its finalizer")
			kept := &v1alpha1.InfobloxIPPool{}
			Expect(apiClient.Get(ctx, poolKey, kept)).To(Succeed())
			Expect(kept.Finalizers).To(ContainElement(ProtectPoolFinalizer))
		})
	})
})
