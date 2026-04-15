package controllers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("InfobloxInstance reconcile — unit", func() {
	var (
		fakeClient client.Client
		reconciler *InfobloxInstanceReconciler
	)

	BeforeEach(func() {
		fakeClient = fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.InfobloxInstance{}).
			Build()
	})

	When("DefaultNetworkView and DefaultDNSView are both empty", func() {
		It("skips CheckNetworkViewExists and sets Ready=True", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "creds",
					Namespace: "",
				},
				Data: map[string][]byte{
					"username": []byte("user"),
					"password": []byte("pass"),
				},
			}
			Expect(fakeClient.Create(context.Background(), secret)).To(Succeed())

			instance := &v1alpha1.InfobloxInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name: "unit-test-instance",
				},
				Spec: v1alpha1.InfobloxInstanceSpec{
					Host:        "somehost",
					WAPIVersion: "1.2.3",
					CredentialsSecretRef: v1alpha1.CredentialsReferece{
						Name: "creds",
					},
				},
			}
			Expect(fakeClient.Create(context.Background(), instance)).To(Succeed())

			reconciler = &InfobloxInstanceReconciler{
				Client: fakeClient,
				Scheme: scheme.Scheme,
				NewInfobloxClientFunc: func(infoblox.Config) (infoblox.Client, error) {
					return mockInfobloxClient, nil
				},
			}

			_, err := reconciler.reconcile(context.Background(), instance)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.Conditions).To(ContainElement(And(
				HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
				HaveField("Status", BeEquivalentTo(metav1.ConditionTrue)),
				HaveField("Reason", BeEquivalentTo(v1alpha1.ConfigurationValidReason)),
			)))
		})
	})
})
