package controllers

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
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

func TestMarkFailedInfobloxRequestClassifiesErrors(t *testing.T) {
	dnsErr := &url.Error{Op: "Get", URL: "https://infoblox.example:443/wapi", Err: &net.DNSError{Err: "no such host", Name: "infoblox.example"}}

	tests := []struct {
		name       string
		err        error
		wantReason string
	}{
		{
			name: "transport error in a request error",
			err: infoblox.RequestError{
				Endpoint:  "infoblox.example:443",
				Operation: "GetNetworkView",
				Params:    map[string]string{"view": "TDCN"},
				Err:       dnsErr,
			},
			wantReason: v1alpha1.InfobloxConnectionFailedReason,
		},
		{
			name:       "transport error without a request error",
			err:        dnsErr,
			wantReason: v1alpha1.InfobloxConnectionFailedReason,
		},
		{
			name: "WAPI error",
			err: infoblox.RequestError{
				Endpoint:  "infoblox.example:443",
				Operation: "GetNetworkView",
				Err:       infoblox.WapiError{HTTPError: infoblox.HTTPError{StatusCode: 400}, Message: "Field is not searchable"},
			},
			wantReason: v1alpha1.InfobloxCheckFailedReason,
		},
		{
			name: "WAPI 401",
			err: infoblox.RequestError{
				Endpoint:  "infoblox.example:443",
				Operation: "GetNetworkView",
				Err:       infoblox.WapiError{HTTPError: infoblox.HTTPError{StatusCode: 401}, Message: "Authorization required"},
			},
			wantReason: v1alpha1.AuthenticationFailedReason,
		},
		{
			name: "HTTP 403 without WAPI body",
			err: infoblox.RequestError{
				Endpoint:  "infoblox.example:443",
				Operation: "GetNetworkView",
				Err:       infoblox.HTTPError{StatusCode: 403},
			},
			wantReason: v1alpha1.AuthenticationFailedReason,
		},
		{
			name: "HTTP 502 without WAPI body",
			err: infoblox.RequestError{
				Endpoint:  "infoblox.example:443",
				Operation: "GetNetworkView",
				Err:       infoblox.HTTPError{StatusCode: 502},
			},
			wantReason: v1alpha1.InfobloxCheckFailedReason,
		},
		{
			name:       "other error",
			err:        errors.New("unexpected end of JSON input"),
			wantReason: v1alpha1.InfobloxCheckFailedReason,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			obj := &v1alpha1.InfobloxInstance{}

			err := markFailedInfobloxRequest(obj, tt.err, v1alpha1.NetworkViewNotFoundReason, `default network view "TDCN"`)

			g.Expect(errors.Unwrap(err)).To(Equal(tt.err))
			g.Expect(err).To(MatchError(ContainSubstring(`failed to check default network view "TDCN"`)))
			condition := conditions.Get(obj, clusterv1.ReadyCondition)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(condition.Reason).To(Equal(tt.wantReason))
			g.Expect(condition.Message).To(Equal(`could not check default network view "TDCN": ` + tt.err.Error()))
		})
	}
}
