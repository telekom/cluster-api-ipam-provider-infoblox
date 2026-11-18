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

package webhooks

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// recordingInfobloxIPPool wraps the production InfobloxIPPool webhook and records
// whether the API server actually dispatched a defaulting request to it.
//
// The production Default implementation is intentionally a no-op, so there is no
// observable mutation on the persisted object that could prove the mutating
// webhook was reached. Wrapping it is the only way to assert reachability without
// inventing defaulting behaviour that does not exist. All calls are delegated to
// the embedded production webhook, so the behaviour under test is unchanged.
type recordingInfobloxIPPool struct {
	*InfobloxIPPool
	defaultCalls atomic.Int32
}

func (w *recordingInfobloxIPPool) Default(ctx context.Context, pool *v1alpha1.InfobloxIPPool) error {
	w.defaultCalls.Add(1)
	return w.InfobloxIPPool.Default(ctx, pool)
}

// TestWebhookConfigurationIsWiredToAPIServer starts an envtest API server, installs
// the *checked-in generated* webhook configuration from config/webhook/manifests.yaml
// and asserts that the API server really routes InfobloxIPPool admission requests to
// this provider's webhook server.
//
// This is a regression test for the webhook markers declaring versions=v1alpha2 while
// the only served API version is v1alpha1. With that typo the generated
// Validating/MutatingWebhookConfiguration rules match an apiVersion that never appears
// on the wire, so validation and defaulting silently never fire in a real cluster. The
// existing unit tests in this package call the validator directly and therefore stayed
// green for the entire time the webhook was inert in-cluster.
func TestWebhookConfigurationIsWiredToAPIServer(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	g.Expect(ipamv1.AddToScheme(scheme)).To(Succeed())

	testEnv := &envtest.Environment{
		Scheme: scheme,
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "config", "crd", "test"),
		},
		ErrorIfCRDPathMissing:   true,
		ControlPlaneStopTimeout: 60 * time.Second,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			// Deliberately the generated manifest, not a hand-written fixture: the
			// apiVersions selector under test is what this file generates.
			Paths: []string{filepath.Join("..", "..", "config", "webhook", "manifests.yaml")},
		},
	}

	cfg, err := testEnv.Start()
	g.Expect(err).NotTo(HaveOccurred())
	t.Cleanup(func() {
		g.Expect(testEnv.Stop()).To(Succeed())
	})

	testCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	webhookOpts := testEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    webhookOpts.LocalServingHost,
			Port:    webhookOpts.LocalServingPort,
			CertDir: webhookOpts.LocalServingCertDir,
		}),
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(index.SetupIndexes(testCtx, mgr.GetFieldIndexer())).To(Succeed())

	recorder := &recordingInfobloxIPPool{InfobloxIPPool: &InfobloxIPPool{Client: mgr.GetClient()}}
	g.Expect(
		ctrl.NewWebhookManagedBy(mgr, &v1alpha1.InfobloxIPPool{}).
			WithDefaulter(recorder).
			WithValidator(recorder).
			Complete(),
	).To(Succeed())

	go func() {
		_ = mgr.Start(testCtx)
	}()

	g.Eventually(mgr.GetCache().WaitForCacheSync, 30*time.Second).WithArguments(testCtx).Should(BeTrue())
	waitForWebhookServer(g, mgr)

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	g.Expect(err).NotTo(HaveOccurred())

	const namespace = "default"

	t.Run("rejects an invalid pool on create", func(t *testing.T) {
		g := NewWithT(t)

		// A non-canonical CIDR passes the CRD OpenAPI schema (cidr is an
		// unconstrained string with no pattern or CEL rule), so a rejection here can
		// only come from the admission webhook.
		pool := &v1alpha1.InfobloxIPPool{
			ObjectMeta: metav1.ObjectMeta{Name: "invalid-cidr", Namespace: namespace},
			Spec: v1alpha1.InfobloxIPPoolSpec{
				InstanceRef: v1alpha1.InstanceReference{Name: "test-instance"},
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.0.0.3/30", Gateway: "10.0.0.1"}},
			},
		}

		err := k8sClient.Create(testCtx, pool)
		g.Expect(err).To(HaveOccurred(), "API server must reject an invalid pool; if this passes the webhook is not wired up")
		g.Expect(err.Error()).To(ContainSubstring("is not a valid CIDR"))
		g.Expect(err.Error()).To(ContainSubstring("validation.infobloxippool.ipam.cluster.x-k8s.io"))
	})

	t.Run("rejects mixed IPv4 CIDR with IPv6 gateway on create", func(t *testing.T) {
		g := NewWithT(t)

		pool := &v1alpha1.InfobloxIPPool{
			ObjectMeta: metav1.ObjectMeta{Name: "mixed-families", Namespace: namespace},
			Spec: v1alpha1.InfobloxIPPoolSpec{
				InstanceRef: v1alpha1.InstanceReference{Name: "test-instance"},
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.0.0.0/24", Gateway: "2001:db8::1"}},
			},
		}

		err := k8sClient.Create(testCtx, pool)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("CIDR and gateway are mixed IPv4 and IPv6 addresses"))
	})

	t.Run("invokes the mutating webhook on create", func(t *testing.T) {
		g := NewWithT(t)

		before := recorder.defaultCalls.Load()

		pool := &v1alpha1.InfobloxIPPool{
			ObjectMeta: metav1.ObjectMeta{Name: "valid-pool", Namespace: namespace},
			Spec: v1alpha1.InfobloxIPPoolSpec{
				InstanceRef: v1alpha1.InstanceReference{Name: "test-instance"},
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.10.0.0/24", Gateway: "10.10.0.1"}},
			},
		}
		g.Expect(k8sClient.Create(testCtx, pool)).To(Succeed())
		t.Cleanup(func() {
			pool.Annotations = map[string]string{SkipValidateDeleteWebhookAnnotation: ""}
			_ = k8sClient.Update(testCtx, pool)
			_ = k8sClient.Delete(testCtx, pool)
		})

		g.Expect(recorder.defaultCalls.Load()).To(BeNumerically(">", before),
			"MutatingWebhookConfiguration must route defaulting requests to the webhook server")
	})

	t.Run("rejects an invalid pool on update", func(t *testing.T) {
		g := NewWithT(t)

		pool := &v1alpha1.InfobloxIPPool{
			ObjectMeta: metav1.ObjectMeta{Name: "update-target", Namespace: namespace},
			Spec: v1alpha1.InfobloxIPPoolSpec{
				InstanceRef: v1alpha1.InstanceReference{Name: "test-instance"},
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.20.0.0/24", Gateway: "10.20.0.1"}},
			},
		}
		g.Expect(k8sClient.Create(testCtx, pool)).To(Succeed())
		t.Cleanup(func() {
			pool.Annotations = map[string]string{SkipValidateDeleteWebhookAnnotation: ""}
			_ = k8sClient.Update(testCtx, pool)
			_ = k8sClient.Delete(testCtx, pool)
		})

		// Keep the value valid against the CRD's broad CIDR pattern so the request
		// reaches the validating webhook instead of being rejected by the API server
		// before admission. The webhook enforces canonical network notation.
		pool.Spec.Subnets = []v1alpha1.Subnet{{CIDR: "10.20.0.3/24", Gateway: "10.20.0.1"}}
		err := k8sClient.Update(testCtx, pool)
		g.Expect(err).To(HaveOccurred(), "API server must reject an invalid update; if this passes the webhook is not wired up")
		g.Expect(err.Error()).To(ContainSubstring("is not a valid CIDR"))
	})

	t.Run("accepts a subnet without a gateway", func(t *testing.T) {
		g := NewWithT(t)

		// Gateway is marked Optional in the API, so an unset gateway must remain
		// valid now that the webhook actually runs. Before the accompanying fix this
		// would have been rejected the moment the webhook started firing, breaking
		// pools that were legal under the CRD schema.
		pool := &v1alpha1.InfobloxIPPool{
			ObjectMeta: metav1.ObjectMeta{Name: "no-gateway", Namespace: namespace},
			Spec: v1alpha1.InfobloxIPPoolSpec{
				InstanceRef: v1alpha1.InstanceReference{Name: "test-instance"},
				Subnets:     []v1alpha1.Subnet{{CIDR: "10.30.0.0/24"}},
			},
		}
		g.Expect(k8sClient.Create(testCtx, pool)).To(Succeed())
		t.Cleanup(func() {
			pool.Annotations = map[string]string{SkipValidateDeleteWebhookAnnotation: ""}
			_ = k8sClient.Update(testCtx, pool)
			_ = k8sClient.Delete(testCtx, pool)
		})
	})
}

func waitForWebhookServer(g Gomega, mgr manager.Manager) {
	g.Eventually(func() error {
		return mgr.GetWebhookServer().StartedChecker()(nil)
	}, 30*time.Second, 100*time.Millisecond).Should(Succeed())
}
