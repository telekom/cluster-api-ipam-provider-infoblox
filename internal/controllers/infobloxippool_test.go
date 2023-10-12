// /*
// Copyright 2023 The Kubernetes Authors.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     http://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// */

package controllers

// import (
// 	"context"
// 	"fmt"
// 	"time"

// 	. "github.com/onsi/ginkgo/v2"
// 	. "github.com/onsi/gomega"
// 	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
// 	corev1 "k8s.io/api/core/v1"
// 	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// 	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1alpha1"
// 	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"

// 	pooltypes "github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/types"
// )

// var _ = Describe("IP Pool Reconciler", func() {
// 	var namespace string
// 	BeforeEach(func() {
// 		namespace = createNamespace()
// 	})

// 	Describe("Pool usage status", func() {
// 		const testPool = "test-pool"
// 		var createdClaimNames []string
// 		var infobloxPool pooltypes.GenericInfobloxPool

// 		BeforeEach(func() {
// 			createdClaimNames = nil
// 		})

// 		AfterEach(func() {
// 			for _, name := range createdClaimNames {
// 				deleteClaim(name, namespace)
// 			}
// 			Expect(k8sClient.Delete(context.Background(), infobloxPool)).To(Succeed())
// 		})

// 		DescribeTable("it shows the total, used, free ip addresses in the pool",
// 			func(poolType string, prefix int, addresses []string, gateway string, expectedTotal, expectedUsed, expectedFree int) {
// 				infobloxPool = newPool(poolType, testPool, namespace, "10.0.0.2", "10.0.0.0/24", "default", "")
// 				Expect(k8sClient.Create(context.Background(), infobloxPool)).To(Succeed())

// 				Eventually(Object(infobloxPool)).
// 					WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
// 					HaveField("Status.Addresses.Total", Equal(expectedTotal)))

// 				Expect(infobloxPool.PoolStatus().Addresses.Used).To(Equal(0))
// 				Expect(infobloxPool.PoolStatus().Addresses.Free).To(Equal(expectedTotal))

// 				for i := 0; i < expectedUsed; i++ {
// 					claim := newClaim(fmt.Sprintf("test%d", i), namespace, poolType, infobloxPool.GetName())
// 					Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())
// 					createdClaimNames = append(createdClaimNames, claim.Name)
// 				}

// 				Eventually(Object(infobloxPool)).
// 					WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
// 					HaveField("Status.Addresses.Used", Equal(expectedUsed)))
// 				poolStatus := infobloxPool.PoolStatus()
// 				Expect(poolStatus.Addresses.Total).To(Equal(expectedTotal))
// 				Expect(poolStatus.Addresses.Free).To(Equal(expectedFree))
// 			},

// 			Entry("When there is 1 claim and no gateway - InfobloxIPPool",
// 				"InfobloxIPPool", 24, []string{"10.0.0.10-10.0.0.20"}, "", 11, 1, 10),
// 			Entry("When there are 2 claims and no gateway - InfobloxIPPool",
// 				"InfobloxIPPool", 24, []string{"10.0.0.10-10.0.0.20"}, "", 11, 2, 9),
// 			Entry("When there is 1 claim with gateway in range - InfobloxIPPool",
// 				"InfobloxIPPool", 24, []string{"10.0.0.10-10.0.0.20"}, "10.0.0.10", 10, 1, 9),
// 			Entry("When there are 2 claims with gateway in range - InfobloxIPPool",
// 				"InfobloxIPPool", 24, []string{"10.0.0.10-10.0.0.20"}, "10.0.0.10", 10, 2, 8),
// 			Entry("When there is 1 claim with gateway outside of range - InfobloxIPPool",
// 				"InfobloxIPPool", 24, []string{"10.0.0.10-10.0.0.20"}, "10.0.0.1", 11, 1, 10),
// 			Entry("When the addresses range includes network addr, it is not available - InfobloxIPPool",
// 				"InfobloxIPPool", 24, []string{"10.0.0.0-10.0.0.1"}, "10.0.0.2", 1, 1, 0),
// 			Entry("When the addresses range includes broadcast, it is not available - InfobloxIPPool",
// 				"InfobloxIPPool", 24, []string{"10.0.0.254-10.0.0.255"}, "10.0.0.1", 1, 1, 0),
// 			Entry("When the addresses range is IPv6 and the last range in the subnet, it is available - InfobloxIPPool",
// 				"InfobloxIPPool", 120, []string{"fe80::ffff"}, "fe80::a", 1, 1, 0),

// 			Entry("When there is 1 claim and no gateway - GlobalInfobloxIPPool",
// 				"GlobalInfobloxIPPool", 24, []string{"10.0.0.10-10.0.0.20"}, "", 11, 1, 10),
// 			Entry("When there are 2 claims and no gateway - GlobalInfobloxIPPool",
// 				"GlobalInfobloxIPPool", 24, []string{"10.0.0.10-10.0.0.20"}, "", 11, 2, 9),
// 			Entry("When there is 1 claim with gateway in range - GlobalInfobloxIPPool",
// 				"GlobalInfobloxIPPool", 24, []string{"10.0.0.10-10.0.0.20"}, "10.0.0.10", 10, 1, 9),
// 			Entry("When there are 2 claims with gateway in range - GlobalInfobloxIPPool",
// 				"GlobalInfobloxIPPool", 24, []string{"10.0.0.10-10.0.0.20"}, "10.0.0.10", 10, 2, 8),
// 			Entry("When there is 1 claim with gateway outside of range - GlobalInfobloxIPPool",
// 				"GlobalInfobloxIPPool", 24, []string{"10.0.0.10-10.0.0.20"}, "10.0.0.1", 11, 1, 10),
// 			Entry("When the addresses range includes network addr, it is not available - GlobalInfobloxIPPool",
// 				"GlobalInfobloxIPPool", 24, []string{"10.0.0.0-10.0.0.1"}, "10.0.0.2", 1, 1, 0),
// 			Entry("When the addresses range includes broadcast, it is not available - GlobalInfobloxIPPool",
// 				"GlobalInfobloxIPPool", 24, []string{"10.0.0.254-10.0.0.255"}, "10.0.0.1", 1, 1, 0),
// 			Entry("When the addresses range is IPv6 and the last range in the subnet, it is available - GlobalInfobloxIPPool",
// 				"GlobalInfobloxIPPool", 120, []string{"fe80::ffff"}, "fe80::a", 1, 1, 0),
// 		)

// 		DescribeTable("it shows the out of range ips if any",
// 			func(poolType string, addresses []string, gateway string, updatedAddresses []string, numClaims, expectedOutOfRange int) {
// 				poolSpec := v1alpha1.InfobloxIPPoolSpec{
// 					InstanceRef: corev1.LocalObjectReference{},
// 					Subnet:      "10.0.0.0/24",
// 					NetworkView: "default",
// 					DNSZone:     "",
// 				}

// 				switch poolType {
// 				case "InfobloxIPPool":
// 					infobloxPool = &v1alpha1.InfobloxIPPool{
// 						ObjectMeta: metav1.ObjectMeta{GenerateName: testPool, Namespace: namespace},
// 						Spec:       poolSpec,
// 					}
// 				// case "GlobalInfobloxIPPool":
// 				// 	infobloxPool = &v1alpha2.GlobalInfobloxIPPool{
// 				// 		ObjectMeta: metav1.ObjectMeta{GenerateName: testPool, Namespace: namespace},
// 				// 		Spec:       poolSpec,
// 				// 	}
// 				default:
// 					Fail("Unknown pool type")
// 				}

// 				Expect(k8sClient.Create(context.Background(), infobloxPool)).To(Succeed())

// 				for i := 0; i < numClaims; i++ {
// 					claim := newClaim(fmt.Sprintf("test%d", i), namespace, poolType, infobloxPool.GetName())
// 					Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())
// 					createdClaimNames = append(createdClaimNames, claim.Name)
// 				}

// 				Eventually(Object(infobloxPool)).
// 					WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
// 					HaveField("Status.Addresses.Used", Equal(numClaims)))

// 				infobloxPool.PoolSpec().Addresses = updatedAddresses
// 				infobloxPool.PoolSpec().AllocateReservedIPAddresses = false
// 				Expect(k8sClient.Update(context.Background(), infobloxPool)).To(Succeed())

// 				Eventually(Object(infobloxPool)).
// 					WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
// 					HaveField("Status.Addresses.OutOfRange", Equal(expectedOutOfRange)))
// 			},

// 			Entry("InfobloxIPPool",
// 				"InfobloxIPPool", []string{"10.0.0.10-10.0.0.20"}, "10.0.0.1", []string{"10.0.0.13-10.0.0.20"}, 5, 3),
// 			Entry("InfobloxIPPool when removing network address",
// 				"InfobloxIPPool", []string{"10.0.0.0-10.0.0.255"}, "10.0.0.1", []string{"10.0.0.1-10.0.0.255"}, 4, 0),
// 			Entry("InfobloxIPPool when removing gateway address",
// 				"InfobloxIPPool", []string{"10.0.0.0-10.0.0.255"}, "10.0.0.1", []string{"10.0.0.0", "10.0.0.2-10.0.0.255"}, 5, 1),
// 			Entry("InfobloxIPPool when removing broadcast address",
// 				"InfobloxIPPool", []string{"10.0.0.251-10.0.0.255"}, "10.0.0.1", []string{"10.0.0.251-10.0.0.254"}, 5, 1),
// 			Entry("GlobalInfobloxIPPool",
// 				"GlobalInfobloxIPPool", []string{"10.0.0.10-10.0.0.20"}, "10.0.0.1", []string{"10.0.0.13-10.0.0.20"}, 5, 3),
// 			Entry("GlobalInfobloxIPPool when removing network address",
// 				"GlobalInfobloxIPPool", []string{"10.0.0.0-10.0.0.255"}, "10.0.0.1", []string{"10.0.0.1-10.0.0.255"}, 4, 0),
// 			Entry("GlobalInfobloxIPPool when removing gateway address",
// 				"GlobalInfobloxIPPool", []string{"10.0.0.0-10.0.0.255"}, "10.0.0.1", []string{"10.0.0.0", "10.0.0.2-10.0.0.255"}, 5, 1),
// 			Entry("GlobalInfobloxIPPool when removing broadcast address",
// 				"GlobalInfobloxIPPool", []string{"10.0.0.251-10.0.0.255"}, "10.0.0.1", []string{"10.0.0.251-10.0.0.254"}, 5, 1),
// 		)
// 	})

// 	Context("when the pool has IPAddresses", func() {
// 		const poolName = "finalizer-pool-test"

// 		DescribeTable("add a finalizer to prevent pool deletion before IPAddresses are deleted", func(poolType string) {
// 			pool := newPool(poolType, poolName, namespace, "10.0.0.2", "10.0.0.0/24", "default", "")
// 			Expect(k8sClient.Create(context.Background(), pool)).To(Succeed())
// 			Eventually(Get(pool)).Should(Succeed())

// 			claim := newClaim("finalizer-pool-test", namespace, poolType, pool.GetName())

// 			Expect(k8sClient.Create(context.Background(), &claim)).To(Succeed())

// 			addresses := ipamv1.IPAddressList{}
// 			Eventually(ObjectList(&addresses)).
// 				WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
// 				HaveField("Items", HaveLen(1)))

// 			Eventually(Object(pool)).
// 				WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
// 				HaveField("ObjectMeta.Finalizers", ContainElement(ProtectPoolFinalizer)))

// 			Expect(k8sClient.Delete(context.Background(), pool)).To(Succeed())

// 			Consistently(Object(pool)).
// 				WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(
// 				HaveField("ObjectMeta.Finalizers", ContainElement(ProtectPoolFinalizer)))

// 			deleteClaim("finalizer-pool-test", namespace)

// 			Eventually(Get(pool)).Should(Not(Succeed()))
// 		},
// 			Entry("validates InfobloxIPPool", "InfobloxIPPool"),
// 			Entry("validates GlobalInfobloxIPPool", "GlobalInfobloxIPPool"),
// 		)
// 	})
// })

// func newPool(poolType, generateName, namespace, gateway, subnet, networkView, dnsZone string) pooltypes.InfobloxIPPool {
// 	poolSpec := v1alpha1.InfobloxIPPoolSpec{
// 		InstanceRef: corev1.LocalObjectReference{},
// 		Subnet:      subnet,
// 		NetworkView: networkView,
// 		DNSZone:     dnsZone,
// 	}

// 	switch poolType {
// 	case "InfobloxIPPool":
// 		return &v1alpha1.InfobloxIPPool{
// 			ObjectMeta: metav1.ObjectMeta{GenerateName: generateName, Namespace: namespace},
// 			Spec:       poolSpec,
// 		}
// 	// case "GlobalInfobloxIPPool":
// 	// 	return &v1alpha2.GlobalInfobloxIPPool{
// 	// 		ObjectMeta: metav1.ObjectMeta{GenerateName: generateName},
// 	// 		Spec:       poolSpec,
// 	// 	}
// 	default:
// 		Fail("Unknown pool type")
// 	}

// 	return nil
// }
