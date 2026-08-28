package controllers

import (
	"context"
	"fmt"
	"strings"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type redactedInfobloxClientError struct {
	message string
	err     error
}

func (e redactedInfobloxClientError) Error() string {
	return e.message
}

func (e redactedInfobloxClientError) Unwrap() error {
	return e.err
}

func redactCredential(message, credential string) string {
	for start := 0; start < len(message); {
		relativeStart := strings.Index(message[start:], credential)
		if relativeStart < 0 {
			break
		}
		matchStart := start + relativeStart
		matchEnd := matchStart + len(credential)
		leftIsIdentifier := matchStart > 0 && isIdentifierByte(message[matchStart-1])
		rightIsIdentifier := matchEnd < len(message) && isIdentifierByte(message[matchEnd])
		if !leftIsIdentifier && !rightIsIdentifier {
			message = message[:matchStart] + "<redacted>" + message[matchEnd:]
			start = matchStart + len("<redacted>")
			continue
		}
		start = matchEnd
	}
	return message
}

func isIdentifierByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// redactInfobloxClientError preserves useful client-construction diagnostics while
// ensuring that credentials cannot escape through an upstream error string.
func redactInfobloxClientError(err error, config infoblox.Config) error {
	if err == nil {
		return nil
	}

	message := err.Error()
	for _, credential := range []string{config.Password, string(config.ClientCert), string(config.ClientKey)} {
		if credential == "" {
			continue
		}
		for _, representation := range []string{
			fmt.Sprintf("%q", credential),
			fmt.Sprintf("%v", []byte(credential)),
			fmt.Sprintf("%#v", []byte(credential)),
		} {
			message = strings.ReplaceAll(message, representation, "<redacted>")
		}
		message = redactCredential(message, credential)
	}

	return redactedInfobloxClientError{message: message, err: err}
}

// markFailedInfobloxRequest sets the `Ready` condition to the provided Setter for a failed infoblox request.
//
// If an error is provided the condition reason will be the generic
// `InfobloxCheckFailedReason` and the error will be wrapped with the `subject` and returned.
//
// If no error is provided the condition will be set to the provided
// `notFoundReason` instead and no error (nil) will be returned.
func markFailedInfobloxRequest(obj conditions.Setter, err error, notFoundReason, subject string) error {
	if err != nil {
		conditions.Set(obj, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.InfobloxCheckFailedReason,
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

	ibClient, err := getClientFunc(instance.Name, instance.ResourceVersion, secret.UID, secret.ResourceVersion, config)
	if err != nil {
		return nil, fmt.Errorf("create infoblox client: %w", redactInfobloxClientError(err, config))
	}
	return ibClient, nil
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
