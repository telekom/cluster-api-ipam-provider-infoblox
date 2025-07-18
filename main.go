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

// Package main is the main package of the Cluster API In-Cluster IPAM Provider.
package main

import (
	"flag"
	"io"
	"log"
	"os"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/controllers"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/webhooks"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/pkg/version"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/klog/v2"
	inclusterv1a2 "sigs.k8s.io/cluster-api-ipam-provider-in-cluster/api/v1alpha2"
	"sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ipamv1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))

	utilruntime.Must(inclusterv1a2.AddToScheme(scheme))
	utilruntime.Must(inclusterv1a2.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

func main() {
	var (
		metricsAddr          string
		enableLeaderElection bool
		probeAddr            string
		watchNamespace       string
		watchFilter          string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&watchNamespace, "namespace", "",
		"Namespace that the controller watches to reconcile cluster-api objects. If unspecified, the controller watches for cluster-api objects across all namespaces.")
	flag.StringVar(&watchFilter, "watch-filter", "", "")
	zapOpts := zap.Options{
		Development: true,
	}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	// suppress logs from Infoblox client library
	log.SetOutput(io.Discard)

	// klog.Background will automatically use the right logger.
	ctrl.SetLogger(klog.Background())

	ctx := ctrl.SetupSignalHandler()

	opts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                server.Options{BindAddress: metricsAddr},
		WebhookServer:          webhook.NewServer(webhook.Options{Port: 9443}),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "7bb7acb4.ipam.cluster.x-k8s.io",
	}

	if watchNamespace != "" {
		opts.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				watchNamespace: {},
			},
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), opts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = index.SetupIndexes(ctx, mgr); err != nil {
		setupLog.Error(err, "failed to setup indexes")
		os.Exit(1)
	}

	podNamespace := os.Getenv("NAMESPACE")

	if err = (&ipamutil.ClaimReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		WatchFilterValue: watchFilter,
		Adapter: &controllers.InfobloxProviderAdapter{
			NewInfobloxClientFunc: infoblox.NewClient,
			OperatorNamespace:     podNamespace,
		},
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "IPAddressClaim")
		os.Exit(1)
	}

	if err = (&controllers.InfobloxInstanceReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		NewInfobloxClientFunc: infoblox.NewClient,
		OperatorNamespace:     podNamespace,
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "InfobloxInstance")
		os.Exit(1)
	}
	if err = (&controllers.InfobloxIPPoolReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		NewInfobloxClientFunc: infoblox.NewClient,
		OperatorNamespace:     podNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "InfobloxIPPool")
		os.Exit(1)
	}

	if err := (&webhooks.InfobloxIPPool{Client: mgr.GetClient()}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "InfobloxIPPool")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager", "version", version.Get().String())
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
