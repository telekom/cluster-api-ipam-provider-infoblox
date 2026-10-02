package controllers

import (
	"context"
	"fmt"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// markFailedInfobloxRequest sets the `Ready` condition to the provided Setter for a failed infoblox request.
//
// If an error is provided the condition reason comes from infobloxErrorReason. The error will be wrapped
// with the `subject` and returned.
//
// If no error is provided the condition will be set to the provided
// `notFoundReason` instead and no error (nil) will be returned.
func markFailedInfobloxRequest(obj conditions.Setter, err error, notFoundReason, subject string) error {
	if err != nil {
		conditions.Set(obj, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  infobloxErrorReason(err),
			Message: fmt.Sprintf("could not check %s: %v", subject, err),
		})
		return fmt.Errorf("failed to check %s: %w", subject, err)
	}

	conditions.Set(obj, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  notFoundReason,
		Message: fmt.Sprintf("could not find %s", subject),
	})
	return nil
}

// infobloxErrorReason returns the condition reason for a failed Infoblox request: AuthenticationFailed for
// HTTP 401 or 403, InfobloxRequestFailed for any other WAPI error, and InfobloxConnectionFailed for
// each error without a WAPI response.
func infobloxErrorReason(err error) string {
	switch {
	case infoblox.IsAuthError(err):
		return v1alpha1.AuthenticationFailedReason
	case infoblox.IsWapiError(err):
		return v1alpha1.InfobloxRequestFailedReason
	default:
		return v1alpha1.InfobloxConnectionFailedReason
	}
}

// GetInfobloxClientForInstance returns an Infoblox client for the named InfobloxInstance, built
// from the credentials secret the instance references in the given namespace.
func GetInfobloxClientForInstance(ctx context.Context, client client.Reader, name, secretNamespace string, getClientFunc infoblox.GetClientFunc) (infoblox.Client, error) {
	instance := &v1alpha1.InfobloxInstance{}
	if err := client.Get(ctx, types.NamespacedName{Name: name}, instance); err != nil {
		return nil, fmt.Errorf("failed to fetch instance: %w", err)
	}

	secret := &corev1.Secret{}
	if err := client.Get(ctx, types.NamespacedName{Name: instance.Spec.CredentialsSecretRef.Name, Namespace: secretNamespace}, secret); err != nil {
		return nil, fmt.Errorf("failed to fetch secret: %w", err)
	}

	config, err := infobloxConfigForInstance(instance, secret)
	if err != nil {
		return nil, fmt.Errorf("credentials secret is invalid: %w", err)
	}

	return getClientFunc(instance.Name, instance.ResourceVersion, secret.UID, secret.ResourceVersion, config)
}

func infobloxConfigForInstance(instance *v1alpha1.InfobloxInstance, secret *corev1.Secret) (infoblox.Config, error) {
	authConfig, err := infoblox.AuthConfigFromSecretData(secret.Data)
	if err != nil {
		return infoblox.Config{}, err
	}

	return infoblox.Config{
		HostConfig: infoblox.HostConfig{
			Host:                   instance.Spec.Host,
			Port:                   instance.Spec.Port,
			Version:                instance.Spec.WAPIVersion,
			CustomCAPath:           instance.Spec.CustomCAPath,
			DisableTLSVerification: instance.Spec.DisableTLSVerification,
			DefaultNetworkView:     instance.Spec.DefaultNetworkView,
			DefaultDNSView:         instance.Spec.DefaultDNSView,
		},
		AuthConfig: authConfig,
	}, nil
}
