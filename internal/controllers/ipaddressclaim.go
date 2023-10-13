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
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1alpha1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	ipampredicates "github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/predicates"
	"sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
)

var (
	getInfobloxClientForInstanceFunc = getInfobloxClientForInstance
)

const (
	// ReleaseAddressFinalizer is used to release an IP address before cleaning up the claim.
	ReleaseAddressFinalizer = "ipam.cluster.x-k8s.io/ReleaseAddress"

	// ProtectAddressFinalizer is used to prevent deletion of an IPAddress object while its claim is not deleted.
	ProtectAddressFinalizer = "ipam.cluster.x-k8s.io/ProtectAddress"
)

// InfobloxProviderIntegration reconciles a InfobloxIPPool object.
type InfobloxProviderIntegration struct {
	NewInfobloxClientFunc func(config infoblox.Config) (infoblox.Client, error)
}

var _ ipamutil.ProviderIntegration = &InfobloxProviderIntegration{}

type InfobloxClaimHandler struct {
	client.Client
	claim                 *ipamv1.IPAddressClaim
	pool                  *v1alpha1.InfobloxIPPool
	newInfobloxClientFunc func(config infoblox.Config) (infoblox.Client, error)
	ibclient              infoblox.Client
}

var _ ipamutil.ClaimHandler = &InfobloxClaimHandler{}

// SetupWithManager sets up the controller with the Manager.
func (r *InfobloxProviderIntegration) SetupWithManager(ctx context.Context, b *ctrl.Builder) error {
	b.
		For(&ipamv1.IPAddressClaim{}, builder.WithPredicates(
			ipampredicates.ClaimReferencesPoolKind(metav1.GroupKind{
				Group: v1alpha1.GroupVersion.Group,
				Kind:  "InfobloxIPPool",
			}),
		)).
		WithOptions(controller.Options{
			// To avoid race conditions when allocating IP Addresses, we explicitly set this to 1
			MaxConcurrentReconciles: 1,
		}).
		Owns(&ipamv1.IPAddress{}, builder.WithPredicates(
			ipampredicates.AddressReferencesPoolKind(metav1.GroupKind{
				Group: v1alpha1.GroupVersion.Group,
				Kind:  "InfobloxIPPool",
			}),
		))
	return nil
}

func (r *InfobloxProviderIntegration) ClaimHandlerFor(cl client.Client, claim *ipamv1.IPAddressClaim) ipamutil.ClaimHandler {
	return &InfobloxClaimHandler{
		Client:                cl,
		claim:                 claim,
		newInfobloxClientFunc: r.NewInfobloxClientFunc,
	}
}

//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims/status;ipaddresses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims/status;ipaddresses/finalizers,verbs=update

func (h *InfobloxClaimHandler) FetchPool(ctx context.Context) (*ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	h.pool = &v1alpha1.InfobloxIPPool{}
	log.Info("InfobloxClaimHandler FetchPool 1")
	if err := h.Client.Get(ctx, types.NamespacedName{Namespace: h.claim.Namespace, Name: h.claim.Spec.PoolRef.Name}, h.pool); err != nil && !apierrors.IsNotFound(err) {
		log.Info("InfobloxClaimHandler FetchPool  - failed to fetch")
		return nil, errors.Wrap(err, "failed to fetch pool")
	}
	log.Info("InfobloxClaimHandler FetchPool 2")
	if h.pool == nil {
		log.Info("pool not found")
		err := errors.New("pool not found")
		log.Error(err, "the referenced pool could not be found")
		return nil, nil
	}

	log.Info("InfobloxClaimHandler FetchPool 3")

	// TODO: ensure pool is ready

	var err error

	log.Info("Instance info", "name", h.pool.Spec.InstanceRef.Name, "namespace", h.pool.Namespace)
	log.Info("pool annotations", "annotations", h.pool.Annotations)
	h.ibclient, err = getInfobloxClientForInstanceFunc(ctx, h.Client, h.pool.Spec.InstanceRef.Name, h.pool.Namespace, h.newInfobloxClientFunc)
	if err != nil {
		log.Error(err, "failed to get infoblox client")
		return nil, fmt.Errorf("failed to get infoblox client: %w", err)
	}

	return nil, nil
}

func (h *InfobloxClaimHandler) EnsureAddress(ctx context.Context, address *ipamv1.IPAddress) (*ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("EnsureAddress - ParsePrefix")
	subnet, err := netip.ParsePrefix(h.pool.Spec.Subnet)

	if err != nil {
		// We won't set a condition here since this should be caught by validation
		logger.Info("EnsureAddress - failed to parse subnet")
		return nil, fmt.Errorf("failed to parse subnet: %w", err)
	}

	logger.Info("EnsureAddress - GetOrAllocateAddress")
	ipaddr, err := h.ibclient.GetOrAllocateAddress(h.pool.Spec.NetworkView, subnet, "", h.pool.Spec.DNSZone)
	if err != nil {
		logger.Error(err, "EnsureAddress - GetOrAllocateAddress - error")
		conditions.MarkFalse(h.claim,
			v1beta1.ReadyCondition,
			v1alpha1.InfobloxAddressAllocationFailedReason,
			v1beta1.ConditionSeverityError,
			"could not allocate address: %s", err)
		return nil, fmt.Errorf("could not allocate address: %w", err)
	}

	logger.Info("EnsureAddress - set spec")
	address.Spec.Address = ipaddr.String()

	if address.Spec.Prefix, err = strconv.Atoi(strings.Split(h.pool.Spec.Subnet, "/")[1]); err != nil {
		logger.Error(err, "EnsureAddress - error - could not aprse address")
		return nil, fmt.Errorf("could not parse address: %w", err)
	}

	logger.Info("EnsureAddress - end")
	return nil, nil
}

func (h *InfobloxClaimHandler) ReleaseAddress() (*ctrl.Result, error) {
	subnet, err := netip.ParsePrefix(h.pool.Spec.Subnet)
	if err != nil {
		// We won't set a condition here since this should be caught by validation
		return nil, fmt.Errorf("failed to parse subnet: %w", err)
	}
	err = h.ibclient.ReleaseAddress(h.pool.Spec.NetworkView, subnet, "")
	if err != nil {
		return nil, fmt.Errorf("failed to release address: %w", err)
	}

	return &ctrl.Result{}, nil
}

func (h *InfobloxClaimHandler) GetPool() client.Object {
	logger := log.FromContext(context.TODO())
	logger.Info("GetPool", "value", h.pool.Annotations)

	return h.pool
}
