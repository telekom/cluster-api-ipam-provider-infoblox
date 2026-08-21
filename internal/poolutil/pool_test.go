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

package poolutil

import (
	"context"
	"testing"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNamespace = "default"

func TestListAddressesInUseIncludesObjectsWithoutTypeMeta(t *testing.T) {
	poolRef := testPoolRef()
	address := &ipamv1.IPAddress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "address",
			Namespace: testNamespace,
		},
		Spec: ipamv1.IPAddressSpec{
			PoolRef: poolRef,
			Address: "192.0.2.10",
		},
	}

	c := newTestClient(t, address)

	addresses, err := ListAddressesInUse(context.Background(), c, testNamespace, poolRef)
	if err != nil {
		t.Fatalf("ListAddressesInUse returned error: %v", err)
	}
	if len(addresses) != 1 {
		t.Fatalf("len(addresses) = %d, want 1", len(addresses))
	}
	if addresses[0].Name != address.Name {
		t.Fatalf("address name = %q, want %q", addresses[0].Name, address.Name)
	}
}

func TestListAddressesInUseIgnoresSameKindNameDifferentAPIGroup(t *testing.T) {
	poolRef := testPoolRef()
	otherPoolRef := poolRef
	otherPoolRef.APIGroup = "other.example.com"
	address := &ipamv1.IPAddress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "address",
			Namespace: testNamespace,
		},
		Spec: ipamv1.IPAddressSpec{
			PoolRef: poolRef,
			Address: "192.0.2.10",
		},
	}
	otherAddress := &ipamv1.IPAddress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-address",
			Namespace: testNamespace,
		},
		Spec: ipamv1.IPAddressSpec{
			PoolRef: otherPoolRef,
			Address: "192.0.2.11",
		},
	}

	c := newTestClient(t, address, otherAddress)

	addresses, err := ListAddressesInUse(context.Background(), c, testNamespace, poolRef)
	if err != nil {
		t.Fatalf("ListAddressesInUse returned error: %v", err)
	}
	if len(addresses) != 1 {
		t.Fatalf("len(addresses) = %d, want 1", len(addresses))
	}
	if addresses[0].Name != address.Name {
		t.Fatalf("address name = %q, want %q", addresses[0].Name, address.Name)
	}
}

func TestListAddressesInUseIncludesLegacyEmptyAPIGroupPoolRef(t *testing.T) {
	poolRef := testPoolRef()
	legacyPoolRef := poolRef
	legacyPoolRef.APIGroup = ""
	address := &ipamv1.IPAddress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "address",
			Namespace: testNamespace,
		},
		Spec: ipamv1.IPAddressSpec{
			PoolRef: legacyPoolRef,
			Address: "192.0.2.10",
		},
	}

	c := newTestClient(t, address)

	addresses, err := ListAddressesInUse(context.Background(), c, testNamespace, poolRef)
	if err != nil {
		t.Fatalf("ListAddressesInUse returned error: %v", err)
	}
	if len(addresses) != 1 {
		t.Fatalf("len(addresses) = %d, want 1", len(addresses))
	}
	if addresses[0].Name != address.Name {
		t.Fatalf("address name = %q, want %q", addresses[0].Name, address.Name)
	}
}

func TestListClaimsReferencingPoolIncludesObjectsWithoutTypeMeta(t *testing.T) {
	poolRef := testPoolRef()
	claim := &ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "claim",
			Namespace: testNamespace,
		},
		Spec: ipamv1.IPAddressClaimSpec{
			PoolRef: poolRef,
		},
	}

	c := newTestClient(t, claim)

	claims, err := ListClaimsReferencingPool(context.Background(), c, testNamespace, poolRef)
	if err != nil {
		t.Fatalf("ListClaimsReferencingPool returned error: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("len(claims) = %d, want 1", len(claims))
	}
	if claims[0].Name != claim.Name {
		t.Fatalf("claim name = %q, want %q", claims[0].Name, claim.Name)
	}
}

func TestListClaimsReferencingPoolIgnoresSameKindNameDifferentAPIGroup(t *testing.T) {
	poolRef := testPoolRef()
	otherPoolRef := poolRef
	otherPoolRef.APIGroup = "other.example.com"
	claim := &ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "claim",
			Namespace: testNamespace,
		},
		Spec: ipamv1.IPAddressClaimSpec{
			PoolRef: poolRef,
		},
	}
	otherClaim := &ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-claim",
			Namespace: testNamespace,
		},
		Spec: ipamv1.IPAddressClaimSpec{
			PoolRef: otherPoolRef,
		},
	}

	c := newTestClient(t, claim, otherClaim)

	claims, err := ListClaimsReferencingPool(context.Background(), c, testNamespace, poolRef)
	if err != nil {
		t.Fatalf("ListClaimsReferencingPool returned error: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("len(claims) = %d, want 1", len(claims))
	}
	if claims[0].Name != claim.Name {
		t.Fatalf("claim name = %q, want %q", claims[0].Name, claim.Name)
	}
}

func TestListClaimsReferencingPoolIncludesLegacyEmptyAPIGroupPoolRef(t *testing.T) {
	poolRef := testPoolRef()
	legacyPoolRef := poolRef
	legacyPoolRef.APIGroup = ""
	claim := &ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "claim",
			Namespace: testNamespace,
		},
		Spec: ipamv1.IPAddressClaimSpec{
			PoolRef: legacyPoolRef,
		},
	}

	c := newTestClient(t, claim)

	claims, err := ListClaimsReferencingPool(context.Background(), c, testNamespace, poolRef)
	if err != nil {
		t.Fatalf("ListClaimsReferencingPool returned error: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("len(claims) = %d, want 1", len(claims))
	}
	if claims[0].Name != claim.Name {
		t.Fatalf("claim name = %q, want %q", claims[0].Name, claim.Name)
	}
}

func newTestClient(t *testing.T, objs ...runtime.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := ipamv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add IPAM scheme: %v", err)
	}

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithIndex(&ipamv1.IPAddress{}, index.IPAddressPoolRefCombinedField, index.IPAddressByCombinedPoolRef).
		WithIndex(&ipamv1.IPAddressClaim{}, index.IPAddressClaimPoolRefCombinedField, func(o client.Object) []string {
			claim, ok := o.(*ipamv1.IPAddressClaim)
			if !ok {
				return nil
			}
			return index.IPPoolRefValues(claim.Spec.PoolRef)
		}).
		Build()
}

func testPoolRef() ipamv1.IPPoolReference {
	return ipamv1.IPPoolReference{
		APIGroup: "ipam.cluster.x-k8s.io",
		Kind:     "InfobloxIPPool",
		Name:     "pool",
	}
}
