/*
Copyright 2023 Deutsche Telekom AG.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox/ibmock"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The InfobloxInstance reconciler is driven directly, one reconciliation at a time, so a spec can
// assert on the outcome deterministically and keep its Infoblox mock entirely local. Assertions
// read from the API server.
//
// InfobloxInstance is cluster scoped, so every spec uses a name derived from its own namespace to
// stay isolated. The credentials secret is resolved in the reconciler's OperatorNamespace, which
// each spec points at that same namespace.
var _ = Describe("InfobloxInstanceReconciler", func() {
	var (
		namespace    string
		instanceName string
		instanceMock *ibmock.MockClient
		clientErr    error
		reconciler   *InfobloxInstanceReconciler
		instance     *v1alpha1.InfobloxInstance
	)

	// reconcileInstance runs a single reconciliation for the instance under test.
	reconcileInstance := func() (ctrl.Result, error) {
		return reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: instanceName}})
	}

	// expectReadyCondition asserts on the Ready condition the reconciliation left behind.
	expectReadyCondition := func(status metav1.ConditionStatus, reason, messageSubstring string) {
		obj := &v1alpha1.InfobloxInstance{}
		ExpectWithOffset(2, apiClient.Get(ctx, client.ObjectKey{Name: instanceName}, obj)).To(Succeed())
		ExpectWithOffset(2, obj.Status.Conditions).To(ContainElement(And(
			HaveField("Type", BeEquivalentTo(clusterv1.ReadyCondition)),
			HaveField("Status", BeEquivalentTo(status)),
			HaveField("Reason", BeEquivalentTo(reason)),
			HaveField("Message", ContainSubstring(messageSubstring)),
		)))
	}

	// expectCondition reconciles once, expecting that to succeed, and asserts on the resulting Ready
	// condition. A misconfigured instance is a verdict, not a fault: there is nothing to retry.
	expectCondition := func(status metav1.ConditionStatus, reason, messageSubstring string) {
		_, err := reconcileInstance()
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		expectReadyCondition(status, reason, messageSubstring)
	}

	// expectFailedCondition reconciles once, expecting that to fail, and asserts on the resulting
	// Ready condition. The error is what gets the reconciliation retried.
	expectFailedCondition := func(errSubstring string, reason, messageSubstring string) {
		_, err := reconcileInstance()
		ExpectWithOffset(1, err).To(MatchError(ContainSubstring(errSubstring)))
		expectReadyCondition(metav1.ConditionFalse, reason, messageSubstring)
	}

	// createCredentialsSecret creates a credentials secret in the operator namespace and points the
	// instance at it.
	createCredentialsSecret := func(data map[string]string) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-credentials", Namespace: namespace},
			StringData: data,
		}
		createObj(secret)
		instance.Spec.CredentialsSecretRef = v1alpha1.CredentialsReferece{Name: secret.Name}
	}

	BeforeEach(func() {
		namespace = createNamespace()
		instanceName = namespace + "-instance"
		clientErr = nil

		// A gomock controller scoped to the spec makes gomock verify the expected interactions when
		// the spec ends instead of at the end of the whole suite.
		instanceMock = ibmock.NewMockClient(gomock.NewController(GinkgoT()))
		reconciler = &InfobloxInstanceReconciler{
			Client:            apiClient,
			Scheme:            apiClient.Scheme(),
			OperatorNamespace: namespace,
			GetInfobloxClientFunc: func(_, _ string, _ types.UID, _ string, _ infoblox.Config) (infoblox.Client, error) {
				return instanceMock, clientErr
			},
		}

		instance = &v1alpha1.InfobloxInstance{
			ObjectMeta: metav1.ObjectMeta{Name: instanceName},
			Spec: v1alpha1.InfobloxInstanceSpec{
				Host:                 "somehost",
				WAPIVersion:          "1.2.3",
				CredentialsSecretRef: v1alpha1.CredentialsReferece{Name: "does-not-exist"},
			},
		}
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(apiClient.Delete(ctx, &v1alpha1.InfobloxInstance{
				ObjectMeta: metav1.ObjectMeta{Name: instanceName},
			}))).To(Succeed())
		})
	})

	When("the instance does not exist", func() {
		It("should not return an error", func() {
			res, err := reconcileInstance()

			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(ctrl.Result{}))
		})

		It("should evict a cached infoblox client for it", func() {
			var evicted string
			reconciler.DeleteInfobloxClientFunc = func(name string) { evicted = name }

			_, err := reconcileInstance()

			Expect(err).NotTo(HaveOccurred())
			Expect(evicted).To(Equal(instanceName))
		})
	})

	When("the configuration is valid", func() {
		It("should set the instance to ready", func() {
			createCredentialsSecret(map[string]string{"username": "user", "password": "pass"})
			createObj(instance)

			expectCondition(metav1.ConditionTrue, v1alpha1.ConfigurationValidReason,
				"Successfully connected to Infoblox instance")
		})
	})

	When("the referenced secret does not exist", func() {
		It("should set the instance to not ready", func() {
			createObj(instance)

			expectCondition(metav1.ConditionFalse, v1alpha1.AuthenticationFailedReason,
				`the referenced settings secret "does-not-exist" could not be found in namespace "`+namespace+`"`)
		})
	})

	When("the referenced secret does not contain credentials", func() {
		It("should set the instance to not ready", func() {
			createCredentialsSecret(map[string]string{"key": "invalid"})
			createObj(instance)

			expectCondition(metav1.ConditionFalse, v1alpha1.AuthenticationFailedReason,
				"the referenced settings secret is invalid")
		})
	})

	When("the infoblox client cannot be created", func() {
		It("should set the instance to not ready", func() {
			clientErr = errors.New("authentication rejected")
			createCredentialsSecret(map[string]string{"username": "user", "password": "wrong"})
			createObj(instance)

			expectCondition(metav1.ConditionFalse, v1alpha1.AuthenticationFailedReason,
				"could not create infoblox client")
		})
	})

	When("a default network view is configured", func() {
		BeforeEach(func() {
			instance.Spec.DefaultNetworkView = "instance-view"
			createCredentialsSecret(map[string]string{"username": "user", "password": "pass"})
		})

		It("should set the instance to not ready if the view does not exist", func() {
			instanceMock.EXPECT().CheckNetworkViewExists("instance-view").Return(false, nil).Times(1)
			createObj(instance)

			expectCondition(metav1.ConditionFalse, v1alpha1.NetworkViewNotFoundReason,
				`could not find default network view "instance-view"`)
		})

		It("should set the instance to not ready and return an error if the view cannot be looked up", func() {
			instanceMock.EXPECT().CheckNetworkViewExists("instance-view").
				Return(false, errors.New("infoblox said no")).Times(1)
			createObj(instance)

			expectFailedCondition("infoblox said no", v1alpha1.InfobloxCheckFailedReason,
				`could not check default network view "instance-view"`)
		})

		It("should set the instance to ready if the view exists", func() {
			instanceMock.EXPECT().CheckNetworkViewExists("instance-view").Return(true, nil).Times(1)
			createObj(instance)

			expectCondition(metav1.ConditionTrue, v1alpha1.ConfigurationValidReason,
				"Successfully connected to Infoblox instance")
		})
	})

	When("a default DNS view is configured", func() {
		BeforeEach(func() {
			instance.Spec.DefaultDNSView = "instance-dns-view"
			createCredentialsSecret(map[string]string{"username": "user", "password": "pass"})
		})

		It("should set the instance to not ready if the view does not exist", func() {
			instanceMock.EXPECT().CheckDNSViewExists("instance-dns-view").Return(false, nil).Times(1)
			createObj(instance)

			expectCondition(metav1.ConditionFalse, v1alpha1.DNSViewNotFoundReason,
				`could not find default DNS view "instance-dns-view"`)
		})

		It("should set the instance to not ready and return an error if the view cannot be looked up", func() {
			instanceMock.EXPECT().CheckDNSViewExists("instance-dns-view").
				Return(false, errors.New("infoblox said no")).Times(1)
			createObj(instance)

			expectFailedCondition("infoblox said no", v1alpha1.InfobloxCheckFailedReason,
				`could not check default DNS view "instance-dns-view"`)
		})

		It("should set the instance to ready if the view exists", func() {
			instanceMock.EXPECT().CheckDNSViewExists("instance-dns-view").Return(true, nil).Times(1)
			createObj(instance)

			expectCondition(metav1.ConditionTrue, v1alpha1.ConfigurationValidReason,
				"Successfully connected to Infoblox instance")
		})
	})
})
