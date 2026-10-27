/*
Copyright 2026 Deutsche Telekom AG.

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
	"testing"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestInfobloxIPPoolReconcileAllowsOrphanedIPAddressDuringDeletion(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add Infoblox scheme: %v", err)
	}
	if err := ipamv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add IPAM scheme: %v", err)
	}

	deletionTime := metav1.Now()
	pool := &v1alpha1.InfobloxIPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pool",
			Namespace:         "default",
			Finalizers:        []string{ProtectPoolFinalizer},
			DeletionTimestamp: &deletionTime,
		},
		Spec: v1alpha1.InfobloxIPPoolSpec{
			InstanceRef: v1alpha1.InstanceReference{Name: "instance"},
			Subnets:     []v1alpha1.Subnet{{CIDR: "192.0.2.0/24", Gateway: "192.0.2.1"}},
		},
	}
	address := &ipamv1.IPAddress{
		ObjectMeta: metav1.ObjectMeta{Name: "address", Namespace: pool.Namespace},
		Spec: ipamv1.IPAddressSpec{
			PoolRef: ipamv1.IPPoolReference{Kind: "InfobloxIPPool", Name: pool.Name},
			Address: "192.0.2.10",
		},
	}

	c := legacyPoolTestClient(scheme, pool, address)
	reconciler := &InfobloxIPPoolReconciler{Client: c, APIReader: c, Scheme: scheme}
	res, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: pool.Namespace,
		Name:      pool.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want no error while waiting for deletion", err)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want no requeue", res)
	}
	assertPoolFinalizerRemoved(t, c, pool)
}

func TestInfobloxIPPoolReconcileKeepsFinalizerForLegacyEmptyAPIGroupClaim(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add Infoblox scheme: %v", err)
	}
	if err := ipamv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add IPAM scheme: %v", err)
	}

	deletionTime := metav1.Now()
	pool := &v1alpha1.InfobloxIPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pool",
			Namespace:         "default",
			Finalizers:        []string{ProtectPoolFinalizer},
			DeletionTimestamp: &deletionTime,
		},
		Spec: v1alpha1.InfobloxIPPoolSpec{
			InstanceRef: v1alpha1.InstanceReference{Name: "instance"},
			Subnets:     []v1alpha1.Subnet{{CIDR: "192.0.2.0/24", Gateway: "192.0.2.1"}},
		},
	}
	claim := &ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: pool.Namespace},
		Spec: ipamv1.IPAddressClaimSpec{PoolRef: ipamv1.IPPoolReference{
			Kind: "InfobloxIPPool",
			Name: pool.Name,
		}},
	}

	c := legacyPoolTestClient(scheme, pool, claim)
	reconciler := &InfobloxIPPoolReconciler{Client: c, APIReader: c, Scheme: scheme}
	res, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: pool.Namespace,
		Name:      pool.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want no error while waiting for deletion", err)
	}
	if res.RequeueAfter != PoolDeletionRetry {
		t.Fatalf("Reconcile() result = %#v, want requeue after %s", res, PoolDeletionRetry)
	}
	assertPoolFinalizer(t, c, pool)
}

func legacyPoolTestClient(scheme *runtime.Scheme, pool *v1alpha1.InfobloxIPPool, objects ...runtime.Object) client.Client {
	allObjects := make([]runtime.Object, 0, len(objects)+1)
	allObjects = append(allObjects, pool)
	allObjects = append(allObjects, objects...)
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(allObjects...).
		WithStatusSubresource(&v1alpha1.InfobloxIPPool{}).
		WithIndex(&ipamv1.IPAddress{}, index.IPAddressPoolRefCombinedField, index.IPAddressByCombinedPoolRef).
		WithIndex(&ipamv1.IPAddressClaim{}, index.IPAddressClaimPoolRefCombinedField, func(object client.Object) []string {
			claim, ok := object.(*ipamv1.IPAddressClaim)
			if !ok {
				return nil
			}
			return index.IPPoolRefValues(claim.Spec.PoolRef)
		}).
		Build()
}

func assertPoolFinalizer(t *testing.T, c client.Client, pool *v1alpha1.InfobloxIPPool) {
	t.Helper()
	currentPool := &v1alpha1.InfobloxIPPool{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: pool.Namespace, Name: pool.Name}, currentPool); err != nil {
		t.Fatalf("get pool: %v", err)
	}
	for _, finalizer := range currentPool.Finalizers {
		if finalizer == ProtectPoolFinalizer {
			return
		}
	}
	t.Fatalf("pool finalizers = %v, want %q retained", currentPool.Finalizers, ProtectPoolFinalizer)
}

func assertPoolFinalizerRemoved(t *testing.T, c client.Client, pool *v1alpha1.InfobloxIPPool) {
	t.Helper()
	currentPool := &v1alpha1.InfobloxIPPool{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: pool.Namespace, Name: pool.Name}, currentPool); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		t.Fatalf("get pool: %v", err)
	}
	for _, finalizer := range currentPool.Finalizers {
		if finalizer == ProtectPoolFinalizer {
			t.Fatalf("pool finalizers = %v, want %q removed", currentPool.Finalizers, ProtectPoolFinalizer)
		}
	}
}
