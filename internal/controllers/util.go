package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
)

func getInfobloxClientForInstance(ctx context.Context, client client.Reader, name, namespace string, newClientFn func(infoblox.Config) (infoblox.Client, error)) (infoblox.Client, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("getInfobloxClientForInstance 1")
	instance := &v1alpha1.InfobloxInstance{}
	if err := client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, instance); err != nil {
		log.Error(err, "getInfobloxClientForInstance - failed to fetch instance")
		return nil, fmt.Errorf("failed to fetch instance: %w", err)
	}
	log.Info("getInfobloxClientForInstance 2")
	secret := &corev1.Secret{}
	if err := client.Get(ctx, types.NamespacedName{Name: instance.Spec.CredentialsSecretRef.Name, Namespace: instance.Namespace}, secret); err != nil {
		log.Error(err, "getInfobloxClientForInstance - failed to fetch secret")
		return nil, fmt.Errorf("failed to fetch secret: %w", err)
	}

	log.Info("getInfobloxClientForInstance 3")
	ac, err := infoblox.AuthConfigFromSecretData(secret.Data)
	if err != nil {
		log.Error(err, "getInfobloxClientForInstance - credentials secret is invalid")
		return nil, fmt.Errorf("credentials secret is invalid: %w", err)
	}
	config := infoblox.Config{
		HostConfig: infoblox.HostConfig{
			Host:                  instance.Spec.Host,
			Version:               instance.Spec.WAPIVersion,
			InsecureSkipTLSVerify: instance.Spec.InsecureSkipTLSVerify,
		},
		AuthConfig: ac,
	}
	log.Info("getInfobloxClientForInstance 4")
	return newClientFn(config)
}
