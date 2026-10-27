/*
Copyright 2026 The Kubernetes Authors.

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

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox/ibmock"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEnsureAddressKeepsExistingAddressWhenSubnetOrderChanges(t *testing.T) {
	ctx := context.Background()
	scheme := allocationTestScheme(t)
	pool := allocationTestPool("1")
	pool.Spec.Subnets[0], pool.Spec.Subnets[1] = pool.Spec.Subnets[1], pool.Spec.Subnets[0]
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build()
	claim := &ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: pool.Namespace},
	}
	ibclient := ibmock.NewMockClient(gomock.NewController(t))
	ibclient.EXPECT().GetHostConfig().Return(&infoblox.HostConfig{}).AnyTimes()
	ibclient.EXPECT().GetOrAllocateAddress(
		"", "default", netip.MustParsePrefix("10.0.1.0/24"), gomock.Any(), gomock.Any(), gomock.Any(),
	).Return(netip.MustParseAddr("10.0.1.10"), nil)

	handler := &InfobloxClaimHandler{
		Client:   client,
		claim:    claim,
		pool:     pool.DeepCopy(),
		ibclient: ibclient,
	}
	address := &ipamv1.IPAddress{
		Spec: ipamv1.IPAddressSpec{Address: "10.0.1.10"},
	}

	_, err := handler.EnsureAddress(ctx, address)
	if err != nil {
		t.Fatalf("EnsureAddress returned error: %v", err)
	}
	if address.Spec.Address != "10.0.1.10" {
		t.Fatalf("address changed to %q", address.Spec.Address)
	}
	if address.Spec.Gateway != "10.0.1.1" {
		t.Fatalf("gateway = %q, want 10.0.1.1", address.Spec.Gateway)
	}
}

func TestFetchPoolNormalizesPoolGVKForLegacyClaimReferences(t *testing.T) {
	ctx := context.Background()
	scheme := allocationTestScheme(t)
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	pool := allocationTestPool("1")
	conditions.Set(pool, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionTrue,
	})
	instance := &v1alpha1.InfobloxInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "instance"},
		Spec: v1alpha1.InfobloxInstanceSpec{
			CredentialsSecretRef: v1alpha1.CredentialsReferece{Name: "credentials"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "operator"},
		Data: map[string][]byte{
			"username": []byte("user"),
			"password": []byte("pass"),
		},
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, instance, secret).Build()
	claim := &ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: pool.Namespace},
		Spec: ipamv1.IPAddressClaimSpec{
			PoolRef: ipamv1.IPPoolReference{
				Kind: "InfobloxIPPool",
				Name: pool.Name,
			},
		},
	}
	handler := &InfobloxClaimHandler{
		Client: apiClient,
		claim:  claim,
		getInfobloxClientFunc: func(_ string, _ string, _ types.UID, _ string, _ infoblox.Config) (infoblox.Client, error) {
			return nil, nil
		},
		getInfobloxClientForInstance: func(context.Context, client.Reader, string, string, infoblox.GetClientFunc) (infoblox.Client, error) {
			return nil, nil
		},
		operatorNamespace: "operator",
	}

	_, _, err := handler.FetchPool(ctx)
	if err != nil {
		t.Fatalf("FetchPool returned error: %v", err)
	}
	if got, want := handler.pool.GroupVersionKind(), v1alpha1.GroupVersion.WithKind("InfobloxIPPool"); got != want {
		t.Fatalf("pool GVK = %s, want %s", got, want)
	}
}

func allocationTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add Infoblox scheme: %v", err)
	}
	if err := ipamv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add IPAM scheme: %v", err)
	}
	return scheme
}

func allocationTestPool(resourceVersion string) *v1alpha1.InfobloxIPPool {
	return &v1alpha1.InfobloxIPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "pool",
			Namespace:       "default",
			ResourceVersion: resourceVersion,
		},
		Spec: v1alpha1.InfobloxIPPoolSpec{
			InstanceRef: v1alpha1.InstanceReference{Name: "instance"},
			Subnets: []v1alpha1.Subnet{
				{CIDR: "10.0.0.0/24", Gateway: "10.0.0.1"},
				{CIDR: "10.0.1.0/24", Gateway: "10.0.1.1"},
			},
		},
	}
}
