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

package webhooks

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const ipamAPIVersion = "ipam.cluster.x-k8s.io/v1beta1"

func TestCreatingPool(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(ipamv1.AddToScheme(scheme)).To(Succeed())

	namespacedPool := &v1alpha1.InfobloxIPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pool",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.InfobloxIPPoolSpec{
			InstanceRef: corev1.LocalObjectReference{Name: "test-instance"},
			Subnets:     []v1alpha1.Subnet{{CIDR: "192.168.1.0/24", Gateway: "192.168.1.1"}},
		},
	}

	ips := []client.Object{
		createIP("address00", "192.168.1.2", namespacedPool),
		createIP("address01", "192.168.1.3", namespacedPool),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ips...).
		WithIndex(&ipamv1.IPAddress{}, index.IPAddressPoolRefCombinedField, index.IPAddressByCombinedPoolRef).
		Build()

	webhook := InfobloxIPPool{
		Client: fakeClient,
	}

	oldNamespacedPool := namespacedPool.DeepCopyObject()
	namespacedPool.Spec.Subnets = []v1alpha1.Subnet{{CIDR: "192.168.2.0/24", Gateway: "192.168.2.1"}}

	_, err := webhook.ValidateUpdate(ctx, oldNamespacedPool, namespacedPool)
	g.Expect(err).ToNot(HaveOccurred(), "should not allow removing in use IPs from addresses field in pool")
}

func TestPoolDeletionWithExistingIPAddresses(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(ipamv1.AddToScheme(scheme)).To(Succeed())

	namespacedPool := &v1alpha1.InfobloxIPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pool",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.InfobloxIPPoolSpec{
			InstanceRef: corev1.LocalObjectReference{Name: "test-instance"},
			Subnets:     []v1alpha1.Subnet{{CIDR: "192.168.1.0/24", Gateway: "192.168.1.1"}},
		},
	}

	ips := []client.Object{
		createIP("address00", "192.168.1.2", namespacedPool),
		createIP("address01", "192.168.1.3", namespacedPool),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ips...).
		WithIndex(&ipamv1.IPAddress{}, index.IPAddressPoolRefCombinedField, index.IPAddressByCombinedPoolRef).
		Build()

	webhook := InfobloxIPPool{
		Client: fakeClient,
	}

	_, err := webhook.ValidateDelete(ctx, namespacedPool)
	g.Expect(err).To(HaveOccurred(), "should not allow deletion when claims exist")

	g.Expect(fakeClient.DeleteAllOf(ctx, &ipamv1.IPAddress{})).To(Succeed())

	_, err = webhook.ValidateDelete(ctx, namespacedPool)
	g.Expect(err).ToNot(HaveOccurred(), "should allow deletion when no claims exist")
}

func TestPoolDeletionWithExistingIPAddressesAndDeletionSkipAnnotation(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(ipamv1.AddToScheme(scheme)).To(Succeed())

	namespacedPool := &v1alpha1.InfobloxIPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pool",
			Namespace: "test-namespace",
			Annotations: map[string]string{
				SkipValidateDeleteWebhookAnnotation: "",
			},
		},
		Spec: v1alpha1.InfobloxIPPoolSpec{
			InstanceRef: corev1.LocalObjectReference{Name: "test-instance"},
			Subnets:     []v1alpha1.Subnet{{CIDR: "192.168.1.0/24", Gateway: "192.168.1.1"}},
		},
	}

	ips := []client.Object{
		createIP("address00", "192.168.1.2", namespacedPool),
		createIP("address01", "192.168.1.3", namespacedPool),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ips...).
		WithIndex(&ipamv1.IPAddress{}, index.IPAddressPoolRefCombinedField, index.IPAddressByCombinedPoolRef).
		Build()

	webhook := InfobloxIPPool{
		Client: fakeClient,
	}

	_, err := webhook.ValidateDelete(ctx, namespacedPool)
	g.Expect(err).ToNot(HaveOccurred(), "should not allow deletion when claims exist")

	g.Expect(fakeClient.DeleteAllOf(ctx, &ipamv1.IPAddress{})).To(Succeed())
}

func TestUpdatingPool(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(ipamv1.AddToScheme(scheme)).To(Succeed())

	namespacedPool := &v1alpha1.InfobloxIPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pool",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.InfobloxIPPoolSpec{
			InstanceRef: corev1.LocalObjectReference{Name: "test-instance"},
			Subnets:     []v1alpha1.Subnet{{CIDR: "192.168.1.0/24", Gateway: "192.168.1.1"}},
		},
	}

	ips := []client.Object{
		createIP("address00", "192.168.1.2", namespacedPool),
		createIP("address01", "192.168.1.3", namespacedPool),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ips...).
		WithIndex(&ipamv1.IPAddress{}, index.IPAddressPoolRefCombinedField, index.IPAddressByCombinedPoolRef).
		Build()

	webhook := InfobloxIPPool{
		Client: fakeClient,
	}

	oldNamespacedPool := namespacedPool.DeepCopyObject()
	namespacedPool.Spec.Subnets = []v1alpha1.Subnet{{CIDR: "192.168.2.0/24", Gateway: "192.168.2.1"}}

	_, err := webhook.ValidateUpdate(ctx, oldNamespacedPool, namespacedPool)
	g.Expect(err).ToNot(HaveOccurred(), "should not allow removing in use IPs from addresses field in pool")
}

type invalidScenarioTest struct {
	testcase      string
	spec          v1alpha1.InfobloxIPPoolSpec
	expectedError string
}

func TestInvalidScenarios(t *testing.T) {
	tests := []invalidScenarioTest{
		{
			testcase: "addresses must be set",
			spec: v1alpha1.InfobloxIPPoolSpec{
				Subnets:     []v1alpha1.Subnet{},
				InstanceRef: corev1.LocalObjectReference{Name: "test-instance"},
			},
			expectedError: "subnets is required",
		},
		{
			testcase: "InstanceRef must be set",
			spec: v1alpha1.InfobloxIPPoolSpec{
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.0.0.0/30", Gateway: "10.0.0.1"}},
				InstanceRef: corev1.LocalObjectReference{},
			},
			expectedError: "InstanceRef.Name is required",
		},
		{
			testcase: "invalid subnet should not be allowed",
			spec: v1alpha1.InfobloxIPPoolSpec{
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.0.0.3/30", Gateway: "10.0.0.1"}},
				InstanceRef: corev1.LocalObjectReference{Name: "test-instance"},
			},
			expectedError: "is not a valid CIDR",
		},
		{
			testcase: "invalid gateway should not be allowed",
			spec: v1alpha1.InfobloxIPPoolSpec{
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.0.0.3/30", Gateway: "10.0.0.999"}},
				InstanceRef: corev1.LocalObjectReference{Name: "test-instance"},
			},
			expectedError: "is not a valid IP address",
		},
		{
			testcase: "IPv4 subnet and IPv6 gateway should not be allowed",
			spec: v1alpha1.InfobloxIPPoolSpec{
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.0.0.3/30", Gateway: "2001:db8::1"}},
				InstanceRef: corev1.LocalObjectReference{Name: "test-instance"},
			},
			expectedError: "CIDR and gateway are mixed IPv4 and IPv6 addresses",
		},
		{
			testcase: "IPv6 subnet and IPv4 gateway should not be allowed",
			spec: v1alpha1.InfobloxIPPoolSpec{
				Subnets:     []v1alpha1.Subnet{{CIDR: "2001:db8::0/64", Gateway: "10.0.0.1"}},
				InstanceRef: corev1.LocalObjectReference{Name: "test-instance"},
			},
			expectedError: "CIDR and gateway are mixed IPv4 and IPv6 addresses",
		},
	}
	for _, tt := range tests {
		namespacedPool := &v1alpha1.InfobloxIPPool{Spec: tt.spec}

		g := NewWithT(t)
		scheme := runtime.NewScheme()
		g.Expect(ipamv1.AddToScheme(scheme)).To(Succeed())

		webhook := InfobloxIPPool{
			Client: fake.NewClientBuilder().
				WithScheme(scheme).
				WithIndex(&ipamv1.IPAddress{}, index.IPAddressPoolRefCombinedField, index.IPAddressByCombinedPoolRef).
				Build(),
		}
		runInvalidScenarioTests(t, tt, namespacedPool, webhook)
	}
}

func runInvalidScenarioTests(t *testing.T, tt invalidScenarioTest, pool *v1alpha1.InfobloxIPPool, webhook InfobloxIPPool) {
	t.Helper()
	t.Run(tt.testcase, func(t *testing.T) {
		t.Run("create", func(t *testing.T) {
			t.Helper()

			g := NewWithT(t)
			g.Expect(testCreate(context.Background(), pool, &webhook)).
				To(MatchError(ContainSubstring(tt.expectedError)))
		})
		t.Run("update", func(t *testing.T) {
			t.Helper()

			g := NewWithT(t)
			g.Expect(testUpdate(context.Background(), pool, &webhook)).
				To(MatchError(ContainSubstring(tt.expectedError)))
		})
		t.Run("delete", func(t *testing.T) {
			t.Helper()

			g := NewWithT(t)
			g.Expect(testDelete(context.Background(), pool, &webhook)).
				To(Succeed())
		})
	})
}

func testCreate(ctx context.Context, obj runtime.Object, webhook customDefaulterValidator) error {
	createCopy := obj.DeepCopyObject()
	if err := webhook.Default(ctx, createCopy); err != nil {
		return err
	}
	_, err := webhook.ValidateCreate(ctx, createCopy)
	return err
}

func testDelete(ctx context.Context, obj runtime.Object, webhook customDefaulterValidator) error {
	deleteCopy := obj.DeepCopyObject()
	if err := webhook.Default(ctx, deleteCopy); err != nil {
		return err
	}
	_, err := webhook.ValidateDelete(ctx, deleteCopy)
	return err
}

func testUpdate(ctx context.Context, obj runtime.Object, webhook customDefaulterValidator) error {
	updateCopy := obj.DeepCopyObject()
	updatedCopy := obj.DeepCopyObject()
	err := webhook.Default(ctx, updateCopy)
	if err != nil {
		return err
	}
	err = webhook.Default(ctx, updatedCopy)
	if err != nil {
		return err
	}
	_, err = webhook.ValidateUpdate(ctx, updateCopy, updatedCopy)
	return err
}

func createIP(name string, ip string, pool *v1alpha1.InfobloxIPPool) *ipamv1.IPAddress {
	return &ipamv1.IPAddress{
		TypeMeta: metav1.TypeMeta{
			Kind:       "IPAddress",
			APIVersion: ipamAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: pool.Namespace,
		},
		Spec: ipamv1.IPAddressSpec{
			PoolRef: ipamv1.IPPoolReference{
				APIGroup: pool.GetObjectKind().GroupVersionKind().Group,
				Kind:     pool.GetObjectKind().GroupVersionKind().Kind,
				Name:     pool.GetName(),
			},
			Address: ip,
		},
	}
}
