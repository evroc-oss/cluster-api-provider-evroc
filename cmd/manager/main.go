// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/evroc-oss/evroc-go-sdk/metrics"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/controller"
	"github.com/evroc-oss/cluster-api-provider-evroc/pkg/version"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(infrav1.AddToScheme(scheme))
}

func main() {
	var enableLeaderElection bool
	var probeAddr string
	var webhookPort int
	var webhookCertDir string
	var printVersion bool

	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "The port the webhook endpoint binds to.")
	flag.StringVar(&webhookCertDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs", "The directory that contains the webhook server key and certificate.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&printVersion, "version", false, "Print version information and exit.")

	opts := zap.Options{
		Development: false,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	if printVersion {
		versionInfo := version.Get()
		setupLog.Info("evroc Cluster API Provider", "version", versionInfo.String())
		os.Exit(0)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Initialize SDK metrics and register selected collectors with
	// controller-runtime's Prometheus registry. We expose only the
	// high-value metrics (auth health, retries, waiter durations) and
	// skip per-HTTP-call counters to avoid noisy timeseries.
	sdkMetrics := metrics.NewManager()
	ctrlmetrics.Registry.MustRegister(
		// Auth — token refresh failures are actionable and hard to detect otherwise
		sdkMetrics.AuthTokenRefreshesTotal,
		sdkMetrics.AuthTokenRefreshErrors,
		sdkMetrics.AuthTokenRefreshTime,
		sdkMetrics.AuthInitialAuthTotal,
		sdkMetrics.AuthInitialAuthErrors,
		sdkMetrics.AuthInitialAuthTime,
		// Retries — spikes indicate API instability
		sdkMetrics.RetriesTotal,
		sdkMetrics.RetryBackoffTime,
		// Waiters — how long resources take to become ready
		sdkMetrics.WaiterOperationsTotal,
		sdkMetrics.WaiterDuration,
		sdkMetrics.WaiterAttempts,
	)
	setupLog.Info("evroc SDK metrics registered with controller-runtime")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "evroc.cluster.x-k8s.io",
		Metrics: metricsserver.Options{
			BindAddress: ":8080",
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    webhookPort,
			CertDir: webhookCertDir,
		}),
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Try to initialize a global evroc cloud client from the mounted config file.
	// This is optional — clusters with spec.credentialsRef will use per-cluster credentials.
	var cloudClient cloud.ClientInterface
	if globalClient, clientErr := cloud.NewClient(context.Background(), sdkMetrics); clientErr != nil {
		setupLog.Info("No global evroc credentials found (clusters must specify credentialsRef)",
			"path", cloud.ConfigPath, "error", clientErr)
	} else {
		cloudClient = globalClient
		setupLog.Info("Global evroc cloud client initialized successfully")
	}

	if err = (&controller.EvrocClusterReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		CloudClient: cloudClient,
		SDKMetrics:  sdkMetrics,
	}).SetupWithManager(context.Background(), mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "EvrocCluster")
		os.Exit(1)
	}

	if err = (&controller.EvrocMachineReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		CloudClient: cloudClient,
		Recorder:    mgr.GetEventRecorderFor("evrocmachine-controller"),
		SDKMetrics:  sdkMetrics,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "EvrocMachine")
		os.Exit(1)
	}

	// Setup webhooks
	if err = infrav1.SetupEvrocMachineWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "EvrocMachine")
		os.Exit(1)
	}

	if err = infrav1.SetupEvrocClusterWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "EvrocCluster")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
