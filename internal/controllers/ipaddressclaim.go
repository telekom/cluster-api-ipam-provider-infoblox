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
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/hostname"
	"net/netip"
	"strconv"
	"strings"

	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	ipampredicates "github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/predicates"
)

var (
	getInfobloxClientForInstanceFunc = getInfobloxClientForInstance
	newHostnameHandlerFunc           = getHostnameResolver
	hostnameAnnotation               = "ipam.cluster.x-k8s.io/hostname"
)

// InfobloxProviderAdapter reconciles a InfobloxIPPool object.
type InfobloxProviderAdapter struct {
	NewInfobloxClientFunc func(config infoblox.Config) (infoblox.Client, error)
	OperatorNamespace     string
}

var _ ipamutil.ProviderAdapter = &InfobloxProviderAdapter{}

// InfobloxClaimHandler handles infoblox claims.
type InfobloxClaimHandler struct {
	client.Client
	claim                 *ipamv1.IPAddressClaim
	pool                  *v1alpha1.InfobloxIPPool
	newInfobloxClientFunc func(config infoblox.Config) (infoblox.Client, error)
	operatorNamespace     string
	ibclient              infoblox.Client
}

var _ ipamutil.ClaimHandler = &InfobloxClaimHandler{}

// SetupWithManager sets up the controller with the Manager.
func (r *InfobloxProviderAdapter) SetupWithManager(_ context.Context, b *ctrl.Builder) error {
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

// ClaimHandlerFor returns handler for claim.
func (r *InfobloxProviderAdapter) ClaimHandlerFor(cl client.Client, claim *ipamv1.IPAddressClaim) ipamutil.ClaimHandler {
	return &InfobloxClaimHandler{
		Client:                cl,
		claim:                 claim,
		newInfobloxClientFunc: r.NewInfobloxClientFunc,
		operatorNamespace:     r.OperatorNamespace,
	}
}

//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims/status;ipaddresses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims/status;ipaddresses/finalizers,verbs=update

// for resolving hostnames
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metal3datas;metal3machines,verbs=get;list;watch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=vspheremachines;vspherevms,verbs=get;list;watch

// FetchPool fetches pool from cluster.
func (h *InfobloxClaimHandler) FetchPool(ctx context.Context) (client.Object, *ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var err error

	h.pool = &v1alpha1.InfobloxIPPool{}
	if err = h.Client.Get(ctx, types.NamespacedName{Namespace: h.claim.Namespace, Name: h.claim.Spec.PoolRef.Name}, h.pool); err != nil && !apierrors.IsNotFound(err) {
		return nil, nil, errors.Wrap(err, "failed to fetch pool")
	}

	if h.pool == nil {
		err := errors.New("pool not found")
		logger.Error(err, "the referenced pool could not be found")
		return h.pool, nil, nil
	}

	// TODO: ensure pool is ready

	h.ibclient, err = getInfobloxClientForInstanceFunc(ctx, h.Client, h.pool.Spec.InstanceRef.Name, h.operatorNamespace, h.newInfobloxClientFunc)
	if err != nil {
		return h.pool, nil, fmt.Errorf("failed to get infoblox client: %w", err)
	}

	return h.pool, nil, nil
}

// EnsureAddress ensures address.
func (h *InfobloxClaimHandler) EnsureAddress(ctx context.Context, address *ipamv1.IPAddress) (*ctrl.Result, error) {
	var err error

	logger := log.FromContext(ctx)

	hostName, err := h.getHostname(ctx)
	if err != nil {
		return nil, err
	}

	// Since we can't guarantee that resolving the hostname during machine deletion will succeed, we store it as an annotation
	// on the claim, and retrieve it during deletion to delete the infoblox record.
	// We only need to do so when
	if h.pool.Spec.DNSZone != "" {
		if h.claim.Annotations == nil {
			h.claim.Annotations = map[string]string{}
		}
		h.claim.Annotations[hostnameAnnotation] = hostName
	}

	logger = logger.WithValues("hostname", hostName)

	for _, sub := range h.pool.Spec.Subnets {
		var subnet netip.Prefix
		subnet, err = netip.ParsePrefix(sub.CIDR)
		if err != nil {
			// We won't set a condition here since this should be caught by validation
			logger.Error(err, "failed to parse subnet", "subnet", subnet)
			continue
		}

		var ipaddr netip.Addr
		ipaddr, err = h.ibclient.GetOrAllocateAddress(h.pool.Spec.NetworkView, subnet, hostName, h.pool.Spec.DNSZone)
		if err != nil {
			continue
		}

		address.Spec.Address = ipaddr.String()

		if address.Spec.Prefix, err = strconv.Atoi(strings.Split(subnet.String(), "/")[1]); err != nil {
			logger.Error(err, "could determine prefix length", "subnet", subnet.String())
			continue
		}

		address.Spec.Gateway = sub.Gateway

		conditions.MarkTrue(h.claim, clusterv1.ReadyCondition)

		return nil, nil
	}

	if err != nil {
		conditions.MarkFalse(h.claim,
			clusterv1.ReadyCondition,
			v1alpha1.InfobloxAddressAllocationFailedReason,
			clusterv1.ConditionSeverityError,
			"could not allocate address: %s", err)
		return &ctrl.Result{}, fmt.Errorf("unable to ensure address: %w", err)
	}

	return nil, nil
}

// ReleaseAddress releases address.
func (h *InfobloxClaimHandler) ReleaseAddress(ctx context.Context) (*ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var err error

	logger = logger.WithValues("hostname", hostName)

	var subnet netip.Prefix
	for _, sub := range h.pool.Spec.Subnets {
		subnet, err = netip.ParsePrefix(sub.CIDR)
		if err != nil {
			logger.Error(err, "failed to parse subnet", "subnet", sub)
			// We won't set a condition here since this should be caught by validation
			continue
		}

		err = h.ibclient.ReleaseAddress(h.pool.Spec.NetworkView, subnet, hostName)
		if err != nil {
			if _, ok := err.(*ibclient.NotFoundError); !ok {
				logger.Error(err, "failed to release address for host", "hostname", hostName)
			}
			continue
		} else if err == nil {
			logger.Info("released address for host", "hostname", hostName)
		}
	}

	if err != nil {
		if _, ok := err.(*ibclient.NotFoundError); !ok {
			return nil, fmt.Errorf("unable to release address: %w", err)
		}
	}

	return nil, nil
}

// GetPool returns local pool.
func (h *InfobloxClaimHandler) GetPool() client.Object {
	return h.pool
}

func (h *InfobloxClaimHandler) getHostname(ctx context.Context) (string, error) {
	hostName := h.claim.Annotations[hostnameAnnotation]
	if hostName != "" {
		return hostName, nil
	}

	if h.pool.Spec.DNSZone == "" {
		return h.claim.Name, nil
	}

	hostnameHandler, err := newHostnameHandlerFunc(h.Client, h.claim)
	if err != nil {
		return "", fmt.Errorf("failed to create hostname handler: %w", err)
	}

	hn, err := hostnameHandler.GetHostname(ctx, h.claim)
	if err != nil {
		return "", fmt.Errorf("failed to get hostname: %w", err)
	}

	if h.pool.Spec.DNSZone != "" {
		hn += "." + h.pool.Spec.DNSZone
	}

	return hn, nil
}

func getHostnameResolver(cl client.Client, claim *ipamv1.IPAddressClaim) (hostname.Resolver, error) {
	switch claim.Kind {
	case "Metal3Data":
		return &hostname.OwnerChainResolver{
			Client: cl,
			Chain: []metav1.GroupKind{
				{Group: "infrastructure.cluster.x-k8s.io", Kind: "Metal3Data"},
				{Group: "infrastructure.cluster.x-k8s.io", Kind: "Metal3Machine"},
				{Group: "cluster.x-k8s.io", Kind: "Machine"},
			},
		}, nil
	case "VSphereVM":
		return &hostname.OwnerChainResolver{
			Client: cl,
			Chain: []metav1.GroupKind{
				{Group: "infrastructure.cluster.x-k8s.io", Kind: "VSphereVM"},
				{Group: "infrastructure.cluster.x-k8s.io", Kind: "VSphereMachine"},
				{Group: "cluster.x-k8s.io", Kind: "Machine"},
			},
		}, nil
	default:
		return nil, fmt.Errorf("failed to create resolver for kind %s", claim.Kind)
	}
}
