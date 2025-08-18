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

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// InfobloxInstanceReconciler reconciles a InfobloxInstance object.
type InfobloxInstanceReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme

	OperatorNamespace     string
	NewInfobloxClientFunc func(config infoblox.Config) (infoblox.Client, error)
}

//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=infobloxinstances,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=infobloxinstances/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=infobloxinstances/finalizers,verbs=update

// SetupWithManager sets up the controller with the Manager.
func (r *InfobloxInstanceReconciler) SetupWithManager(_ context.Context, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.InfobloxInstance{}).
		Complete(r)
}

// Reconcile and InfobloxInstance.
func (r *InfobloxInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	instance := &v1alpha1.InfobloxInstance{}
	if err := r.Client.Get(ctx, req.NamespacedName, instance); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	patchHelper, err := patch.NewHelper(instance, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if err := patchHelper.Patch(ctx, instance, patch.WithOwnedConditions{}); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
	}()

	return r.reconcile(ctx, instance)
}

func (r *InfobloxInstanceReconciler) reconcile(ctx context.Context, instance *v1alpha1.InfobloxInstance) (ctrl.Result, error) { //nolint:unparam
	logger := log.FromContext(ctx)

	authSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: instance.Spec.CredentialsSecretRef.Name, Namespace: r.OperatorNamespace}, authSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		conditions.MarkFalse(instance,
			clusterv1.ReadyCondition,
			v1alpha1.AuthenticationFailedReason,
			clusterv1.ConditionSeverityError,
			"the referenced settings secret '%s' could not be found in namespace '%s'",
			instance.Spec.CredentialsSecretRef.Name, r.OperatorNamespace)
		return ctrl.Result{}, nil
	}

	authConfig, err := infoblox.AuthConfigFromSecretData(authSecret.Data)
	_ = authConfig
	if err != nil {
		conditions.MarkFalse(instance,
			clusterv1.ReadyCondition,
			v1alpha1.AuthenticationFailedReason,
			clusterv1.ConditionSeverityError,
			"referenced credentials secret is invalid: %s", err)
		return ctrl.Result{}, nil
	}

	hc := infoblox.HostConfig{
		Host:                   instance.Spec.Host,
		Version:                instance.Spec.WAPIVersion,
		DisableTLSVerification: instance.Spec.DisableTLSVerification,
		CustomCAPath:           instance.Spec.CustomCAPath,
		DefaultNetworkView:     instance.Spec.DefaultNetworkView,
		DefaultDNSView:         instance.Spec.DefaultDNSView,
	}

	ibcl, err := r.NewInfobloxClientFunc(infoblox.Config{HostConfig: hc, AuthConfig: authConfig})
	if err != nil {
		conditions.MarkFalse(instance,
			clusterv1.ReadyCondition,
			v1alpha1.AuthenticationFailedReason,
			clusterv1.ConditionSeverityError,
			"could not create infoblox client: %s", err)
		return ctrl.Result{}, nil
	}

	// TODO: handle this in a better way
	if ok, err := ibcl.CheckNetworkViewExists(instance.Spec.DefaultNetworkView); err != nil || !ok {
		logger.Error(err, "could not find default network view", "networkView")
		conditions.MarkFalse(instance,
			clusterv1.ReadyCondition,
			v1alpha1.NetworkViewNotFoundReason,
			clusterv1.ConditionSeverityError,
			"could not find default network view: %s", err)
		return ctrl.Result{}, nil
	}

	// Check DNS view if specified
	if instance.Spec.DefaultDNSView != "" {
		if ok, err := ibcl.CheckDNSViewExists(instance.Spec.DefaultDNSView); err != nil || !ok {
			logger.Error(err, "could not find default DNS view", "dnsView", instance.Spec.DefaultDNSView)
			conditions.MarkFalse(instance,
				clusterv1.ReadyCondition,
				v1alpha1.DNSViewNotFoundReason,
				clusterv1.ConditionSeverityError,
				"could not find default DNS view: %s", err)
			return ctrl.Result{}, nil
		}
	}

	conditions.MarkTrue(instance,
		clusterv1.ReadyCondition)

	return ctrl.Result{}, nil
}
