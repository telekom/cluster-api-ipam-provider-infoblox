package controllers

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("InfobloxInstance controller", func() {
	var instance *v1alpha1.InfobloxInstance

	BeforeEach(func() {
		instance = &v1alpha1.InfobloxInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Spec: v1alpha1.InfobloxInstanceSpec{},
		}

	})

	When("the referenced secret is not found", func() {
		BeforeEach(func() {
			createObj(instance)
		})

		AfterEach(func() {
			deleteObj(&v1alpha1.InfobloxInstance{}, instance.Name, instance.Namespace)
		})

		It("should set the InfobloxInstance to not ready", func() {
			Eventually(Object(&v1alpha1.InfobloxInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      instance.Name,
					Namespace: instance.Namespace,
				},
			})).WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(And(
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
					HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
					HaveField("Reason", BeEquivalentTo(v1alpha1.AuthenticationFailedReason)),
				)))))
		})
	})

	When("the referenced secret is invalid", func() {
		var secret *corev1.Secret

		BeforeEach(func() {
			instance.Spec.CredentialsSecretRef = corev1.LocalObjectReference{
				Name: "test",
			}
			createObj(instance)
			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				StringData: map[string]string{
					"key": "invalid",
				},
			}
			createObj(secret)
		})
		AfterEach(func() {
			deleteObj(&v1alpha1.InfobloxInstance{}, instance.Name, instance.Namespace)
			deleteObj(&corev1.Secret{}, secret.Name, secret.Namespace)
		})

		It("should set the InfobloxInstance to not ready", func() {
			Eventually(Object(&v1alpha1.InfobloxInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      instance.Name,
					Namespace: instance.Namespace,
				},
			})).WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(And(
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
					HaveField("Status", BeEquivalentTo(metav1.ConditionFalse)),
					HaveField("Reason", BeEquivalentTo(v1alpha1.AuthenticationFailedReason)),
				)))))
		})
	})

	When("the provided credentials are invalid", func() {
		var secret *corev1.Secret
		BeforeEach(func() {
			instance.Spec.CredentialsSecretRef = corev1.LocalObjectReference{
				Name: "test",
			}
			createObj(instance)
			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				StringData: map[string]string{
					"username": "user",
					"password": "pass",
				},
			}
			createObj(secret)
		})
		AfterEach(func() {
			deleteObj(&v1alpha1.InfobloxInstance{}, instance.Name, instance.Namespace)
			deleteObj(&corev1.Secret{}, secret.Name, secret.Namespace)
		})

		It("should set the InfobloxInstance to not ready", func() {

		})
	})

})

func createObj(object client.Object) {
	Expect(k8sClient.Create(ctx, object)).To(Succeed())
	Eventually(Get(object)).Should(Succeed())
}

func deleteObj(object client.Object, name, namespace string) {
	object.SetName(name)
	object.SetNamespace(namespace)
	Expect(k8sClient.Delete(ctx, object)).To(Succeed())
	Eventually(Get(object)).ShouldNot(Succeed())
}
