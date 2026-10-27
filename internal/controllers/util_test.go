package controllers

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestInfobloxConfigForInstance(t *testing.T) {
	g := NewWithT(t)
	instance := &v1alpha1.InfobloxInstance{
		Spec: v1alpha1.InfobloxInstanceSpec{
			Host:                   "2001:db8::1",
			Port:                   "8443",
			WAPIVersion:            "2.12",
			DisableTLSVerification: true,
			CustomCAPath:           "/etc/infoblox/ca.crt",
			DefaultNetworkView:     "network-view",
			DefaultDNSView:         "dns-view",
		},
	}
	secret := &corev1.Secret{Data: map[string][]byte{
		"username": []byte("user"),
		"password": []byte("pass"),
	}}

	config, err := infobloxConfigForInstance(instance, secret)

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(config.HostConfig).To(Equal(infoblox.HostConfig{
		Host:                   "2001:db8::1",
		Port:                   "8443",
		Version:                "2.12",
		DisableTLSVerification: true,
		CustomCAPath:           "/etc/infoblox/ca.crt",
		DefaultNetworkView:     "network-view",
		DefaultDNSView:         "dns-view",
	}))
	g.Expect(config.AuthConfig).To(Equal(infoblox.AuthConfig{
		Username: "user",
		Password: "pass",
	}))
}

func TestGetInfobloxClientForInstancePassesIdentityVersionsAndConfig(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

	instance := &v1alpha1.InfobloxInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "instance-a", ResourceVersion: "instance-rv-7"},
		Spec: v1alpha1.InfobloxInstanceSpec{
			Host:                 "infoblox.example.test",
			Port:                 "443",
			WAPIVersion:          "2.12",
			CredentialsSecretRef: v1alpha1.CredentialsReferece{Name: "credentials"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "operator", UID: "secret-uid", ResourceVersion: "secret-rv-9"},
		Data: map[string][]byte{
			"username": []byte("user"),
			"password": []byte("pass"),
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, secret).Build()
	expectedConfig, err := infobloxConfigForInstance(instance, secret)
	g.Expect(err).NotTo(HaveOccurred())

	var (
		gotInstanceName    string
		gotInstanceVersion string
		gotSecretUID       types.UID
		gotSecretVersion   string
		gotConfig          infoblox.Config
	)
	_, err = GetInfobloxClientForInstance(context.Background(), k8sClient, instance.Name, secret.Namespace,
		func(instanceName, instanceResourceVersion string, secretUID types.UID, secretResourceVersion string, config infoblox.Config) (infoblox.Client, error) {
			gotInstanceName = instanceName
			gotInstanceVersion = instanceResourceVersion
			gotSecretUID = secretUID
			gotSecretVersion = secretResourceVersion
			gotConfig = config
			return nil, nil
		})

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(gotInstanceName).To(Equal(instance.Name))
	g.Expect(gotInstanceVersion).To(Equal(instance.ResourceVersion))
	g.Expect(gotSecretUID).To(Equal(secret.UID))
	g.Expect(gotSecretVersion).To(Equal(secret.ResourceVersion))
	g.Expect(gotConfig).To(Equal(expectedConfig))
}

func TestGetInfobloxClientForInstancePropagatesClientCreationError(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

	instance := &v1alpha1.InfobloxInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "instance-a"},
		Spec: v1alpha1.InfobloxInstanceSpec{
			CredentialsSecretRef: v1alpha1.CredentialsReferece{Name: "credentials"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "operator"},
		Data: map[string][]byte{
			"username":   []byte("user"),
			"password":   []byte("pass"),
			"clientCert": []byte("cert-data"),
			"clientKey":  []byte("key-data"),
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, secret).Build()
	wantErr := errors.New("client config: password=pass cert=cert-data key=key-data")

	_, err := GetInfobloxClientForInstance(context.Background(), k8sClient, instance.Name, secret.Namespace,
		func(string, string, types.UID, string, infoblox.Config) (infoblox.Client, error) {
			return nil, wantErr
		})

	g.Expect(err).To(MatchError("create infoblox client: client config: password=<redacted> cert=<redacted> key=<redacted>"))
	g.Expect(err.Error()).NotTo(ContainSubstring("password=pass"))
	g.Expect(err.Error()).NotTo(ContainSubstring("cert-data"))
	g.Expect(err.Error()).NotTo(ContainSubstring("key-data"))
	g.Expect(errors.Is(err, wantErr)).To(BeTrue())
}

func TestInfobloxInstanceReconcilerEvictsMissingInstance(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

	var deletedInstance string
	reconciler := &InfobloxInstanceReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		DeleteInfobloxClientFunc: func(instanceName string) {
			deletedInstance = instanceName
		},
	}
	request := ctrl.Request{NamespacedName: types.NamespacedName{Name: "deleted-instance"}}

	_, err := reconciler.Reconcile(context.Background(), request)

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deletedInstance).To(Equal(request.Name))
}
