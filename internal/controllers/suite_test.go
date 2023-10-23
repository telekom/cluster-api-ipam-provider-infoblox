/*
Copyright 2023 The Kubernetes Authors.

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
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox/ibmock"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancelCtx func()

	mockInfobloxClient        *ibmock.MockClient
	localInfobloxClientMock   *ibmock.MockClient
	mockNewInfobloxClientFunc func(infoblox.Config) (infoblox.Client, error)
	mockCtrl                  *gomock.Controller
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	mockCtrl = gomock.NewController(t)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancelCtx = context.WithCancel(ctrl.SetupSignalHandler())

	mockInfobloxClient = ibmock.NewMockClient(mockCtrl)
	mockNewInfobloxClientFunc = func(infoblox.Config) (infoblox.Client, error) {
		return mockInfobloxClient, nil
	}

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "config", "crd", "test"),
		},
		ErrorIfCRDPathMissing:    true,
		ControlPlaneStopTimeout:  60 * time.Second,
		AttachControlPlaneOutput: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	// Expect(v1alpha2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(clusterv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(ipamv1.AddToScheme(scheme.Scheme)).To(Succeed())

	//+kubebuilder:scaffold:scheme

	syncDur := 100 * time.Millisecond
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:     scheme.Scheme,
		SyncPeriod: &syncDur,
	})
	Expect(err).ToNot(HaveOccurred())

	k8sClient = mgr.GetClient()
	komega.SetClient(mgr.GetClient())

	Expect(index.SetupIndexes(ctx, mgr)).To(Succeed())

	Expect(
		(&InfobloxInstanceReconciler{
			Client:                mgr.GetClient(),
			Scheme:                mgr.GetScheme(),
			newInfobloxClientFunc: mockNewInfobloxClientFunc,
		}).SetupWithManager(ctx, mgr),
	).To(Succeed())

	Expect(
		(&ipamutil.ClaimReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Provider: &InfobloxProviderAdapter{
				NewInfobloxClientFunc: mockNewInfobloxClientFunc,
			},
		}).SetupWithManager(ctx, mgr),
	).To(Succeed())

	// Expect(
	// 	(&InClusterIPPoolReconciler{
	// 		Client: mgr.GetClient(),
	// 		Scheme: mgr.GetScheme(),
	// 	}).SetupWithManager(ctx, mgr),
	// ).To(Succeed())

	// Expect(
	// 	(&GlobalInClusterIPPoolReconciler{
	// 		Client: mgr.GetClient(),
	// 		Scheme: mgr.GetScheme(),
	// 	}).SetupWithManager(ctx, mgr),
	// ).To(Succeed())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()

})

var _ = AfterSuite(func() {
	cancelCtx()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func newClaim(name, namespace, poolKind, poolName string) ipamv1.IPAddressClaim {
	return ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: ipamv1.IPAddressClaimSpec{
			PoolRef: corev1.TypedLocalObjectReference{
				APIGroup: pointer.String("ipam.cluster.x-k8s.io"),
				Kind:     poolKind,
				Name:     poolName,
			},
		},
	}
}
