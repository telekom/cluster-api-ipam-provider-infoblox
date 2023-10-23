/*
Copyright 2023 The Kubernetes Authors.

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

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1alpha1"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox/ibmock"
)

var IgnoreUIDsOnIPAddress = IgnorePaths{
	"TypeMeta",
	"ObjectMeta.OwnerReferences[0].UID",
	"ObjectMeta.OwnerReferences[1].UID",
	"ObjectMeta.OwnerReferences[2].UID",
	"Spec.Claim.UID",
	"Spec.Pool.UID",
}

const instanceName = "test-instance"

var _ = Describe("IPAddressClaimReconciler", func() {
	var (
		namespace string
		// namespace2 string
		// instance *v1alpha1.InfobloxInstance
	)
	BeforeEach(func() {
		namespace = createNamespace()
		// namespace2 = createNamespace()
	})

	Context("When a new IPAddressClaim is created", func() {
		When("the referenced pool is an unrecognized kind", func() {
			const poolName = "unknown-pool"

			BeforeEach(func() {
				pool := v1alpha1.InfobloxIPPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      poolName,
						Namespace: namespace,
					},
					Spec: v1alpha1.InfobloxIPPoolSpec{
						InstanceRef: corev1.LocalObjectReference{},
						Subnet:      "10.0.0.0/24",
						NetworkView: "default",
						DNSZone:     "",
					},
				}

				Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
				Eventually(Get(&pool)).Should(Succeed())
			})

			AfterEach(func() {
				deleteClaim("unknown-pool-test", namespace)
				deleteNamespacedPool(poolName, namespace)
			})

			It("should ignore the claim", func() {
				claim := newClaim("unknown-pool-test", namespace, "unknownKind", poolName)
				Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

				addresses := ipamv1.IPAddressList{}
				Consistently(ObjectList(&addresses)).
					WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(0)))
			})
		})

		When("the referenced namespaced pool exists", func() {
			const poolName = "test-pool"
			const claimName = "test-claim"

			var expectedIPAddress ipamv1.IPAddress

			BeforeEach(func() {
				localInfobloxClientMock = ibmock.NewMockClient(mockCtrl)
				getInfobloxClientForInstanceFunc = mockGetInfobloxClientForInstance
				pool := v1alpha1.InfobloxIPPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      poolName,
						Namespace: namespace,
					},
					Spec: v1alpha1.InfobloxIPPoolSpec{
						InstanceRef: corev1.LocalObjectReference{Name: instanceName},
						Subnet:      "10.0.0.0/24",
						NetworkView: "default",
						DNSZone:     "",
					},
				}
				Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
			})

			AfterEach(func() {
				deleteClaim(claimName, namespace)
				deleteNamespacedPool(poolName, namespace)
				getInfobloxClientForInstanceFunc = getInfobloxClientForInstance
			})

			It("should allocate an Address from the Pool", func() {
				addr, err := netip.ParseAddr("10.0.0.1")
				Expect(err).NotTo(HaveOccurred())
				localInfobloxClientMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(addr, nil).AnyTimes()
				localInfobloxClientMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

				claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
				expectedIPAddress = ipamv1.IPAddress{
					ObjectMeta: metav1.ObjectMeta{
						Name:       claimName,
						Namespace:  namespace,
						Finalizers: []string{ipamutil.ProtectAddressFinalizer},
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
								BlockOwnerDeletion: pointer.Bool(true),
								Controller:         pointer.Bool(true),
								Kind:               "IPAddressClaim",
								Name:               claimName,
							},
							{
								APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
								BlockOwnerDeletion: pointer.Bool(true),
								Controller:         pointer.Bool(false),
								Kind:               "InfobloxIPPool",
								Name:               poolName,
							},
						},
					},
					Spec: ipamv1.IPAddressSpec{
						ClaimRef: corev1.LocalObjectReference{
							Name: claimName,
						},
						PoolRef: corev1.TypedLocalObjectReference{
							APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
							Kind:     "InfobloxIPPool",
							Name:     poolName,
						},
						Address: "10.0.0.1",
						Prefix:  24,
						Gateway: "",
					},
				}

				Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

				Eventually(findAddress(claimName, namespace)).
					WithTimeout(1 * time.Second).WithPolling(100 * time.Millisecond).Should(
					EqualObject(&expectedIPAddress, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress),
				)

			})
		})

		When("the referenced namespaced pool does not exists", func() {
			const wrongPoolName = "wrong-test-pool"
			const poolName = "test-pool"

			BeforeEach(func() {
				pool := v1alpha1.InfobloxIPPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      poolName,
						Namespace: namespace,
					},
					Spec: v1alpha1.InfobloxIPPoolSpec{
						InstanceRef: corev1.LocalObjectReference{Name: instanceName},
						Subnet:      "10.0.0.0/24",
						NetworkView: "default",
						DNSZone:     "",
					},
				}
				Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
				Eventually(Get(&pool)).Should(Succeed())
			})

			AfterEach(func() {
				deleteClaim("test", namespace)
				deleteNamespacedPool(poolName, namespace)
			})

			It("should not allocate an Address from the Pool", func() {
				claim := newClaim("test", namespace, "InClusterIPPool", wrongPoolName)
				Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

				addresses := ipamv1.IPAddressList{}
				Consistently(ObjectList(&addresses)).
					WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(0)))
			})
		})

		// When("the referenced namespaced pool has addresses that overlap with reserved addresses", func() {
		// 	const poolName = "test-pool"

		// 	BeforeEach(func() {
		// 		pool := v1alpha2.InClusterIPPool{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      poolName,
		// 				Namespace: namespace,
		// 			},
		// 			Spec: v1alpha2.InClusterIPPoolSpec{
		// 				Prefix:  24,
		// 				Gateway: "10.0.1.1",
		// 				Addresses: []string{
		// 					"10.0.1.0", // reserved network addr
		// 					"10.0.1.1", // reserved gateway addr
		// 					"10.0.1.2",
		// 					"10.0.1.255", // rerved broadcast addr
		// 				},
		// 			},
		// 		}
		// 		Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
		// 		Eventually(Get(&pool)).Should(Succeed())
		// 	})

		// 	AfterEach(func() {
		// 		deleteClaim("test", namespace)
		// 		deleteClaim("test2", namespace)
		// 		deleteNamespacedPool(poolName, namespace)
		// 	})

		// 	It("does no allocate the reserved addresses", func() {
		// 		claim := newClaim("test", namespace, "InClusterIPPool", poolName)
		// 		Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

		// 		expectedIPAddress := ipamv1.IPAddress{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:       "test",
		// 				Namespace:  namespace,
		// 				Finalizers: []string{ipamutil.ProtectAddressFinalizer},
		// 				OwnerReferences: []metav1.OwnerReference{
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(true),
		// 						Kind:               "IPAddressClaim",
		// 						Name:               "test",
		// 					},
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha2",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(false),
		// 						Kind:               "InClusterIPPool",
		// 						Name:               poolName,
		// 					},
		// 				},
		// 			},
		// 			Spec: ipamv1.IPAddressSpec{
		// 				ClaimRef: corev1.LocalObjectReference{
		// 					Name: "test",
		// 				},
		// 				PoolRef: corev1.TypedLocalObjectReference{
		// 					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
		// 					Kind:     "InClusterIPPool",
		// 					Name:     poolName,
		// 				},
		// 				Address: "10.0.1.2",
		// 				Prefix:  24,
		// 				Gateway: "10.0.1.1",
		// 			},
		// 		}

		// 		Eventually(findAddress("test", namespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			EqualObject(&expectedIPAddress, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress),
		// 		)

		// 		claim2 := newClaim("test2", namespace, "InClusterIPPool", poolName)
		// 		Expect(k8sClient.Create(context.Background(), &claim2)).To(Succeed())

		// 		// verify none of the reserved addresses are allocated
		// 		addresses := ipamv1.IPAddressList{}
		// 		Consistently(ObjectList(&addresses)).
		// 			WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			HaveField("Items", HaveLen(1)))

		// 	})
		// })

		// When("the referenced namespaced pool has out of order Addresses", func() {
		// 	const poolName = "test-pool"

		// 	BeforeEach(func() {
		// 		pool := v1alpha2.InClusterIPPool{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      poolName,
		// 				Namespace: namespace,
		// 			},
		// 			Spec: v1alpha2.InClusterIPPoolSpec{
		// 				Prefix:  24,
		// 				Gateway: "10.0.1.1",
		// 				Addresses: []string{
		// 					"10.0.1.3",
		// 					"10.0.1.2",
		// 				},
		// 			},
		// 		}
		// 		Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
		// 		Eventually(Get(&pool)).Should(Succeed())
		// 	})

		// 	AfterEach(func() {
		// 		deleteClaim("test", namespace)
		// 		deleteNamespacedPool(poolName, namespace)
		// 	})

		// 	It("should allocate the lowest available Address from the Pool, regardless of Addresses order", func() {
		// 		claim := newClaim("test", namespace, "InClusterIPPool", poolName)
		// 		Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

		// 		Eventually(findAddress("test", namespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			HaveField("Spec.Address", "10.0.1.2"),
		// 		)
		// 	})
		// })

		// When("the referenced namespaced pool does not contain a gateway", func() {
		// 	const poolName = "test-pool"

		// 	BeforeEach(func() {
		// 		pool := v1alpha2.InClusterIPPool{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      poolName,
		// 				Namespace: namespace,
		// 			},
		// 			Spec: v1alpha2.InClusterIPPoolSpec{
		// 				Addresses: []string{"10.0.0.1-10.0.0.254"},
		// 				Prefix:    24,
		// 			},
		// 		}
		// 		Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
		// 		Eventually(Get(&pool)).Should(Succeed())
		// 	})

		// 	AfterEach(func() {
		// 		deleteClaim("test", namespace)
		// 		deleteNamespacedPool(poolName, namespace)
		// 	})

		// 	It("should allocate an Address from the Pool", func() {
		// 		claim := newClaim("test", namespace, "InClusterIPPool", poolName)
		// 		Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

		// 		expectedIPAddress := ipamv1.IPAddress{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:       "test",
		// 				Namespace:  namespace,
		// 				Finalizers: []string{ipamutil.ProtectAddressFinalizer},
		// 				OwnerReferences: []metav1.OwnerReference{
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(true),
		// 						Kind:               "IPAddressClaim",
		// 						Name:               "test",
		// 					},
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha2",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(false),
		// 						Kind:               "InClusterIPPool",
		// 						Name:               poolName,
		// 					},
		// 				},
		// 			},
		// 			Spec: ipamv1.IPAddressSpec{
		// 				ClaimRef: corev1.LocalObjectReference{
		// 					Name: "test",
		// 				},
		// 				PoolRef: corev1.TypedLocalObjectReference{
		// 					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
		// 					Kind:     "InClusterIPPool",
		// 					Name:     poolName,
		// 				},
		// 				Address: "10.0.0.1",
		// 				Prefix:  24,
		// 			},
		// 		}

		// 		Eventually(findAddress("test", namespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			EqualObject(&expectedIPAddress, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress),
		// 		)
		// 	})
		// })

		// When("the referenced global pool exists", func() {
		// 	const poolName = "global-pool"
		// 	var secondNamespace string
		// 	BeforeEach(func() {
		// 		pool := v1alpha2.GlobalInClusterIPPool{
		// 			ObjectMeta: metav1.ObjectMeta{ // global pool, no namespace
		// 				Name: poolName,
		// 			},
		// 			Spec: v1alpha2.InClusterIPPoolSpec{
		// 				Addresses: []string{"10.0.0.2-10.0.0.254"},
		// 				Prefix:    24,
		// 				Gateway:   "10.0.0.1",
		// 			},
		// 		}
		// 		Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
		// 		Eventually(Get(&pool)).Should(Succeed())
		// 		secondNamespace = createNamespace()
		// 	})

		// 	AfterEach(func() {
		// 		deleteClaim("test", namespace)
		// 		deleteClaim("test-second-namespace", secondNamespace)
		// 		deleteClusterScopedPool(poolName)
		// 	})

		// 	It("should allocate an Address from the Pool, no matter the claim's namespace", func() {
		// 		claim := newClaim("test", namespace, "GlobalInClusterIPPool", poolName)
		// 		Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

		// 		claimFromSecondNamespace := newClaim("test-second-namespace", secondNamespace, "GlobalInClusterIPPool", poolName)
		// 		Expect(k8sClient.Create(context.Background(), &claimFromSecondNamespace)).To(Succeed())

		// 		expectedIPAddress := ipamv1.IPAddress{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:       "test",
		// 				Namespace:  namespace,
		// 				Finalizers: []string{ipamutil.ProtectAddressFinalizer},
		// 				OwnerReferences: []metav1.OwnerReference{
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(true),
		// 						Kind:               "IPAddressClaim",
		// 						Name:               "test",
		// 					},
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha2",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(false),
		// 						Kind:               "GlobalInClusterIPPool",
		// 						Name:               poolName,
		// 					},
		// 				},
		// 			},
		// 			Spec: ipamv1.IPAddressSpec{
		// 				ClaimRef: corev1.LocalObjectReference{
		// 					Name: "test",
		// 				},
		// 				PoolRef: corev1.TypedLocalObjectReference{
		// 					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
		// 					Kind:     "GlobalInClusterIPPool",
		// 					Name:     poolName,
		// 				},
		// 				Address: "10.0.0.2",
		// 				Prefix:  24,
		// 				Gateway: "10.0.0.1",
		// 			},
		// 		}

		// 		expectedIPAddressInSecondNamespace := ipamv1.IPAddress{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:       "test-second-namespace",
		// 				Namespace:  secondNamespace,
		// 				Finalizers: []string{ipamutil.ProtectAddressFinalizer},
		// 				OwnerReferences: []metav1.OwnerReference{
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(true),
		// 						Kind:               "IPAddressClaim",
		// 						Name:               "test-second-namespace",
		// 					},
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha2",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(false),
		// 						Kind:               "GlobalInClusterIPPool",
		// 						Name:               poolName,
		// 					},
		// 				},
		// 			},
		// 			Spec: ipamv1.IPAddressSpec{
		// 				ClaimRef: corev1.LocalObjectReference{
		// 					Name: "test-second-namespace",
		// 				},
		// 				PoolRef: corev1.TypedLocalObjectReference{
		// 					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
		// 					Kind:     "GlobalInClusterIPPool",
		// 					Name:     poolName,
		// 				},
		// 				Address: "10.0.0.3",
		// 				Prefix:  24,
		// 				Gateway: "10.0.0.1",
		// 			},
		// 		}

		// 		Eventually(findAddress("test", namespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			EqualObject(&expectedIPAddress, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress),
		// 		)

		// 		Eventually(findAddress("test-second-namespace", secondNamespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			EqualObject(&expectedIPAddressInSecondNamespace, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress),
		// 		)
		// 	})
		// })

		// When("the referenced global pool does not exists", func() {
		// 	const wrongPoolName = "wrong-test-pool"
		// 	const poolName = "test-pool"

		// 	BeforeEach(func() {
		// 		pool := v1alpha2.GlobalInClusterIPPool{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name: poolName,
		// 			},
		// 			Spec: v1alpha2.InClusterIPPoolSpec{
		// 				Addresses: []string{"10.0.0.2-10.0.0.254"},
		// 				Prefix:    24,
		// 				Gateway:   "10.0.0.1",
		// 			},
		// 		}
		// 		Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
		// 		Eventually(Get(&pool)).Should(Succeed())
		// 	})

		// 	AfterEach(func() {
		// 		deleteClaim("test", namespace)
		// 		deleteClusterScopedPool(poolName)
		// 	})

		// 	It("should not allocate an Address from the Pool", func() {
		// 		claim := newClaim("test", namespace, "GlobalInClusterIPPool", wrongPoolName)
		// 		Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

		// 		addresses := ipamv1.IPAddressList{}
		// 		Consistently(ObjectList(&addresses)).
		// 			WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			HaveField("Items", HaveLen(0)))

		// 	})
		// })

		// When("the referenced pool uses single ip addresses", func() {
		// 	const poolName = "test-pool-single-ip-addresses"
		// 	var claim1, claim2 ipamv1.IPAddressClaim

		// 	BeforeEach(func() {
		// 		pool := v1alpha2.InClusterIPPool{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      poolName,
		// 				Namespace: namespace,
		// 			},
		// 			Spec: v1alpha2.InClusterIPPoolSpec{
		// 				Addresses: []string{
		// 					"10.0.0.50",
		// 					"10.0.0.128",
		// 				},
		// 				Prefix:  24,
		// 				Gateway: "10.0.0.1",
		// 			},
		// 		}
		// 		Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
		// 		Eventually(Get(&pool)).Should(Succeed())
		// 	})

		// 	AfterEach(func() {
		// 		deleteClaim(claim1.Name, claim1.Namespace)
		// 		deleteClaim(claim2.Name, claim2.Namespace)
		// 		deleteNamespacedPool(poolName, namespace)
		// 	})

		// 	It("should allocate an Address from the Pool", func() {
		// 		claim1 = newClaim("test-1", namespace, "InClusterIPPool", poolName)
		// 		Expect(k8sClient.Create(context.Background(), &claim1)).To(Succeed())

		// 		claim2 = newClaim("test-2", namespace, "InClusterIPPool", poolName)
		// 		Expect(k8sClient.Create(context.Background(), &claim2)).To(Succeed())

		// 		Eventually(findAddress("test-1", namespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			HaveField("Spec.Address", "10.0.0.50"))

		// 		Eventually(findAddress("test-2", namespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			HaveField("Spec.Address", "10.0.0.128"))
		// 	})
		// })

		// When("there are two pools with the same name in different namespaces", func() {
		// 	const commonPoolName = "common-pool-name"
		// 	var secondNamespace string
		// 	var claim1, claim2 ipamv1.IPAddressClaim

		// 	BeforeEach(func() {
		// 		poolA := v1alpha2.InClusterIPPool{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      commonPoolName,
		// 				Namespace: namespace,
		// 			},
		// 			Spec: v1alpha2.InClusterIPPoolSpec{
		// 				Addresses: []string{"10.0.0.50"},
		// 				Prefix:    24,
		// 				Gateway:   "10.0.0.1",
		// 			},
		// 		}
		// 		Expect(k8sClient.Create(context.Background(), &poolA)).To(Succeed())
		// 		Eventually(Get(&poolA)).Should(Succeed())

		// 		secondNamespace = createNamespace()

		// 		poolB := v1alpha2.InClusterIPPool{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      commonPoolName,
		// 				Namespace: secondNamespace,
		// 			},
		// 			Spec: v1alpha2.InClusterIPPoolSpec{
		// 				Addresses: []string{"10.0.0.50"},
		// 				Prefix:    24,
		// 				Gateway:   "10.0.0.1",
		// 			},
		// 		}
		// 		Expect(k8sClient.Create(context.Background(), &poolB)).To(Succeed())
		// 		Eventually(Get(&poolB)).Should(Succeed())
		// 	})

		// 	AfterEach(func() {
		// 		deleteClaim(claim1.Name, claim1.Namespace)
		// 		deleteClaim(claim2.Name, claim2.Namespace)
		// 		deleteNamespacedPool(commonPoolName, namespace)
		// 		deleteNamespacedPool(commonPoolName, secondNamespace)
		// 	})

		// 	It("should allocate Addresses from each Pool independently", func() {
		// 		claim1 = newClaim("test-1", namespace, "InClusterIPPool", commonPoolName)
		// 		claim2 = newClaim("test-2", secondNamespace, "InClusterIPPool", commonPoolName)
		// 		Expect(k8sClient.Create(context.Background(), &claim1)).To(Succeed())
		// 		Expect(k8sClient.Create(context.Background(), &claim2)).To(Succeed())

		// 		expectedAddress1 := ipamv1.IPAddress{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:       "test-1",
		// 				Namespace:  namespace,
		// 				Finalizers: []string{ipamutil.ProtectAddressFinalizer},
		// 				OwnerReferences: []metav1.OwnerReference{
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(true),
		// 						Kind:               "IPAddressClaim",
		// 						Name:               "test-1",
		// 					},
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha2",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(false),
		// 						Kind:               "InClusterIPPool",
		// 						Name:               commonPoolName,
		// 					},
		// 				},
		// 			},
		// 			Spec: ipamv1.IPAddressSpec{
		// 				ClaimRef: corev1.LocalObjectReference{
		// 					Name: "test-1",
		// 				},
		// 				PoolRef: corev1.TypedLocalObjectReference{
		// 					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
		// 					Kind:     "InClusterIPPool",
		// 					Name:     commonPoolName,
		// 				},
		// 				Address: "10.0.0.50",
		// 				Prefix:  24,
		// 				Gateway: "10.0.0.1",
		// 			},
		// 		}

		// 		expectedAddress2 := ipamv1.IPAddress{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:       "test-2",
		// 				Namespace:  secondNamespace,
		// 				Finalizers: []string{ipamutil.ProtectAddressFinalizer},
		// 				OwnerReferences: []metav1.OwnerReference{
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(true),
		// 						Kind:               "IPAddressClaim",
		// 						Name:               "test-2",
		// 					},
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha2",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(false),
		// 						Kind:               "InClusterIPPool",
		// 						Name:               commonPoolName,
		// 					},
		// 				},
		// 			},
		// 			Spec: ipamv1.IPAddressSpec{
		// 				ClaimRef: corev1.LocalObjectReference{
		// 					Name: "test-2",
		// 				},
		// 				PoolRef: corev1.TypedLocalObjectReference{
		// 					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
		// 					Kind:     "InClusterIPPool",
		// 					Name:     commonPoolName,
		// 				},
		// 				Address: "10.0.0.50",
		// 				Prefix:  24,
		// 				Gateway: "10.0.0.1",
		// 			},
		// 		}

		// 		Eventually(findAddress("test-1", namespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			EqualObject(&expectedAddress1, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))

		// 		Eventually(findAddress("test-2", secondNamespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			EqualObject(&expectedAddress2, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
		// 	})
		// })

		// When("two pools with the same name, one in a namespace and one cluster-scoped, exist", func() {
		// 	const commonPoolName = "comomon-pool-name"
		// 	var claimFromNamespacedPool, claimFromGlobalPool ipamv1.IPAddressClaim

		// 	BeforeEach(func() {
		// 		namespacedPool := v1alpha2.InClusterIPPool{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      commonPoolName,
		// 				Namespace: namespace,
		// 			},
		// 			Spec: v1alpha2.InClusterIPPoolSpec{
		// 				Addresses: []string{"10.0.0.50"},
		// 				Prefix:    24,
		// 				Gateway:   "10.0.0.1",
		// 			},
		// 		}
		// 		Expect(k8sClient.Create(context.Background(), &namespacedPool)).To(Succeed())
		// 		Eventually(Get(&namespacedPool)).Should(Succeed())

		// 		globalPool := v1alpha2.GlobalInClusterIPPool{
		// 			ObjectMeta: metav1.ObjectMeta{ // global pool, no namespace
		// 				Name: commonPoolName,
		// 			},
		// 			Spec: v1alpha2.InClusterIPPoolSpec{
		// 				Addresses: []string{"10.0.0.50"},
		// 				Prefix:    24,
		// 				Gateway:   "10.0.0.1",
		// 			},
		// 		}

		// 		Expect(k8sClient.Create(context.Background(), &globalPool)).To(Succeed())
		// 		Eventually(Get(&globalPool)).Should(Succeed())
		// 	})

		// 	AfterEach(func() {
		// 		deleteClaim(claimFromNamespacedPool.Name, claimFromNamespacedPool.Namespace)
		// 		deleteClaim(claimFromGlobalPool.Name, claimFromGlobalPool.Namespace)
		// 		deleteNamespacedPool(commonPoolName, namespace)
		// 		deleteClusterScopedPool(commonPoolName)
		// 	})

		// 	It("should allocate Addresses from each Pool independently", func() {
		// 		claimFromNamespacedPool = newClaim("test-1", namespace, "InClusterIPPool", commonPoolName)
		// 		claimFromGlobalPool = newClaim("test-2", namespace, "GlobalInClusterIPPool", commonPoolName)

		// 		expectedAddress1 := ipamv1.IPAddress{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:       "test-1",
		// 				Namespace:  namespace,
		// 				Finalizers: []string{ipamutil.ProtectAddressFinalizer},
		// 				OwnerReferences: []metav1.OwnerReference{
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(true),
		// 						Kind:               "IPAddressClaim",
		// 						Name:               "test-1",
		// 					},
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha2",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(false),
		// 						Kind:               "InClusterIPPool",
		// 						Name:               commonPoolName,
		// 					},
		// 				},
		// 			},
		// 			Spec: ipamv1.IPAddressSpec{
		// 				ClaimRef: corev1.LocalObjectReference{
		// 					Name: "test-1",
		// 				},
		// 				PoolRef: corev1.TypedLocalObjectReference{
		// 					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
		// 					Kind:     "InClusterIPPool",
		// 					Name:     commonPoolName,
		// 				},
		// 				Address: "10.0.0.50",
		// 				Prefix:  24,
		// 				Gateway: "10.0.0.1",
		// 			},
		// 		}

		// 		expectedAddress2 := ipamv1.IPAddress{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:       "test-2",
		// 				Namespace:  namespace,
		// 				Finalizers: []string{ipamutil.ProtectAddressFinalizer},
		// 				OwnerReferences: []metav1.OwnerReference{
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(true),
		// 						Kind:               "IPAddressClaim",
		// 						Name:               "test-2",
		// 					},
		// 					{
		// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha2",
		// 						BlockOwnerDeletion: pointer.Bool(true),
		// 						Controller:         pointer.Bool(false),
		// 						Kind:               "GlobalInClusterIPPool",
		// 						Name:               commonPoolName,
		// 					},
		// 				},
		// 			},
		// 			Spec: ipamv1.IPAddressSpec{
		// 				ClaimRef: corev1.LocalObjectReference{
		// 					Name: "test-2",
		// 				},
		// 				PoolRef: corev1.TypedLocalObjectReference{
		// 					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
		// 					Kind:     "GlobalInClusterIPPool",
		// 					Name:     commonPoolName,
		// 				},
		// 				Address: "10.0.0.50",
		// 				Prefix:  24,
		// 				Gateway: "10.0.0.1",
		// 			},
		// 		}

		// 		Expect(k8sClient.Create(context.Background(), &claimFromNamespacedPool)).To(Succeed())
		// 		Expect(k8sClient.Create(context.Background(), &claimFromGlobalPool)).To(Succeed())

		// 		Eventually(findAddress(expectedAddress1.GetName(), namespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			EqualObject(&expectedAddress1, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))

		// 		Eventually(findAddress(expectedAddress2.GetName(), namespace)).
		// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
		// 			EqualObject(&expectedAddress2, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
		// 	})
		// })

		When("the pool is paused", func() {
			When("a claim is created", func() {
				const poolName = "paused-pool"
				const claimName = "paused-pool-test"
				var pool v1alpha1.InfobloxIPPool

				BeforeEach(func() {
					localInfobloxClientMock = ibmock.NewMockClient(mockCtrl)
					getInfobloxClientForInstanceFunc = mockGetInfobloxClientForInstance
					pool = v1alpha1.InfobloxIPPool{
						ObjectMeta: metav1.ObjectMeta{
							Name:      poolName,
							Namespace: namespace,
							Annotations: map[string]string{
								clusterv1.PausedAnnotation: "",
							},
						},
						Spec: v1alpha1.InfobloxIPPoolSpec{
							InstanceRef: corev1.LocalObjectReference{Name: instanceName},
							Subnet:      "10.0.0.0/24",
							NetworkView: "default",
							DNSZone:     "",
						},
					}
					Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
					Eventually(Get(&pool)).Should(Succeed())
				})

				AfterEach(func() {
					deleteClaim(claimName, namespace)
					deleteNamespacedPool(poolName, namespace)
					getInfobloxClientForInstanceFunc = getInfobloxClientForInstance
				})

				It("should not create an IPAddress for claims until the pool is unpaused", func() {
					addr, err := netip.ParseAddr("10.0.0.1")
					Expect(err).NotTo(HaveOccurred())
					localInfobloxClientMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(addr, nil).AnyTimes()
					localInfobloxClientMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

					tmpPool := &v1alpha1.InfobloxIPPool{}
					err = k8sClient.Get(ctx, client.ObjectKeyFromObject(&pool), tmpPool)
					Expect(err).NotTo(HaveOccurred())
					Expect(annotations.HasPaused(tmpPool)).To(BeTrue())

					claim := newClaim(claimName, namespace, "InfobloxIPPool", poolName)
					Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

					addresses := ipamv1.IPAddressList{}
					Consistently(ObjectList(&addresses)).
						WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
						HaveField("Items", HaveLen(0)))

					patchHelper, err := patch.NewHelper(&pool, k8sClient)
					Expect(err).NotTo(HaveOccurred())
					delete(pool.Annotations, clusterv1.PausedAnnotation)
					Expect(pool.Annotations).Should(BeEmpty())
					err = patchHelper.Patch(ctx, &pool)
					Expect(err).NotTo(HaveOccurred())

					Eventually(ObjectList(&addresses)).
						WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
						HaveField("Items", HaveLen(1)))
				})
			})

			When("a claim is deleted", func() {
				const poolName = "paused-delete-claim-pool" // #nosec G101
				var pool v1alpha1.InfobloxIPPool

				BeforeEach(func() {
					localInfobloxClientMock = ibmock.NewMockClient(mockCtrl)
					getInfobloxClientForInstanceFunc = mockGetInfobloxClientForInstance
					pool = v1alpha1.InfobloxIPPool{
						ObjectMeta: metav1.ObjectMeta{
							Name:      poolName,
							Namespace: namespace,
						},
						Spec: v1alpha1.InfobloxIPPoolSpec{
							InstanceRef: corev1.LocalObjectReference{Name: instanceName},
							Subnet:      "10.0.0.0/24",
							NetworkView: "default",
							DNSZone:     "",
						},
					}
					Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
					Eventually(Get(&pool)).Should(Succeed())
				})

				AfterEach(func() {
					deleteNamespacedPool(poolName, namespace)
					getInfobloxClientForInstanceFunc = getInfobloxClientForInstance
				})

				It("should prevent deletion of claims", func() {
					addr, err := netip.ParseAddr("10.0.0.1")
					Expect(err).NotTo(HaveOccurred())
					localInfobloxClientMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(addr, nil).AnyTimes()
					localInfobloxClientMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

					claim := newClaim("paused-pool-delete-claim-test", namespace, "InfobloxIPPool", poolName)
					Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

					claims := ipamv1.IPAddressClaimList{}
					Eventually(ObjectList(&claims)).
						WithTimeout(10 * time.Second).WithPolling(100 * time.Millisecond).Should(
						HaveField("Items", HaveLen(1)))

					patchHelper, err := patch.NewHelper(&pool, k8sClient)
					Expect(err).NotTo(HaveOccurred())
					pool.Annotations = map[string]string{
						clusterv1.PausedAnnotation: "",
					}
					err = patchHelper.Patch(ctx, &pool)
					Expect(err).NotTo(HaveOccurred())

					time.Sleep(1 * time.Second)

					Expect(k8sClient.Delete(context.Background(), &claim)).To(Succeed())
					Consistently(ObjectList(&claims)).
						WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
						HaveField("Items", HaveLen(1)))

					patchHelper, err = patch.NewHelper(&pool, k8sClient)
					Expect(err).NotTo(HaveOccurred())
					delete(pool.Annotations, clusterv1.PausedAnnotation)
					err = patchHelper.Patch(ctx, &pool)
					Expect(err).NotTo(HaveOccurred())

					Eventually(ObjectList(&claims)).
						WithTimeout(10 * time.Second).WithPolling(100 * time.Millisecond).Should(
						HaveField("Items", HaveLen(0)))
				})
			})
		})
	})

	Context("When an existing IPAddress with no ownerReferences is missing finalizers and owner references", func() {
		const poolName = "test-pool"

		BeforeEach(func() {
			localInfobloxClientMock = ibmock.NewMockClient(mockCtrl)
			getInfobloxClientForInstanceFunc = mockGetInfobloxClientForInstance
			pool := v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: v1alpha1.InfobloxIPPoolSpec{
					InstanceRef: corev1.LocalObjectReference{Name: instanceName},
					Subnet:      "10.0.0.0/24",
					NetworkView: "default",
					DNSZone:     "",
				},
			}
			Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
			Eventually(Get(&pool)).Should(Succeed())
		})

		AfterEach(func() {
			deleteClaim("test", namespace)
			deleteNamespacedPool(poolName, namespace)
			getInfobloxClientForInstanceFunc = getInfobloxClientForInstance
		})

		It("should add the owner references and finalizer", func() {
			addr, err := netip.ParseAddr("10.0.0.1")
			Expect(err).NotTo(HaveOccurred())
			localInfobloxClientMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(addr, nil).AnyTimes()
			localInfobloxClientMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			addressSpec := ipamv1.IPAddressSpec{
				ClaimRef: corev1.LocalObjectReference{
					Name: "test",
				},
				PoolRef: corev1.TypedLocalObjectReference{
					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
					Kind:     "InfobloxIPPool",
					Name:     poolName,
				},
				Address: "10.0.0.1",
				Prefix:  24,
				Gateway: "10.0.0.2",
			}

			address := ipamv1.IPAddress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: namespace,
				},
				Spec: addressSpec,
			}

			Expect(k8sClient.Create(context.Background(), &address)).To(Succeed())

			claim := newClaim("test", namespace, "InfobloxIPPool", poolName)
			Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

			expectedIPAddress := ipamv1.IPAddress{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  namespace,
					Finalizers: []string{ipamutil.ProtectAddressFinalizer},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
							BlockOwnerDeletion: pointer.Bool(true),
							Controller:         pointer.Bool(true),
							Kind:               "IPAddressClaim",
							Name:               "test",
						},
						{
							APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
							BlockOwnerDeletion: pointer.Bool(true),
							Controller:         pointer.Bool(false),
							Kind:               "InfobloxIPPool",
							Name:               poolName,
						},
					},
				},
				Spec: addressSpec,
			}

			Eventually(findAddress("test", namespace)).
				WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				EqualObject(&expectedIPAddress, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
		})
	})

	Context("When an existing IPAddress with an unrelated ownerRef is missing finalizers and IPAM owner references", func() {
		const poolName = "test-pool"

		BeforeEach(func() {
			localInfobloxClientMock = ibmock.NewMockClient(mockCtrl)
			getInfobloxClientForInstanceFunc = mockGetInfobloxClientForInstance
			pool := v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: v1alpha1.InfobloxIPPoolSpec{
					InstanceRef: corev1.LocalObjectReference{Name: instanceName},
					Subnet:      "10.0.0.0/24",
					NetworkView: "default",
					DNSZone:     "",
				},
			}
			Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
			Eventually(Get(&pool)).Should(Succeed())
		})

		AfterEach(func() {
			deleteClaim("test", namespace)
			deleteNamespacedPool(poolName, namespace)
			getInfobloxClientForInstanceFunc = getInfobloxClientForInstance
		})

		It("should add the owner references and finalizer", func() {
			addr, err := netip.ParseAddr("10.0.0.1")
			Expect(err).NotTo(HaveOccurred())
			localInfobloxClientMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(addr, nil).AnyTimes()
			localInfobloxClientMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			addressSpec := ipamv1.IPAddressSpec{
				ClaimRef: corev1.LocalObjectReference{
					Name: "test",
				},
				PoolRef: corev1.TypedLocalObjectReference{
					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
					Kind:     "InfobloxIPPool",
					Name:     poolName,
				},
				Address: "10.0.0.1",
				Prefix:  24,
				Gateway: "",
			}
			address := ipamv1.IPAddress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: namespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "alpha-dummy",
							Kind:       "dummy-kind",
							Name:       "dummy-name",
							UID:        "abc-dummy-123",
						},
					},
				},
				Spec: addressSpec,
			}

			Expect(k8sClient.Create(context.Background(), &address)).To(Succeed())

			claim := newClaim("test", namespace, "InfobloxIPPool", poolName)
			Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

			expectedIPAddress := ipamv1.IPAddress{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  namespace,
					Finalizers: []string{ipamutil.ProtectAddressFinalizer},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "alpha-dummy",
							Kind:       "dummy-kind",
							Name:       "dummy-name",
							UID:        "abc-dummy-123",
						},
						{
							APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
							BlockOwnerDeletion: pointer.Bool(true),
							Controller:         pointer.Bool(true),
							Kind:               "IPAddressClaim",
							Name:               "test",
						},
						{
							APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
							BlockOwnerDeletion: pointer.Bool(true),
							Controller:         pointer.Bool(false),
							Kind:               "InfobloxIPPool",
							Name:               poolName,
						},
					},
				},
				Spec: addressSpec,
			}

			Eventually(findAddress("test", namespace)).
				WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
				EqualObject(&expectedIPAddress, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
		})
	})

	// Context("When a GlobalInClusterIPPool has two claims with the same name in two different namespaces", func() {
	// 	const poolName = "test-pool"

	// 	BeforeEach(func() {
	// 		pool := v1alpha2.GlobalInClusterIPPool{
	// 			ObjectMeta: metav1.ObjectMeta{
	// 				Name: poolName,
	// 			},
	// 			Spec: v1alpha2.InClusterIPPoolSpec{
	// 				Addresses: []string{
	// 					"10.0.0.2-10.0.0.254",
	// 				},
	// 				Prefix:  24,
	// 				Gateway: "10.0.0.1",
	// 			},
	// 		}
	// 		Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
	// 		Eventually(Get(&pool)).Should(Succeed())
	// 	})

	// 	AfterEach(func() {
	// 		deleteClaim("test", namespace)
	// 		deleteClaim("test", namespace2)
	// 		deleteClusterScopedPool(poolName)
	// 	})

	// 	It("should successfully create different ip addresses for both claims", func() {
	// 		claim := newClaim("test", namespace, "GlobalInClusterIPPool", poolName)
	// 		claim2 := newClaim("test", namespace2, "GlobalInClusterIPPool", poolName)

	// 		expectedIPAddress := ipamv1.IPAddress{
	// 			ObjectMeta: metav1.ObjectMeta{
	// 				Name:       "test",
	// 				Namespace:  namespace,
	// 				Finalizers: []string{ipamutil.ProtectAddressFinalizer},
	// 				OwnerReferences: []metav1.OwnerReference{
	// 					{
	// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
	// 						BlockOwnerDeletion: pointer.Bool(true),
	// 						Controller:         pointer.Bool(true),
	// 						Kind:               "IPAddressClaim",
	// 						Name:               "test",
	// 					},
	// 					{
	// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha2",
	// 						BlockOwnerDeletion: pointer.Bool(true),
	// 						Controller:         pointer.Bool(false),
	// 						Kind:               "GlobalInClusterIPPool",
	// 						Name:               poolName,
	// 					},
	// 				},
	// 			},
	// 			Spec: ipamv1.IPAddressSpec{
	// 				ClaimRef: corev1.LocalObjectReference{
	// 					Name: "test",
	// 				},
	// 				PoolRef: corev1.TypedLocalObjectReference{
	// 					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
	// 					Kind:     "GlobalInClusterIPPool",
	// 					Name:     poolName,
	// 				},
	// 				Address: "10.0.0.2",
	// 				Prefix:  24,
	// 				Gateway: "10.0.0.1",
	// 			},
	// 		}

	// 		expectedIPAddress2 := ipamv1.IPAddress{
	// 			ObjectMeta: metav1.ObjectMeta{
	// 				Name:       "test",
	// 				Namespace:  namespace2,
	// 				Finalizers: []string{ipamutil.ProtectAddressFinalizer},
	// 				OwnerReferences: []metav1.OwnerReference{
	// 					{
	// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
	// 						BlockOwnerDeletion: pointer.Bool(true),
	// 						Controller:         pointer.Bool(true),
	// 						Kind:               "IPAddressClaim",
	// 						Name:               "test",
	// 					},
	// 					{
	// 						APIVersion:         "ipam.cluster.x-k8s.io/v1alpha2",
	// 						BlockOwnerDeletion: pointer.Bool(true),
	// 						Controller:         pointer.Bool(false),
	// 						Kind:               "GlobalInClusterIPPool",
	// 						Name:               poolName,
	// 					},
	// 				},
	// 			},
	// 			Spec: ipamv1.IPAddressSpec{
	// 				ClaimRef: corev1.LocalObjectReference{
	// 					Name: "test",
	// 				},
	// 				PoolRef: corev1.TypedLocalObjectReference{
	// 					APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
	// 					Kind:     "GlobalInClusterIPPool",
	// 					Name:     poolName,
	// 				},
	// 				Address: "10.0.0.3",
	// 				Prefix:  24,
	// 				Gateway: "10.0.0.1",
	// 			},
	// 		}

	// 		Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())
	// 		Eventually(findAddress("test", namespace)).
	// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
	// 			EqualObject(&expectedIPAddress, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress),
	// 		)

	// 		Expect(k8sClient.Create(context.Background(), &claim2)).To(Succeed())
	// 		Eventually(findAddress("test", namespace2)).
	// 			WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
	// 			EqualObject(&expectedIPAddress2, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress),
	// 		)
	// 	})
	// })

	Context("When the cluster is spec.paused true and the ipaddressclaim has the cluster-name label", func() {
		const (
			clusterName = "test-cluster"
			poolName    = "test-pool"
		)

		var (
			cluster clusterv1.Cluster
		)

		BeforeEach(func() {
			localInfobloxClientMock = ibmock.NewMockClient(mockCtrl)
			getInfobloxClientForInstanceFunc = mockGetInfobloxClientForInstance

			pool := v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: v1alpha1.InfobloxIPPoolSpec{
					InstanceRef: corev1.LocalObjectReference{Name: instanceName},
					Subnet:      "10.0.0.0/24",
					NetworkView: "default",
					DNSZone:     "",
				},
			}
			Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
			Eventually(Get(&pool)).Should(Succeed())
		})

		Context("When the cluster can be retrieved", func() {
			AfterEach(func() {
				deleteClaim("test", namespace)
				deleteNamespacedPool(poolName, namespace)
				deleteCluster(clusterName, namespace)
				getInfobloxClientForInstanceFunc = getInfobloxClientForInstance
			})

			It("does not allocate an ipaddress upon creating a cluster when the cluster has paused annotation", func() {
				claim := ipamv1.IPAddressClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: namespace,
						Labels: map[string]string{
							clusterv1.ClusterNameLabel: clusterName,
						},
					},
					Spec: ipamv1.IPAddressClaimSpec{
						PoolRef: corev1.TypedLocalObjectReference{
							APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
							Kind:     "InfobloxIPPool",
							Name:     poolName,
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())
				Eventually(Get(&claim)).Should(Succeed())

				cluster = clusterv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterName,
						Namespace: namespace,
						Annotations: map[string]string{
							clusterv1.PausedAnnotation: "",
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), &cluster)).To(Succeed())
				Eventually(Get(&cluster)).Should(Succeed())

				addresses := ipamv1.IPAddressList{}
				Consistently(ObjectList(&addresses)).
					WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(0)))
			})

			It("does not allocate an ipaddress upon creating a cluster when the cluster has spec.Paused", func() {
				claim := ipamv1.IPAddressClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: namespace,
						Labels: map[string]string{
							clusterv1.ClusterNameLabel: clusterName,
						},
					},
					Spec: ipamv1.IPAddressClaimSpec{
						PoolRef: corev1.TypedLocalObjectReference{
							APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
							Kind:     "InfobloxIPPool",
							Name:     poolName,
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())
				Eventually(Get(&claim)).Should(Succeed())

				cluster = clusterv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterName,
						Namespace: namespace,
					},
					Spec: clusterv1.ClusterSpec{
						Paused: true,
					},
				}

				Expect(k8sClient.Create(context.Background(), &cluster)).To(Succeed())
				Eventually(Get(&cluster)).Should(Succeed())

				addresses := ipamv1.IPAddressList{}
				Consistently(ObjectList(&addresses)).
					WithTimeout(1 * time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(0)))
			})

			It("does not allocate an ipaddress upon updating a cluster when the cluster has spec.paused", func() {
				cluster = clusterv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterName,
						Namespace: namespace,
					},
					Spec: clusterv1.ClusterSpec{
						Paused: true,
					},
				}

				Expect(k8sClient.Create(context.Background(), &cluster)).To(Succeed())
				Eventually(Get(&cluster)).Should(Succeed())

				claim := ipamv1.IPAddressClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: namespace,
						Labels: map[string]string{
							clusterv1.ClusterNameLabel: clusterName,
						},
					},
					Spec: ipamv1.IPAddressClaimSpec{
						PoolRef: corev1.TypedLocalObjectReference{
							APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
							Kind:     "InfobloxIPPool",
							Name:     poolName,
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())
				Eventually(Get(&claim)).Should(Succeed())

				// update the cluster
				cluster.Annotations = map[string]string{"superficial": "change"}
				Expect(k8sClient.Update(context.Background(), &cluster)).To(Succeed())

				addresses := ipamv1.IPAddressList{}
				Consistently(ObjectList(&addresses)).
					WithTimeout(1 * time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(0)))
			})

			It("does not allocate an ipaddress upon updating a cluster when the cluster has paused annotation", func() {
				cluster = clusterv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterName,
						Namespace: namespace,
						Annotations: map[string]string{
							clusterv1.PausedAnnotation: "",
						},
					},
				}

				Expect(k8sClient.Create(context.Background(), &cluster)).To(Succeed())
				Eventually(Get(&cluster)).Should(Succeed())

				claim := ipamv1.IPAddressClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: namespace,
						Labels: map[string]string{
							clusterv1.ClusterNameLabel: clusterName,
						},
					},
					Spec: ipamv1.IPAddressClaimSpec{
						PoolRef: corev1.TypedLocalObjectReference{
							APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
							Kind:     "InfobloxIPPool",
							Name:     poolName,
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())
				Eventually(Get(&claim)).Should(Succeed())

				// update the cluster
				cluster.Annotations["superficial"] = "change"
				Expect(k8sClient.Update(context.Background(), &cluster)).To(Succeed())

				addresses := ipamv1.IPAddressList{}
				Consistently(ObjectList(&addresses)).
					WithTimeout(1 * time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(0)))
			})

			It("allocates an ipaddress upon updating a cluster when removing spec.paused", func() {
				addr, err := netip.ParseAddr("10.0.0.1")
				Expect(err).NotTo(HaveOccurred())
				localInfobloxClientMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(addr, nil).AnyTimes()
				localInfobloxClientMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

				cluster = clusterv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterName,
						Namespace: namespace,
					},
					Spec: clusterv1.ClusterSpec{
						Paused: true,
					},
				}

				Expect(k8sClient.Create(context.Background(), &cluster)).To(Succeed())
				Eventually(Get(&cluster)).Should(Succeed())

				claim := newClaim("test", namespace, "InfobloxIPPool", poolName)
				claim.ObjectMeta.Labels = map[string]string{
					clusterv1.ClusterNameLabel: cluster.GetName(),
				}
				Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

				addresses := ipamv1.IPAddressList{}
				Consistently(ObjectList(&addresses)).
					WithTimeout(1 * time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(0)))

				// update the cluster
				cluster.Spec.Paused = false
				Expect(k8sClient.Update(context.Background(), &cluster)).To(Succeed())

				Eventually(ObjectList(&addresses)).
					WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(1)))
			})

			It("allocates an ipaddress upon updating a cluster when removing the paused annotation", func() {
				addr, err := netip.ParseAddr("10.0.0.1")
				Expect(err).NotTo(HaveOccurred())
				localInfobloxClientMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(addr, nil).AnyTimes()
				localInfobloxClientMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

				cluster = clusterv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterName,
						Namespace: namespace,
						Annotations: map[string]string{
							clusterv1.PausedAnnotation: "",
						},
					},
				}

				Expect(k8sClient.Create(context.Background(), &cluster)).To(Succeed())
				Eventually(Get(&cluster)).Should(Succeed())

				claim := ipamv1.IPAddressClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: namespace,
						Labels: map[string]string{
							clusterv1.ClusterNameLabel: clusterName,
						},
					},
					Spec: ipamv1.IPAddressClaimSpec{
						PoolRef: corev1.TypedLocalObjectReference{
							APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
							Kind:     "InfobloxIPPool",
							Name:     poolName,
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())
				Eventually(Get(&claim)).Should(Succeed())

				addresses := ipamv1.IPAddressList{}
				Consistently(ObjectList(&addresses)).
					WithTimeout(1 * time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(0)))

				// update the cluster
				delete(cluster.Annotations, clusterv1.PausedAnnotation)
				Expect(k8sClient.Update(context.Background(), &cluster)).To(Succeed())

				Eventually(ObjectList(&addresses)).
					WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(1)))
			})
		})

		Context("When the cluster cannot be retrieved", func() {
			AfterEach(func() {
				deleteClaim("test", namespace)
				deleteNamespacedPool(poolName, namespace)
			})
			It("does not allocate an ipaddress for the claim", func() {
				claim := newClaim("test", namespace, "InfobloxIPPool", poolName)
				claim.ObjectMeta.Labels = map[string]string{
					clusterv1.ClusterNameLabel: "an-unfindable-cluster",
				}
				Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())
				Eventually(Get(&claim)).Should(Succeed())

				addresses := ipamv1.IPAddressList{}
				Consistently(ObjectList(&addresses)).
					WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
					HaveField("Items", HaveLen(0)))
			})
		})
	})

	Context("When the ipaddressclaim is paused", func() {
		const (
			poolName = "test-pool"
		)

		BeforeEach(func() {
			localInfobloxClientMock = ibmock.NewMockClient(mockCtrl)
			getInfobloxClientForInstanceFunc = mockGetInfobloxClientForInstance

			pool := v1alpha1.InfobloxIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: v1alpha1.InfobloxIPPoolSpec{
					InstanceRef: corev1.LocalObjectReference{Name: instanceName},
					Subnet:      "10.0.0.0/24",
					NetworkView: "default",
					DNSZone:     "",
				},
			}
			Expect(k8sClient.Create(context.Background(), &pool)).To(Succeed())
			Eventually(Get(&pool)).Should(Succeed())
		})

		AfterEach(func() {
			deleteClaim("test", namespace)
			deleteNamespacedPool(poolName, namespace)
			getInfobloxClientForInstanceFunc = getInfobloxClientForInstance
		})

		It("does not allocate an ipaddress for the claim until the ip address claim is unpaused", func() {
			addr, err := netip.ParseAddr("10.0.0.1")
			Expect(err).NotTo(HaveOccurred())
			localInfobloxClientMock.EXPECT().GetOrAllocateAddress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(addr, nil).AnyTimes()
			localInfobloxClientMock.EXPECT().ReleaseAddress(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			claim := newClaim("test", namespace, "InfobloxIPPool", poolName)
			claim.ObjectMeta.Annotations = map[string]string{
				clusterv1.PausedAnnotation: "",
			}
			Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())
			Eventually(Get(&claim)).Should(Succeed())

			addresses := ipamv1.IPAddressList{}
			Consistently(ObjectList(&addresses)).
				WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
				HaveField("Items", HaveLen(0)))

			// Unpause the IPAddressClaim
			patchHelper, err := patch.NewHelper(&claim, k8sClient)
			Expect(err).NotTo(HaveOccurred())
			delete(claim.Annotations, clusterv1.PausedAnnotation)
			Expect(patchHelper.Patch(context.Background(), &claim)).To(Succeed())

			expectedIPAddress := ipamv1.IPAddress{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  namespace,
					Finalizers: []string{ipamutil.ProtectAddressFinalizer},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
							BlockOwnerDeletion: pointer.Bool(true),
							Controller:         pointer.Bool(true),
							Kind:               "IPAddressClaim",
							Name:               "test",
						},
						{
							APIVersion:         "ipam.cluster.x-k8s.io/v1alpha1",
							BlockOwnerDeletion: pointer.Bool(true),
							Controller:         pointer.Bool(false),
							Kind:               "InfobloxIPPool",
							Name:               poolName,
						},
					},
				},
				Spec: ipamv1.IPAddressSpec{
					ClaimRef: corev1.LocalObjectReference{
						Name: "test",
					},
					PoolRef: corev1.TypedLocalObjectReference{
						APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
						Kind:     "InfobloxIPPool",
						Name:     poolName,
					},
					Address: "10.0.0.1",
					Prefix:  24,
					Gateway: "",
				},
			}

			Eventually(findAddress("test", namespace)).
				WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(
				EqualObject(&expectedIPAddress, IgnoreAutogeneratedMetadata, IgnoreUIDsOnIPAddress))
		})
	})
})

func createNamespace() string {
	namespaceObj := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-ns-",
		},
	}
	ExpectWithOffset(1, k8sClient.Create(context.Background(), &namespaceObj)).To(Succeed())
	return namespaceObj.Name
}

func deleteCluster(name, namespace string) {
	cluster := clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err := k8sClient.Delete(context.Background(), &cluster)
	Expect(err).NotTo(HaveOccurred())
	EventuallyWithOffset(1, Get(&cluster)).Should(Not(Succeed()))
}

// func deleteClusterScopedPool(name string) {
// 	pool := v1alpha2.GlobalInClusterIPPool{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name: name,
// 		},
// 	}
// 	ExpectWithOffset(1, k8sClient.Delete(context.Background(), &pool)).To(Succeed())
// 	EventuallyWithOffset(1, Get(&pool)).Should(Not(Succeed()))
// }

func deleteNamespacedPool(name, namespace string) {
	defer GinkgoRecover()
	pool := v1alpha1.InfobloxIPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	ExpectWithOffset(1, k8sClient.Delete(context.Background(), &pool)).To(Succeed())
	EventuallyWithOffset(1, Get(&pool)).Should(Not(Succeed()))
}

func deleteClaim(name, namespace string) {
	defer GinkgoRecover()
	claim := ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	ExpectWithOffset(1, k8sClient.Delete(context.Background(), &claim)).To(Succeed())
	EventuallyWithOffset(1, Get(&claim)).Should(Not(Succeed()))
}

func findAddress(name, namespace string) func() (client.Object, error) {
	address := ipamv1.IPAddress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: ipamv1.IPAddressSpec{},
	}
	return Object(&address)
}

func mockGetInfobloxClientForInstance(_ context.Context, _ client.Reader, _, _ string, _ func(infoblox.Config) (infoblox.Client, error)) (infoblox.Client, error) {
	return localInfobloxClientMock, nil
}
