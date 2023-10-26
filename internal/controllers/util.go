package controllers

import (
	"context"
	"fmt"

	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
)

func getInfobloxClientForInstance(ctx context.Context, client client.Reader, name, namespace string, newClientFn func(infoblox.Config) (infoblox.Client, error)) (infoblox.Client, error) {
	instance := &v1alpha1.InfobloxInstance{}
	if err := client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, instance); err != nil {
		return nil, fmt.Errorf("failed to fetch instance: %w", err)
	}

	secret := &corev1.Secret{}
	if err := client.Get(ctx, types.NamespacedName{Name: instance.Spec.CredentialsSecretRef.Name, Namespace: namespace}, secret); err != nil {
		return nil, fmt.Errorf("failed to fetch secret: %w", err)
	}

	ac, err := infoblox.AuthConfigFromSecretData(secret.Data)
	if err != nil {
		return nil, fmt.Errorf("credentials secret is invalid: %w", err)
	}
	config := infoblox.Config{
		HostConfig: infoblox.HostConfig{
			Host:                  instance.Spec.Host + ":" + instance.Spec.Port,
			Version:               instance.Spec.WAPIVersion,
			InsecureSkipTLSVerify: instance.Spec.InsecureSkipTLSVerify,
		},
		AuthConfig: ac,
	}

	return newClientFn(config)
}

const (
	metal3DataKind     = "Metal3Data"
	metal3MachineKind  = "Metal3Machine"
	vsphereMachineKind = "VSphereMachine"
	machineKind        = "Machine"
)

type hostnameHandler interface {
	getHostname(context.Context) (string, error)
}

type metal3HostnameHandler struct {
	client.Client
	claim *ipamv1.IPAddressClaim
}

type vsphereHostnameHandler struct {
	client.Client
	claim *ipamv1.IPAddressClaim
}

func (h *vsphereHostnameHandler) getHostname(ctx context.Context) (string, error) {
	vSphereMachine := v1beta1.VSphereMachine{}
	if err := getOwnerByKind(ctx, h.claim.ObjectMeta, vsphereMachineKind, &vSphereMachine, h.Client); err != nil {
		return "", err
	}

	for _, ownerRef := range vSphereMachine.ObjectMeta.OwnerReferences {
		if ownerRef.Kind == machineKind {
			return ownerRef.Name, nil
		}
	}
	return "", errors.New("hostname not found")
}

func newHostnameHandler(claim *ipamv1.IPAddressClaim, c client.Client) (hostnameHandler, error) {
	for _, ref := range claim.ObjectMeta.OwnerReferences {
		switch ref.Kind {
		case metal3DataKind:
			return &metal3HostnameHandler{claim: claim, Client: c}, nil
		case vsphereMachineKind:
			return &vsphereHostnameHandler{claim: claim, Client: c}, nil
		}
	}

	return nil, fmt.Errorf("cannot create hostname handler: no owner reference of supported kind found")
}

func getOwnerByKind(ctx context.Context, meta metav1.ObjectMeta, kind string, owner client.Object, k8sclient client.Client) error {
	name := ""
	for _, o := range meta.OwnerReferences {
		if o.Kind == kind {
			name = o.Name
			break
		}
	}
	if name == "" {
		return fmt.Errorf("no owner with kind %s found", kind)
	}

	if err := k8sclient.Get(ctx, types.NamespacedName{Namespace: meta.Namespace, Name: name}, owner); err != nil {
		return fmt.Errorf("failed to fetch object: %w", err)
	}
	return nil
}

func (h *metal3HostnameHandler) getHostname(ctx context.Context) (string, error) {
	m3Data := metal3v1.Metal3Data{}
	if err := getOwnerByKind(ctx, h.claim.ObjectMeta, metal3DataKind, &m3Data, h.Client); err != nil {
		return "", err
	}

	m3Machine := metal3v1.Metal3Machine{}
	if err := getOwnerByKind(ctx, m3Data.ObjectMeta, metal3MachineKind, &m3Machine, h.Client); err != nil {
		return "", err
	}

	for _, o := range m3Machine.OwnerReferences {
		if o.Kind == machineKind {
			return o.Name, nil
		}
	}

	return "", fmt.Errorf("hostname not found for claim %s in namespace %s", h.claim.Name, h.claim.Namespace)
}
