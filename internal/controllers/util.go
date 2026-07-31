package controllers

import (
	"context"
	"fmt"
	"net"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getInfobloxClientForInstance(ctx context.Context, client client.Reader, name, secretNamespace string, getClientFunc infoblox.GetClientFunc) (infoblox.Client, error) {
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
			Host:                   net.JoinHostPort(instance.Spec.Host, instance.Spec.Port),
			Version:                instance.Spec.WAPIVersion,
			CustomCAPath:           instance.Spec.CustomCAPath,
			DisableTLSVerification: instance.Spec.DisableTLSVerification,
			DefaultNetworkView:     instance.Spec.DefaultNetworkView,
			DefaultDNSView:         instance.Spec.DefaultDNSView,
		},
		AuthConfig: authConfig,
	}, nil
}
