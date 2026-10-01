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
	"testing"
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
	"k8s.io/client-go/kubernetes/scheme"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// cacheTimeout bounds the wait for the informer cache to observe an object the reconciler lists
// through an index. Only the deletion path needs this.
const (
	cacheTimeout = 10 * time.Second
	cachePolling = 20 * time.Millisecond
)

var _ = Describe("InfobloxIPPoolReconciler", func() {
	const (
		poolName    = "test-pool"
		secretName  = "test-pool-credentials" //nolint:gosec // G101 matches the identifier, not the value
		dnsViewName = "test-dns-view"
	)

	var (
		namespace    string
		instanceName string
		poolMock     *ibmock.MockClient
		reconciler   *InfobloxIPPoolReconciler
		pool         *v1alpha1.InfobloxIPPool
		poolKey      client.ObjectKey
	)

	// reconcilePool runs a single reconciliation for the pool under test.
	reconcilePool := func() (ctrl.Result, error) {
		return reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: poolKey})
	}

	// getPool reads the pool from the API server, the source of truth for assertions.
	getPool := func() *v1alpha1.InfobloxIPPool {
		obj := &v1alpha1.InfobloxIPPool{}
		ExpectWithOffset(1, apiClient.Get(ctx, poolKey, obj)).To(Succeed())
		return obj
	}

	// reconcileValidatedPool brings the pool to the point where the reconciler validates it against
	// Infoblox. The first reconciliation only adds the protection finalizer and returns early.
	reconcileValidatedPool := func() (ctrl.Result, error) {
		By("running the reconciliation that adds the protection finalizer")
		res, err := reconcilePool()
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		ExpectWithOffset(1, res).To(Equal(ctrl.Result{}))
		ExpectWithOffset(1, getPool().Finalizers).To(ContainElement(ProtectPoolFinalizer))

		By("running the reconciliation that validates the pool")
		return reconcilePool()
	}

	// expectPoolGone asserts that the pool is gone on the API server.
	expectPoolGone := func() {
		err := apiClient.Get(ctx, poolKey, &v1alpha1.InfobloxIPPool{})
		ExpectWithOffset(1, apierrors.IsNotFound(err)).To(BeTrue(), "expected the pool to be gone, got %v", err)
	}

	// createPool creates the pool and makes sure it is removed again when the spec ends, dropping a
	// finalizer the reconciler may have left behind.
	createPool := func() {
		Expect(apiClient.Create(ctx, pool)).To(Succeed())
		DeferCleanup(func() {
			remaining := &v1alpha1.InfobloxIPPool{}
			err := apiClient.Get(ctx, poolKey, remaining)
			if apierrors.IsNotFound(err) {
				return
			}
			Expect(err).NotTo(HaveOccurred())

			// Drop the finalizer before deleting. Deleting first would bump the
			// resourceVersion, making the subsequent update fail with a conflict.
			if controllerutil.RemoveFinalizer(remaining, ProtectPoolFinalizer) {
				Expect(apiClient.Update(ctx, remaining)).To(Succeed())
			}
			Expect(client.IgnoreNotFound(apiClient.Delete(ctx, remaining))).To(Succeed())
			expectPoolGone()
		})
	}

	BeforeEach(func() {
		namespace = createNamespace()
		instanceName = namespace + "-instance"
		poolKey = client.ObjectKey{Name: poolName, Namespace: namespace}

		poolMock = ibmock.NewMockClient(gomock.NewController(GinkgoT()))
		reconciler = &InfobloxIPPoolReconciler{
			Client:            apiClient,
			APIReader:         apiClient,
			Scheme:            apiClient.Scheme(),
			OperatorNamespace: namespace,
			GetInfobloxClientFunc: func(_, _ string, _ types.UID, _ string, _ infoblox.Config) (infoblox.Client, error) {
				return poolMock, nil
			},
		}

		By("creating the Infoblox instance the pool refers to")
		Expect(apiClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
			StringData: map[string]string{"username": "user", "password": "pass"},
		})).To(Succeed())
		// InfobloxInstance is cluster scoped, so it needs a name unique to the spec.
		instance := &v1alpha1.InfobloxInstance{
			ObjectMeta: metav1.ObjectMeta{Name: instanceName},
			Spec: v1alpha1.InfobloxInstanceSpec{
				Host:                 "somehost",
				WAPIVersion:          "1.2.3",
				CredentialsSecretRef: v1alpha1.CredentialsReferece{Name: secretName},
			},
		}
		Expect(apiClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(apiClient.Delete(ctx, instance))).To(Succeed())
		})

		// Stub pool. Specs may adjust its spec before creating it.
		pool = &v1alpha1.InfobloxIPPool{
			ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: namespace},
			Spec: v1alpha1.InfobloxIPPoolSpec{
				InstanceRef: v1alpha1.InstanceReference{Name: instanceName},
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.0.0.0/24", Gateway: "10.0.0.1"}},
				NetworkView: "test-view",
			},
		}
	})

	When("the pool does not exist", func() {
		It("should not return an error", func() {
			poolKey = client.ObjectKey{Name: "does-not-exist", Namespace: namespace}

			res, err := reconcilePool()

			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(ctrl.Result{}))
		})
	})

	When("a pool is created", func() {
		BeforeEach(func() {
			createPool()
		})

		It("should add the protection finalizer before doing anything else", func() {

			poolMock.EXPECT().GetHostConfig().Times(0)
			poolMock.EXPECT().CheckNetworkViewExists(gomock.Any()).Times(0)
			poolMock.EXPECT().CheckDNSViewExists(gomock.Any()).Times(0)
			poolMock.EXPECT().CheckNetworkExists(gomock.Any(), gomock.Any()).Times(0)

			res, err := reconcilePool()

			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(ctrl.Result{}))
			Expect(getPool().Finalizers).To(ContainElement(ProtectPoolFinalizer))
			By("leaving the status untouched until the pool has been validated")
			Expect(getPool().Status.Conditions).To(BeEmpty())
		})

		It("should set the pool to ready", func() {

			poolMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).Times(1)
			poolMock.EXPECT().CheckNetworkViewExists("test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckDNSViewExists("default.test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckNetworkExists("test-view", netip.MustParsePrefix("10.0.0.0/24")).Return(true, nil).Times(1)

			res, err := reconcileValidatedPool()

			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(ctrl.Result{}))
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionTrue, v1alpha1.ReadyReason))
		})

		It("should keep the pool ready across repeated reconciliations", func() {
			// Three validating reconciliations, each revalidating the pool from scratch.
			poolMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).Times(3)
			poolMock.EXPECT().CheckNetworkViewExists("test-view").Return(true, nil).Times(3)
			poolMock.EXPECT().CheckDNSViewExists("default.test-view").Return(true, nil).Times(3)
			poolMock.EXPECT().CheckNetworkExists("test-view", netip.MustParsePrefix("10.0.0.0/24")).Return(true, nil).Times(3)

			_, err := reconcileValidatedPool()
			Expect(err).NotTo(HaveOccurred())

			By("reconciling twice more without any further changes")
			for range 2 {
				res, err := reconcilePool()
				Expect(err).NotTo(HaveOccurred())
				Expect(res).To(Equal(ctrl.Result{}))
			}
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionTrue, v1alpha1.ReadyReason))
		})
	})

	When("the pool does not specify a network view", func() {
		It("should default to the network view of the instance", func() {
			pool.Spec.NetworkView = ""
			// Twice: once to default the network view, once to derive the DNS view from it.
			poolMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{DefaultNetworkView: "instance-view"}).Times(2)
			poolMock.EXPECT().CheckNetworkViewExists("instance-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckDNSViewExists("default.instance-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckNetworkExists("instance-view", netip.MustParsePrefix("10.0.0.0/24")).Return(true, nil).Times(1)
			createPool()

			_, err := reconcileValidatedPool()

			Expect(err).NotTo(HaveOccurred())
			Expect(getPool().Spec.NetworkView).To(Equal("instance-view"))
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionTrue, v1alpha1.ReadyReason))
		})
	})

	When("the pool specifies a DNS view", func() {
		It("should validate that DNS view instead of the derived one", func() {
			pool.Spec.DNSView = dnsViewName
			poolMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).Times(1)
			poolMock.EXPECT().CheckNetworkViewExists("test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckDNSViewExists(dnsViewName).Return(true, nil).Times(1)
			poolMock.EXPECT().CheckNetworkExists("test-view", netip.MustParsePrefix("10.0.0.0/24")).Return(true, nil).Times(1)
			createPool()

			_, err := reconcileValidatedPool()

			Expect(err).NotTo(HaveOccurred())
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionTrue, v1alpha1.ReadyReason))
		})
	})

	When("the infoblox client cannot be created", func() {
		It("should set the pool to not ready and return an error", func() {
			pool.Spec.InstanceRef.Name = "unknown-instance"
			createPool()

			_, err := reconcileValidatedPool()

			Expect(err).To(HaveOccurred())
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionFalse, v1alpha1.ConfigurationInvalidReason))
		})
	})

	When("the network view does not exist", func() {
		It("should set the pool to not ready", func() {
			// The reconciliation stops at the network view, before the DNS view is derived.
			poolMock.EXPECT().GetHostConfig().Times(0)
			poolMock.EXPECT().CheckNetworkViewExists("test-view").Return(false, nil).Times(1)
			createPool()

			_, err := reconcileValidatedPool()

			Expect(err).NotTo(HaveOccurred())
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionFalse, v1alpha1.NetworkViewNotFoundReason))
		})
	})

	When("the network view cannot be looked up", func() {
		It("should set the pool to not ready and return an error", func() {
			poolMock.EXPECT().GetHostConfig().Times(0)
			poolMock.EXPECT().CheckNetworkViewExists("test-view").Return(false, wapiRejection).Times(1)
			createPool()

			_, err := reconcileValidatedPool()

			// A failed lookup says nothing about whether the view exists, so it must not be
			// reported as NetworkViewNotFound, and it has to be retried.
			Expect(err).To(MatchError(ContainSubstring("infoblox said no")))
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionFalse, v1alpha1.InfobloxRequestFailedReason))
			Expect(getPool().Status.Conditions[0].Message).To(ContainSubstring(`could not check network view "test-view"`))
		})
	})

	When("the DNS view cannot be looked up", func() {
		It("should set the pool to not ready and return an error", func() {
			pool.Spec.DNSView = dnsViewName
			poolMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).Times(1)
			poolMock.EXPECT().CheckNetworkViewExists("test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckDNSViewExists(dnsViewName).Return(false, wapiRejection).Times(1)
			poolMock.EXPECT().CheckNetworkExists(gomock.Any(), gomock.Any()).Times(0)
			createPool()

			_, err := reconcileValidatedPool()

			Expect(err).To(MatchError(ContainSubstring("infoblox said no")))
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionFalse, v1alpha1.InfobloxRequestFailedReason))
		})
	})

	When("a network of the pool cannot be looked up", func() {
		It("should set the pool to not ready and return an error", func() {
			poolMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).Times(1)
			poolMock.EXPECT().CheckNetworkViewExists("test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckDNSViewExists("default.test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckNetworkExists("test-view", netip.MustParsePrefix("10.0.0.0/24")).
				Return(false, wapiRejection).Times(1)
			createPool()

			_, err := reconcileValidatedPool()

			Expect(err).To(MatchError(ContainSubstring("infoblox said no")))
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionFalse, v1alpha1.InfobloxRequestFailedReason))
		})
	})

	When("the DNS view does not exist", func() {
		It("should set the pool to not ready", func() {
			pool.Spec.DNSView = dnsViewName
			// The reconciliation stops at the DNS view, before any subnet is checked.
			poolMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).Times(1)
			poolMock.EXPECT().CheckNetworkViewExists("test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckDNSViewExists(dnsViewName).Return(false, nil).Times(1)
			poolMock.EXPECT().CheckNetworkExists(gomock.Any(), gomock.Any()).Times(0)
			createPool()

			_, err := reconcileValidatedPool()

			Expect(err).NotTo(HaveOccurred())
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionFalse, v1alpha1.DNSViewNotFoundReason))
		})
	})

	When("a network of the pool does not exist", func() {
		It("should set the pool to not ready", func() {
			poolMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).Times(1)
			poolMock.EXPECT().CheckNetworkViewExists("test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckDNSViewExists("default.test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckNetworkExists("test-view", netip.MustParsePrefix("10.0.0.0/24")).Return(false, nil).Times(1)
			createPool()

			_, err := reconcileValidatedPool()

			Expect(err).NotTo(HaveOccurred())
			Expect(getPool()).To(haveReadyCondition(metav1.ConditionFalse, v1alpha1.NetworkNotFoundReason))
		})
	})

	When("a pool is deleted", func() {

		var indexedClient client.Client
		buildIndexedClient := func() client.Client {
			cacheCtx, cacheCancel := context.WithCancel(ctx)
			DeferCleanup(cacheCancel)

			syncPeriod := 100 * time.Millisecond
			indexedCache, err := cache.New(cfg, cache.Options{Scheme: scheme.Scheme, SyncPeriod: &syncPeriod})
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			// The same registration production uses, so the two cannot drift apart.
			ExpectWithOffset(1, index.SetupIndexes(cacheCtx, indexedCache)).To(Succeed())

			go func() {
				defer GinkgoRecover()
				Expect(indexedCache.Start(cacheCtx)).To(Succeed())
			}()
			ExpectWithOffset(1, indexedCache.WaitForCacheSync(cacheCtx)).To(BeTrue())

			indexedClient, err := client.New(cfg, client.Options{
				Scheme: scheme.Scheme,
				Cache: &client.CacheOptions{
					Reader:     indexedCache,
					DisableFor: []client.Object{&v1alpha1.InfobloxIPPool{}},
				},
			})
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			return indexedClient
		}

		BeforeEach(func() {
			// replace the reconcilers client with a fresh, cached and indexed client.
			// this is required for deletion tests because those filter objects using
			// a field index list request filter to find pending claims.
			indexedClient = buildIndexedClient()
			reconciler.Client = indexedClient

			// Only the single validating reconciliation below reaches Infoblox. The deletion
			// reconciliations the specs drive must not.
			poolMock.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).Times(1)
			poolMock.EXPECT().CheckNetworkViewExists("test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckDNSViewExists("default.test-view").Return(true, nil).Times(1)
			poolMock.EXPECT().CheckNetworkExists("test-view", netip.MustParsePrefix("10.0.0.0/24")).Return(true, nil).Times(1)

			createPool()
			_, err := reconcileValidatedPool()
			Expect(err).NotTo(HaveOccurred())

			By("deleting the pool")
			Expect(apiClient.Delete(ctx, pool)).To(Succeed())
		})

		When("no claim references the pool", func() {
			It("should remove the finalizer and allow deletion", func() {
				res, err := reconcilePool()

				Expect(err).NotTo(HaveOccurred())
				Expect(res).To(Equal(ctrl.Result{}))
				expectPoolGone()
			})
		})

		When("a claim still references the pool", func() {
			const claimName = "test-claim"
			var claim ipamv1.IPAddressClaim

			BeforeEach(func() {
				By("creating a claim referencing the pool and waiting for the cache to observe it")
				claim = newClaim(claimName, namespace, "InfobloxIPPool", poolName)
				Expect(apiClient.Create(ctx, &claim)).To(Succeed())
				Eventually(func() error {
					return indexedClient.Get(ctx, client.ObjectKeyFromObject(&claim), &ipamv1.IPAddressClaim{})
				}).WithTimeout(cacheTimeout).WithPolling(cachePolling).Should(Succeed())

				DeferCleanup(func() {
					Expect(client.IgnoreNotFound(apiClient.Delete(ctx, &claim))).To(Succeed())
				})
			})

			It("should block deletion, say why, and ask to be called again", func() {
				// Waiting for a claim to drain is a normal state during a teardown. Reporting it as
				// an error would log it at error level and put the pool on an exponential backoff,
				// checking it progressively less often exactly while it is being torn down.
				res, err := reconcilePool()

				Expect(err).NotTo(HaveOccurred())
				Expect(res).To(Equal(ctrl.Result{RequeueAfter: PoolDeletionRetry}))
				Expect(getPool().Finalizers).To(ContainElement(ProtectPoolFinalizer))

				By("saying so on the pool, so the reason is not only visible in the logs")
				Expect(getPool()).To(haveReadyCondition(metav1.ConditionFalse, v1alpha1.ClaimsPendingDeletionReason))
				Expect(getPool().Status.Conditions).To(ContainElement(
					HaveField("Message", ContainSubstring("waiting for 1 IPAddressClaim(s)"))))
			})

			It("should keep blocking deletion for as long as the claim exists", func() {
				Consistently(func() error {
					_, err := reconcilePool()
					return err
				}).WithTimeout(cacheTimeout).WithPolling(cachePolling).Should(Succeed())

				Expect(getPool().Finalizers).To(ContainElement(ProtectPoolFinalizer))
			})

			It("should remove the finalizer once the claim is gone", func() {
				_, err := reconcilePool()
				Expect(err).NotTo(HaveOccurred())

				By("deleting the claim and waiting for the cache to observe that")
				Expect(apiClient.Delete(ctx, &claim)).To(Succeed())
				Eventually(func() bool {
					err := indexedClient.Get(ctx, client.ObjectKeyFromObject(&claim), &ipamv1.IPAddressClaim{})
					return apierrors.IsNotFound(err)
				}).WithTimeout(cacheTimeout).WithPolling(cachePolling).Should(BeTrue())

				res, err := reconcilePool()

				Expect(err).NotTo(HaveOccurred())
				Expect(res).To(Equal(ctrl.Result{}))
				expectPoolGone()
			})
		})
	})
})

func TestDetermineDNSView(t *testing.T) {
	tests := []struct {
		name                   string
		poolDNSView            string
		instanceDefaultDNSView string
		networkView            string
		want                   string
	}{
		{name: "pool dns view takes precedence", poolDNSView: "pool", instanceDefaultDNSView: "instance", networkView: "network", want: "pool"},
		{name: "instance default is used if pool is empty", instanceDefaultDNSView: "instance", networkView: "network", want: "instance"},
		{name: "derived from network view", networkView: "network", want: "default.network"},
		{name: "default network view", networkView: "default", want: "default"},
		{name: "empty network view", want: "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(determineDNSView(tt.poolDNSView, tt.instanceDefaultDNSView, tt.networkView)).To(Equal(tt.want))
		})
	}
}
