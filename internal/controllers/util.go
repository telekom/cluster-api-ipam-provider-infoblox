package controllers

import (
	"context"
	"fmt"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getInfobloxClientForInstance(ctx context.Context, client client.Reader, name, secretNamespace string, newClientFn func(infoblox.Config) (infoblox.Client, error)) (infoblox.Client, error) {
	instance := &v1alpha1.InfobloxInstance{}
	if err := client.Get(ctx, types.NamespacedName{Name: name}, instance); err != nil {
		return nil, fmt.Errorf("failed to fetch instance: %w", err)
	}

	secret := &corev1.Secret{}
	if err := client.Get(ctx, types.NamespacedName{Name: instance.Spec.CredentialsSecretRef.Name, Namespace: secretNamespace}, secret); err != nil {
		return nil, fmt.Errorf("failed to fetch secret: %w", err)
	}

	ac, err := infoblox.AuthConfigFromSecretData(secret.Data)
	if err != nil {
		return nil, fmt.Errorf("credentials secret is invalid: %w", err)
	}
	config := infoblox.Config{
		HostConfig: infoblox.HostConfig{
			Host:                   instance.Spec.Host + ":" + instance.Spec.Port,
			Version:                instance.Spec.WAPIVersion,
			DisableTLSVerification: instance.Spec.DisableTLSVerification,
			DefaultNetworkView:     instance.Spec.DefaultNetworkView,
		},
		AuthConfig: ac,
	}

	return newClientFn(config)
}
