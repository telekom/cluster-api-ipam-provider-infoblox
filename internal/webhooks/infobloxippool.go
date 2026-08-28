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

package webhooks

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/poolutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	// SkipValidateDeleteWebhookAnnotation is an annotation that can be applied
	// to the InClusterIPPool or GlobalInClusterIPPool to skip delete
	// validation. Necessary for clusterctl move to work as expected.
	SkipValidateDeleteWebhookAnnotation = "ipam.cluster.x-k8s.io/skip-validate-delete-webhook"
)

func (webhook *InfobloxIPPool) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.InfobloxIPPool{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:verbs=create;update;delete,path=/validate-ipam-cluster-x-k8s-io-v1alpha1-infobloxippool,mutating=false,failurePolicy=fail,matchPolicy=Equivalent,groups=ipam.cluster.x-k8s.io,resources=infobloxippools,versions=v1alpha1,name=validation.infobloxippool.ipam.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1;v1beta1
// +kubebuilder:webhook:verbs=create;update,path=/mutate-ipam-cluster-x-k8s-io-v1alpha1-infobloxippool,mutating=true,failurePolicy=fail,matchPolicy=Equivalent,groups=ipam.cluster.x-k8s.io,resources=infobloxippools,versions=v1alpha1,name=default.infobloxippool.ipam.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

// InfobloxIPPool implements a validating and defaulting webhook for InfobloxIPPool.
type InfobloxIPPool struct {
	Client client.Client
}

var _ admission.Defaulter[*v1alpha1.InfobloxIPPool] = &InfobloxIPPool{}
var _ admission.Validator[*v1alpha1.InfobloxIPPool] = &InfobloxIPPool{}

// Default satisfies the defaulting webhook interface.
func (webhook *InfobloxIPPool) Default(_ context.Context, _ *v1alpha1.InfobloxIPPool) error {
	return nil
}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type.
func (webhook *InfobloxIPPool) ValidateCreate(_ context.Context, pool *v1alpha1.InfobloxIPPool) (admission.Warnings, error) {
	return nil, webhook.validate(pool)
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type.
func (webhook *InfobloxIPPool) ValidateUpdate(_ context.Context, _, newPool *v1alpha1.InfobloxIPPool) (admission.Warnings, error) {
	// Once the pool is marked for deletion, its spec is no longer actionable: the only
	// updates left are the controller removing ProtectPoolFinalizer and metadata edits
	// that let the deletion finish. Rejecting those on spec grounds would deadlock
	// deletion, leaving the pool in Terminating forever with no way out short of
	// manually stripping the finalizer as cluster-admin. That matters because this
	// webhook was inert for the provider's entire deployed life, so pools that are
	// invalid under these rules are already persisted in real clusters.
	if !newPool.GetDeletionTimestamp().IsZero() {
		return nil, nil
	}

	err := webhook.validate(newPool)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type.
func (webhook *InfobloxIPPool) ValidateDelete(ctx context.Context, pool *v1alpha1.InfobloxIPPool) (admission.Warnings, error) {
	if _, ok := pool.GetAnnotations()[SkipValidateDeleteWebhookAnnotation]; ok {
		return nil, nil
	}

	poolTypeRef := ipamv1.IPPoolReference{
		APIGroup: v1alpha1.GroupVersion.Group,
		Kind:     v1alpha1.InfobloxIPPoolKind,
		Name:     pool.GetName(),
	}

	inUseAddresses, err := poolutil.ListAddressesInUse(ctx, webhook.Client, pool.GetNamespace(), poolTypeRef)
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}

	if len(inUseAddresses) > 0 {
		return nil, apierrors.NewBadRequest("Pool has IPAddresses allocated. Cannot delete Pool until all IPAddresses have been removed.")
	}

	return nil, nil
}

func (webhook *InfobloxIPPool) validate(newPool *v1alpha1.InfobloxIPPool) error {
	var allErrs field.ErrorList

	if len(newPool.Spec.Subnets) == 0 {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "subnets"), newPool.Spec.Subnets, "subnets is required"))
	}

	if newPool.Spec.InstanceRef.Name == "" {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "InstanceRef.Name"),
			newPool.Spec.InstanceRef.Name, "InstanceRef.Name is required"))
	}

	for i, subnet := range newPool.Spec.Subnets {
		_, network, err := net.ParseCIDR(subnet.CIDR)
		if err != nil || network.String() != subnet.CIDR {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", subnetPath(i), "CIDR"),
				newPool.Spec.Subnets[i].CIDR, subnetPath(i)+".CIDR is not a valid CIDR"))
		}

		// net.ParseCIDR returns a nil network on error, so there is nothing left
		// to cross-check for this subnet.
		if network == nil {
			continue
		}

		networkIP, err := netip.ParseAddr(network.IP.String())
		if err != nil {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", subnetPath(i), "CIDR"),
				newPool.Spec.Subnets[i].CIDR, subnetPath(i)+".CIDR could not be parsed"))
			continue
		}

		// Gateway is an optional field: an unset gateway is valid and simply
		// means no gateway is propagated to the allocated IPAddress.
		if subnet.Gateway == "" {
			continue
		}

		gatewayIP, err := netip.ParseAddr(subnet.Gateway)
		if err != nil {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", subnetPath(i), "Gateway"),
				newPool.Spec.Subnets[i].Gateway, subnetPath(i)+".Gateway is not a valid IP address"+" "+err.Error()))
			continue
		}

		ipVersionsMatched := (networkIP.Is4() && gatewayIP.Is4()) || (networkIP.Is6() && gatewayIP.Is6())

		if !ipVersionsMatched {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", subnetPath(i)),
				newPool.Spec.Subnets[i].CIDR, "CIDR and gateway are mixed IPv4 and IPv6 addresses"))
		}
	}

	if len(allErrs) > 0 {
		return apierrors.NewInvalid(
			v1alpha1.GroupVersion.WithKind(v1alpha1.InfobloxIPPoolKind).GroupKind(),
			newPool.GetName(), allErrs)
	}
	return nil
}

func subnetPath(i int) string {
	return fmt.Sprintf("Subnet[%d]", i)
}
