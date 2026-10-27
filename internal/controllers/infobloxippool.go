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
	"fmt"
	"net/netip"
	"time"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/poolutil"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// ProtectPoolFinalizer is used to prevent deletion of a Pool object while its addresses have not been deleted.
	ProtectPoolFinalizer = "ipam.cluster.x-k8s.io/ProtectPool"

	// PoolDeletionRetry is how often a pool that is waiting for its claims and addresses to be
	// deleted is checked again. Nothing wakes the reconciler when the last one disappears, so this
	// is also the floor for how long a pool lingers in Terminating afterwards.
	PoolDeletionRetry = 10 * time.Second
	defaultDNSView    = "default"
)

// InfobloxIPPoolReconciler reconciles a InfobloxIPPool object.
type InfobloxIPPoolReconciler struct {
	Client client.Client
	// APIReader reads straight from the API server, bypassing the client cache.
	APIReader client.Reader
	Scheme    *runtime.Scheme

	OperatorNamespace     string
	GetInfobloxClientFunc infoblox.GetClientFunc
}

//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=infobloxippools,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=infobloxippools/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=infobloxippools/finalizers,verbs=update

// SetupWithManager sets up the controller with the Manager.
func (r *InfobloxIPPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&v1alpha1.InfobloxIPPool{}).
		Complete(r)
}

// Reconcile an InfobloxIPPool.
func (r *InfobloxIPPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	pool := &v1alpha1.InfobloxIPPool{}
	if err := r.Client.Get(ctx, req.NamespacedName, pool); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// setup patch helper
	patchHelper, err := patch.NewHelper(pool, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if err := patchHelper.Patch(ctx, pool, patch.WithOwnedConditions{}); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
	}()

	// add finalizer
	isMarkedForDeletion := pool.GetDeletionTimestamp() != nil
	if !isMarkedForDeletion && controllerutil.AddFinalizer(pool, ProtectPoolFinalizer) {
		return ctrl.Result{}, nil
	}

	if isMarkedForDeletion {
		return r.reconcileDelete(ctx, pool)
	}

	return ctrl.Result{}, r.reconcile(ctx, pool)
}

// reconcileDelete holds a pool back for as long as claims still reference it.
//
// Waiting for claims to drain is a normal state during a teardown rather than a failure, so it is
// reported as a condition and a requeue while any still exist.
func (r *InfobloxIPPoolReconciler) reconcileDelete(ctx context.Context, pool *v1alpha1.InfobloxIPPool) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	poolTypeRef := ipamv1.IPPoolReference{
		APIGroup: v1alpha1.GroupVersion.Group,
		Kind:     v1alpha1.InfobloxIPPoolKind,
		Name:     pool.GetName(),
	}
	inUseClaims, err := poolutil.ListClaimsReferencingPool(ctx, r.Client, pool.GetNamespace(), poolTypeRef)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(inUseClaims) == 0 {
		// The client might be cached and the cache might be stale and we may not see all claims that reference the pool.
		// So we double-check with a direct API call if none are found in cache.
		inUseClaims, err = poolutil.ListClaimsReferencingPoolUnindexed(ctx, r.APIReader, pool.GetNamespace(), poolTypeRef)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to confirm that no claims reference the pool: %w", err)
		}
	}
	if len(inUseClaims) > 0 {
		message := fmt.Sprintf("waiting for %d IPAddressClaim(s) referencing this pool to be deleted", len(inUseClaims))
		logger.Info(message)
		conditions.Set(pool, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ClaimsPendingDeletionReason,
			Message: message,
		})
		return ctrl.Result{RequeueAfter: PoolDeletionRetry}, nil
	}

	// Claims are gone so the pool can be removed.
	controllerutil.RemoveFinalizer(pool, ProtectPoolFinalizer)
	return ctrl.Result{}, nil
}

func (r *InfobloxIPPoolReconciler) reconcile(ctx context.Context, pool *v1alpha1.InfobloxIPPool) error {
	logger := log.FromContext(ctx)
	ibclient, err := GetInfobloxClientForInstance(ctx, r.Client, pool.Spec.InstanceRef.Name, r.OperatorNamespace, r.GetInfobloxClientFunc)
	if err != nil {
		logger.Error(errInfobloxClientCreationFailed, "client creation failed", "instance", pool.Spec.InstanceRef.Name, "cause", err)
		conditions.Set(pool, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.AuthenticationFailedReason,
			Message: fmt.Sprintf("client creation failed for instance %q; see controller logs", pool.Spec.InstanceRef.Name),
		})
		return err
	}

	if pool.Spec.NetworkView == "" {
		pool.Spec.NetworkView = ibclient.GetHostConfig().DefaultNetworkView
	}

	if ok, err := ibclient.CheckNetworkViewExists(pool.Spec.NetworkView); err != nil || !ok {
		return markFailedInfobloxRequest(pool, err, v1alpha1.NetworkViewNotFoundReason,
			fmt.Sprintf("network view %q", pool.Spec.NetworkView))
	}

	// Check DNS view if specified
	dnsView := determineDNSView(pool.Spec.DNSView, ibclient.GetHostConfig().DefaultDNSView, pool.Spec.NetworkView)
	if dnsView != "" {
		if ok, err := ibclient.CheckDNSViewExists(dnsView); err != nil || !ok {
			return markFailedInfobloxRequest(pool, err, v1alpha1.DNSViewNotFoundReason,
				fmt.Sprintf("DNS view %q", dnsView))
		}
	}

	for _, sub := range pool.Spec.Subnets {
		subnet, err := netip.ParsePrefix(sub.CIDR)
		if err != nil {
			// We won't set a condition here since this should be caught by validation
			return fmt.Errorf("failed to parse subnet: %w", err)
		}
		if ok, err := ibclient.CheckNetworkExists(pool.Spec.NetworkView, subnet); err != nil || !ok {
			return markFailedInfobloxRequest(pool, err, v1alpha1.NetworkNotFoundReason,
				fmt.Sprintf("network %q in view %q", subnet, pool.Spec.NetworkView))
		}
	}

	conditions.Set(pool, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  v1alpha1.ReadyReason,
		Message: "pool is ready",
	})
	return nil
}

// determineDNSView determines the DNS view to use based on the priority order:
// 1. Pool.spec.dnsView (if set)
// 2. Instance.spec.defaultDnsView (if not set on pool but set on instance)
// 3. Derived from networkView (if neither is set).
func determineDNSView(poolDNSView, instanceDefaultDNSView, networkView string) string {
	if poolDNSView != "" {
		return poolDNSView
	}
	if instanceDefaultDNSView != "" {
		return instanceDefaultDNSView
	}
	// fallback to old behavior: derive DNS view from networkView
	if networkView == "" || networkView == defaultDNSView {
		return defaultDNSView
	}
	return defaultDNSView + "." + networkView
}
