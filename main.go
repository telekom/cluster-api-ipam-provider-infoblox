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

// Package main is the main package of the Cluster API In-Cluster IPAM Provider.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/spf13/pflag"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/controllers"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/webhooks"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/pkg/version"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/klog/v2"
	"sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	"sigs.k8s.io/cluster-api/util/flags"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
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
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

// logLevels maps the values of --log-level to zap levels.
var logLevels = map[string]zapcore.Level{
	"debug": zapcore.DebugLevel,
	"info":  zapcore.InfoLevel,
	"warn":  zapcore.WarnLevel,
	"error": zapcore.ErrorLevel,
}

// logFormats maps the values of --log-format to values of --zap-encoder.
var logFormats = map[string]string{
	"text": "console",
	"json": "json",
}

// bindLogFlags registers --log-level, --log-format and the --zap-* flags of controller-runtime on fs.
// The defaults, info and text without stack traces, are closest to the klog output of the releases
// that ignored the --zap-* flags. When a --log-* flag and its --zap-* counterpart are both given,
// the one later on the command line wins.
func bindLogFlags(fs *flag.FlagSet, o *zap.Options) {
	o.BindFlags(fs)
	o.Level = zapcore.InfoLevel
	o.StacktraceLevel = zapcore.PanicLevel
	if err := fs.Set("zap-encoder", logFormats["text"]); err != nil {
		panic(err)
	}
	fs.Func("log-level", "Log level, one of debug, info, warn or error. (default info)", func(v string) error {
		level, ok := logLevels[v]
		if !ok {
			return fmt.Errorf("invalid log level %q, must be one of debug, info, warn or error", v)
		}
		o.Level = level
		return nil
	})
	fs.Func("log-format", "Log format, one of text or json. (default text)", func(v string) error {
		encoder, ok := logFormats[v]
		if !ok {
			return fmt.Errorf("invalid log format %q, must be one of text or json", v)
		}
		return fs.Set("zap-encoder", encoder)
	})
}

func main() {
	var (
		enableLeaderElection    bool
		maxConcurrentReconciles int
		probeAddr               string
		watchNamespace          string
		watchFilter             string

		managerOptions = flags.ManagerOptions{}

		webhookOpts = webhook.Options{}
	)
	// CAPI operator assumes some flags are present so we need to comply. Without this the operator crashes
	// https://github.com/kubernetes-sigs/cluster-api-operator/pull/871
	flags.AddManagerOptions(pflag.CommandLine, &managerOptions)

	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 1,
		"Maximum number of IPAddressClaims reconciled concurrently.")
	flag.StringVar(&watchNamespace, "namespace", "",
		"Namespace that the controller watches to reconcile cluster-api objects. If unspecified, the controller watches for cluster-api objects across all namespaces.")
	flag.StringVar(&watchFilter, "watch-filter", "", "")
	flag.IntVar(&webhookOpts.Port, "webhook-port", webhook.DefaultPort,
		"Webhook Server port")
	flag.StringVar(&webhookOpts.CertDir, "webhook-cert-dir", "",
		"Webhook cert dir, only used when webhook-port is specified.")
	flag.StringVar(&webhookOpts.CertName, "webhook-cert-name", "", "Webhook cert name.")
	flag.StringVar(&webhookOpts.KeyName, "webhook-key-name", "", "Webhook key name.")

	zapOpts := zap.Options{}
	bindLogFlags(flag.CommandLine, &zapOpts)

	goFlagSet := flag.CommandLine
	pflag.CommandLine.AddGoFlagSet(goFlagSet)

	pflag.Parse()
	// Set the logger first: lines logged before ctrl.SetLogger are lost when the process exits.
	logger := zap.New(zap.UseFlagOptions(&zapOpts))
	ctrl.SetLogger(logger)
	// client-go and other libraries log through klog; send their lines through the same logger.
	klog.SetLogger(logger)

	if maxConcurrentReconciles < 1 {
		setupLog.Error(os.ErrInvalid, "max-concurrent-reconciles must be greater than zero", "value", maxConcurrentReconciles)
		os.Exit(1)
	}

	tlsOpts, metricsOpts, err := flags.GetManagerOptions(managerOptions)
	if err != nil {
		setupLog.Error(err, "unable to get manager options")
		os.Exit(1)
	}
	webhookOpts.TLSOpts = tlsOpts

	// suppress logs from Infoblox client library
	log.SetOutput(io.Discard)

	ctx := ctrl.SetupSignalHandler()

	opts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                *metricsOpts,
		WebhookServer:          webhook.NewServer(webhookOpts),
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

	if err = index.SetupIndexes(ctx, mgr.GetFieldIndexer()); err != nil {
		setupLog.Error(err, "failed to setup indexes")
		os.Exit(1)
	}

	podNamespace := os.Getenv("NAMESPACE")
	infobloxClientCache := infoblox.NewClientCache(infoblox.NewClient)

	if err = (&ipamutil.ClaimReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		WatchFilterValue: watchFilter,
		Adapter: &controllers.InfobloxProviderAdapter{
			K8sClient:                        mgr.GetClient(),
			K8sReader:                        mgr.GetAPIReader(),
			OperatorNamespace:                podNamespace,
			MaxConcurrentReconciles:          maxConcurrentReconciles,
			GetInfobloxClientFunc:            infobloxClientCache.Get,
			GetInfobloxClientForInstanceFunc: controllers.GetInfobloxClientForInstance,
			NewHostnameResolverFunc:          controllers.NewHostnameResolver,
		},
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "IPAddressClaim")
		os.Exit(1)
	}

	if err = (&controllers.InfobloxInstanceReconciler{
		Client:                   mgr.GetClient(),
		Scheme:                   mgr.GetScheme(),
		GetInfobloxClientFunc:    infobloxClientCache.Get,
		DeleteInfobloxClientFunc: infobloxClientCache.Delete,
		OperatorNamespace:        podNamespace,
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "InfobloxInstance")
		os.Exit(1)
	}
	if err = (&controllers.InfobloxIPPoolReconciler{
		Client:                mgr.GetClient(),
		APIReader:             mgr.GetAPIReader(),
		Scheme:                mgr.GetScheme(),
		GetInfobloxClientFunc: infobloxClientCache.Get,
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
