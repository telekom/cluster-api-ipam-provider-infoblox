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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox/ibmock"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("InfobloxIPPool controller", func() {
	const (
		poolNamespace = "default"
		poolInstance  = "pool-test-instance"
		poolSecret    = "pool-test-secret"
		operatorNS    = "default"
	)

	// newPool creates a basic InfobloxIPPool for testing.
	newPool := func(name, networkView string) *v1alpha1.InfobloxIPPool {
		return &v1alpha1.InfobloxIPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: poolNamespace,
			},
			Spec: v1alpha1.InfobloxIPPoolSpec{
				InstanceRef: corev1.LocalObjectReference{Name: poolInstance},
				Subnets: []v1alpha1.Subnet{
					{CIDR: "192.168.100.0/24", Gateway: "192.168.100.1"},
				},
				NetworkView: networkView,
			},
		}
	}

	// newIBInstance creates an InfobloxInstance (cluster-scoped) for testing.
	newIBInstance := func() *v1alpha1.InfobloxInstance {
		return &v1alpha1.InfobloxInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name: poolInstance,
			},
			Spec: v1alpha1.InfobloxInstanceSpec{
				Host:        "infoblox.example.com",
				Port:        "443",
				WAPIVersion: "2.5",
				CredentialsSecretRef: corev1.LocalObjectReference{
					Name: poolSecret,
				},
			},
		}
	}

	// newIBSecret creates a credentials Secret for testing.
	newIBSecret := func() *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      poolSecret,
				Namespace: operatorNS,
			},
			StringData: map[string]string{
				"username": "admin",
				"password": "password",
			},
		}
	}

	// resetMock replaces the shared mockInfobloxClient with a fresh mock for this test.
	// Since mockNewInfobloxClientFunc closes over the mockInfobloxClient *variable* (not its
	// value at creation time), reassigning it here causes the reconcilers to receive the new
	// mock on their next call — isolating expectations between tests.
	resetMock := func() {
		mockInfobloxClient = ibmock.NewMockClient(mockCtrl)
	}

	Describe("Deletion without claims — no instance configured", func() {
		It("should allow pool deletion when no claims and no blocking finalizer path", func() {
			pool := newPool("notfound-pool", "default")
			createObj(pool)

			Eventually(Object(&v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pool.Name,
					Namespace: pool.Namespace,
				},
			})).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				HaveField("Status.Conditions", ContainElement(
					HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				)),
			)

			deleteObj(&v1alpha1.InfobloxIPPool{}, pool.Name, pool.Namespace)
			Eventually(Get(pool)).
				WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).ShouldNot(Succeed())
		})
	})

	Describe("Finalizer added", func() {
		var pool *v1alpha1.InfobloxIPPool

		BeforeEach(func() {
			resetMock()
			// Both reconcilers (instance + pool) will call the mock.
			// Allow all calls freely so neither blocks.
			mockInfobloxClient.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{
				DefaultNetworkView: "default",
				DefaultDNSView:     "default",
			}).AnyTimes()
			mockInfobloxClient.EXPECT().CheckNetworkViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckDNSViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckNetworkExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

			instance := newIBInstance()
			secret := newIBSecret()
			createObj(instance)
			createObj(secret)

			pool = newPool("finalizer-pool", "default")
			createObj(pool)
		})

		AfterEach(func() {
			deleteObj(&v1alpha1.InfobloxIPPool{}, "finalizer-pool", poolNamespace)
			deleteObj(&v1alpha1.InfobloxInstance{}, poolInstance, "")
			deleteObj(&corev1.Secret{}, poolSecret, operatorNS)
		})

		It("should add ProtectPoolFinalizer to the pool", func() {
			Eventually(Object(&v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pool.Name,
					Namespace: pool.Namespace,
				},
			})).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				HaveField("Finalizers", ContainElement(ProtectPoolFinalizer)),
			)
		})
	})

	Describe("Pool readiness — authentication failure", func() {
		var pool *v1alpha1.InfobloxIPPool

		BeforeEach(func() {
			resetMock()
			// No InfobloxInstance or Secret — getInfobloxClientForInstance fails immediately
			// without ever calling any mock methods.
			pool = newPool("auth-fail-pool", "default")
			createObj(pool)
		})

		AfterEach(func() {
			deleteObj(&v1alpha1.InfobloxIPPool{}, "auth-fail-pool", poolNamespace)
		})

		It("should set ReadyCondition=False with AuthenticationFailedReason", func() {
			Eventually(Object(&v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pool.Name,
					Namespace: pool.Namespace,
				},
			})).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
					HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
					HaveField("Reason", BeEquivalentTo(v1alpha1.AuthenticationFailedReason)),
				))),
			)
		})
	})

	Describe("Pool readiness — network view not found", func() {
		var pool *v1alpha1.InfobloxIPPool

		BeforeEach(func() {
			resetMock()
			mockInfobloxClient.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{
				DefaultNetworkView: "default",
				DefaultDNSView:     "default",
			}).AnyTimes()
			// Both reconcilers call CheckNetworkViewExists. The instance reconciler calls with
			// "" (empty DefaultNetworkView on the test instance spec). The pool reconciler calls
			// with "default". Return false for all so the pool gets NetworkViewNotFoundReason.
			// The instance reconciler will also fail, but we only assert on the pool condition.
			mockInfobloxClient.EXPECT().CheckNetworkViewExists(gomock.Any()).Return(false, nil).AnyTimes()

			instance := newIBInstance()
			secret := newIBSecret()
			createObj(instance)
			createObj(secret)

			pool = newPool("netview-fail-pool", "default")
			createObj(pool)
		})

		AfterEach(func() {
			deleteObj(&v1alpha1.InfobloxIPPool{}, "netview-fail-pool", poolNamespace)
			deleteObj(&v1alpha1.InfobloxInstance{}, poolInstance, "")
			deleteObj(&corev1.Secret{}, poolSecret, operatorNS)
		})

		It("should set ReadyCondition=False with NetworkViewNotFoundReason", func() {
			Eventually(Object(&v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pool.Name,
					Namespace: pool.Namespace,
				},
			})).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
					HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
					HaveField("Reason", BeEquivalentTo(v1alpha1.NetworkViewNotFoundReason)),
				))),
			)
		})
	})

	Describe("Pool readiness — DNS view not found", func() {
		var pool *v1alpha1.InfobloxIPPool

		BeforeEach(func() {
			resetMock()
			mockInfobloxClient.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{
				DefaultNetworkView: "default",
				DefaultDNSView:     "default",
			}).AnyTimes()
			// Network view passes for both reconcilers.
			mockInfobloxClient.EXPECT().CheckNetworkViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			// DNS view fails — pool reconciler sets DNSViewNotFoundReason.
			// The instance reconciler does NOT call CheckDNSViewExists (instance.Spec.DefaultDNSView is "").
			mockInfobloxClient.EXPECT().CheckDNSViewExists(gomock.Any()).Return(false, nil).AnyTimes()

			instance := newIBInstance()
			secret := newIBSecret()
			createObj(instance)
			createObj(secret)

			pool = newPool("dnsview-fail-pool", "default")
			createObj(pool)
		})

		AfterEach(func() {
			deleteObj(&v1alpha1.InfobloxIPPool{}, "dnsview-fail-pool", poolNamespace)
			deleteObj(&v1alpha1.InfobloxInstance{}, poolInstance, "")
			deleteObj(&corev1.Secret{}, poolSecret, operatorNS)
		})

		It("should set ReadyCondition=False with DNSViewNotFoundReason", func() {
			Eventually(Object(&v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pool.Name,
					Namespace: pool.Namespace,
				},
			})).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
					HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
					HaveField("Reason", BeEquivalentTo(v1alpha1.DNSViewNotFoundReason)),
				))),
			)
		})
	})

	Describe("Pool readiness — network not found", func() {
		var pool *v1alpha1.InfobloxIPPool

		BeforeEach(func() {
			resetMock()
			mockInfobloxClient.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{
				DefaultNetworkView: "default",
				DefaultDNSView:     "default",
			}).AnyTimes()
			mockInfobloxClient.EXPECT().CheckNetworkViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckDNSViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			// Network does not exist — pool reconciler sets NetworkNotFoundReason.
			mockInfobloxClient.EXPECT().CheckNetworkExists(gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()

			instance := newIBInstance()
			secret := newIBSecret()
			createObj(instance)
			createObj(secret)

			pool = newPool("network-fail-pool", "default")
			createObj(pool)
		})

		AfterEach(func() {
			deleteObj(&v1alpha1.InfobloxIPPool{}, "network-fail-pool", poolNamespace)
			deleteObj(&v1alpha1.InfobloxInstance{}, poolInstance, "")
			deleteObj(&corev1.Secret{}, poolSecret, operatorNS)
		})

		It("should set ReadyCondition=False with NetworkNotFoundReason", func() {
			Eventually(Object(&v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pool.Name,
					Namespace: pool.Namespace,
				},
			})).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
					HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
					HaveField("Reason", BeEquivalentTo(v1alpha1.NetworkNotFoundReason)),
				))),
			)
		})
	})

	Describe("Pool readiness — happy path", func() {
		var pool *v1alpha1.InfobloxIPPool

		BeforeEach(func() {
			resetMock()
			mockInfobloxClient.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{
				DefaultNetworkView: "default",
				DefaultDNSView:     "default",
			}).AnyTimes()
			mockInfobloxClient.EXPECT().CheckNetworkViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckDNSViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckNetworkExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

			instance := newIBInstance()
			secret := newIBSecret()
			createObj(instance)
			createObj(secret)

			pool = newPool("happy-pool", "default")
			createObj(pool)
		})

		AfterEach(func() {
			deleteObj(&v1alpha1.InfobloxIPPool{}, "happy-pool", poolNamespace)
			deleteObj(&v1alpha1.InfobloxInstance{}, poolInstance, "")
			deleteObj(&corev1.Secret{}, poolSecret, operatorNS)
		})

		It("should set ReadyCondition=True with ReadyReason", func() {
			Eventually(Object(&v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pool.Name,
					Namespace: pool.Namespace,
				},
			})).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
					HaveField("Status", BeEquivalentTo(metav1.ConditionTrue)),
					HaveField("Reason", BeEquivalentTo(v1alpha1.ReadyReason)),
				))),
			)
		})
	})

	Describe("Network view defaulting", func() {
		var pool *v1alpha1.InfobloxIPPool

		BeforeEach(func() {
			resetMock()
			// GetHostConfig returns a distinctive view name — pool reconciler must copy it
			// into pool.Spec.NetworkView when the pool was created with an empty NetworkView.
			mockInfobloxClient.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{
				DefaultNetworkView: "my-default-view",
				DefaultDNSView:     "default",
			}).AnyTimes()
			// Accept any argument so both the instance reconciler (calls with "")
			// and the pool reconciler (calls with "my-default-view") both pass.
			mockInfobloxClient.EXPECT().CheckNetworkViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckDNSViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckNetworkExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

			instance := newIBInstance()
			secret := newIBSecret()
			createObj(instance)
			createObj(secret)

			// Pool with empty NetworkView — should be defaulted from HostConfig.
			pool = newPool("defaultview-pool", "")
			createObj(pool)
		})

		AfterEach(func() {
			deleteObj(&v1alpha1.InfobloxIPPool{}, "defaultview-pool", poolNamespace)
			deleteObj(&v1alpha1.InfobloxInstance{}, poolInstance, "")
			deleteObj(&corev1.Secret{}, poolSecret, operatorNS)
		})

		It("should set NetworkView from HostConfig.DefaultNetworkView and become Ready", func() {
			Eventually(Object(&v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pool.Name,
					Namespace: pool.Namespace,
				},
			})).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				And(
					HaveField("Spec.NetworkView", Equal("my-default-view")),
					HaveField("Status.Conditions", ContainElement(And(
						HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
						HaveField("Status", BeEquivalentTo(metav1.ConditionTrue)),
						HaveField("Reason", BeEquivalentTo(v1alpha1.ReadyReason)),
					))),
				),
			)
		})
	})

	Describe("Deletion blocked by claims", func() {
		var pool *v1alpha1.InfobloxIPPool
		var claim *ipamv1.IPAddressClaim

		BeforeEach(func() {
			resetMock()
			mockInfobloxClient.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{
				DefaultNetworkView: "default",
				DefaultDNSView:     "default",
			}).AnyTimes()
			mockInfobloxClient.EXPECT().CheckNetworkViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckDNSViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckNetworkExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

			instance := newIBInstance()
			secret := newIBSecret()
			createObj(instance)
			createObj(secret)

			pool = newPool("deletion-blocked-pool", "default")
			createObj(pool)

			// Wait for finalizer to be added before creating the claim.
			Eventually(Object(pool)).
				WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				HaveField("Finalizers", ContainElement(ProtectPoolFinalizer)),
			)

			// Create a claim referencing this pool to block deletion.
			c := newClaim("deletion-blocked-claim", poolNamespace, "InfobloxIPPool", pool.Name)
			claim = &c
			createObj(claim)
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, claim)
			Eventually(func() error {
				fresh := &ipamv1.IPAddressClaim{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), fresh); err != nil {
					return nil
				}
				base := fresh.DeepCopy()
				controllerutil.RemoveFinalizer(fresh, "ipam.cluster.x-k8s.io/ReleaseAddress")
				return k8sClient.Patch(ctx, fresh, client.MergeFrom(base))
			}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())
			Eventually(Get(claim)).WithTimeout(5 * time.Second).ShouldNot(Succeed())

			_ = k8sClient.Delete(ctx, pool)
			Eventually(Get(pool)).WithTimeout(5 * time.Second).ShouldNot(Succeed())
			deleteObj(&v1alpha1.InfobloxInstance{}, poolInstance, "")
			deleteObj(&corev1.Secret{}, poolSecret, operatorNS)
		})

		It("should keep the finalizer while claims exist", func() {
			// Trigger deletion of the pool.
			Expect(k8sClient.Delete(ctx, pool)).To(Succeed())

			// Pool should persist because the ProtectPoolFinalizer is still there.
			Consistently(func() error {
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(pool), pool)
			}).WithTimeout(2 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())
		})
	})

	Describe("Deletion succeeds when no claims", func() {
		var pool *v1alpha1.InfobloxIPPool

		BeforeEach(func() {
			resetMock()
			mockInfobloxClient.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{
				DefaultNetworkView: "default",
				DefaultDNSView:     "default",
			}).AnyTimes()
			mockInfobloxClient.EXPECT().CheckNetworkViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckDNSViewExists(gomock.Any()).Return(true, nil).AnyTimes()
			mockInfobloxClient.EXPECT().CheckNetworkExists(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

			instance := newIBInstance()
			secret := newIBSecret()
			createObj(instance)
			createObj(secret)

			pool = newPool("deletion-ok-pool", "default")
			createObj(pool)

			// Wait for finalizer to be added.
			Eventually(Object(pool)).
				WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				HaveField("Finalizers", ContainElement(ProtectPoolFinalizer)),
			)
		})

		AfterEach(func() {
			poolCopy := &v1alpha1.InfobloxIPPool{}
			poolCopy.SetName("deletion-ok-pool")
			poolCopy.SetNamespace(poolNamespace)
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, poolCopy))).To(Succeed())
			Eventually(Get(poolCopy)).WithTimeout(10 * time.Second).WithPolling(200 * time.Millisecond).ShouldNot(Succeed())
			deleteObj(&v1alpha1.InfobloxInstance{}, poolInstance, "")
			deleteObj(&corev1.Secret{}, poolSecret, operatorNS)
		})

		It("should remove the finalizer and allow deletion when no claims exist", func() {
			Expect(k8sClient.Delete(ctx, pool)).To(Succeed())

			// Pool should be fully gone once the finalizer is removed.
			Eventually(Get(pool)).
				WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).ShouldNot(Succeed())
		})
	})
})
