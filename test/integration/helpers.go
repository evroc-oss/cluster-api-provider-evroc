// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/controller"
)

// TestEnvironment holds the test environment
type TestEnvironment struct {
	Client      client.Client
	CloudClient cloud.ClientInterface
	Env         *envtest.Environment
	Scheme      *runtime.Scheme
	cancelMgr   context.CancelFunc
}

// SetupTestEnvironmentForSuite creates a test environment for the entire test suite
// This should be called from TestMain, not from individual tests
func SetupTestEnvironmentForSuite() *TestEnvironment {
	loadCredentialsFromE2EConfig()

	// Check required environment variables
	requiredEnvVars := []string{"EVROC_PROJECT", "EVROC_REGION"}
	for _, env := range requiredEnvVars {
		if os.Getenv(env) == "" {
			panic(fmt.Sprintf("%s environment variable must be set", env))
		}
	}

	// Either USERNAME/PASSWORD or TOKEN must be set
	hasUsernameAuth := os.Getenv("EVROC_USERNAME") != "" && os.Getenv("EVROC_PASSWORD") != ""
	hasTokenAuth := os.Getenv("EVROC_TOKEN") != ""
	if !hasUsernameAuth && !hasTokenAuth {
		panic("Either EVROC_USERNAME/EVROC_PASSWORD or EVROC_TOKEN must be set")
	}

	// Create scheme
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = infrav1.AddToScheme(scheme)

	// Setup envtest
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{"../../config/crd/bases"},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		panic(fmt.Sprintf("Failed to start test environment: %v", err))
	}

	// Setup logger for controller-runtime
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	// Create Kubernetes client
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(fmt.Sprintf("Failed to create Kubernetes client: %v", err))
	}

	// Create evroc cloud client with background context
	// Use Background() instead of a timeout context so the SDK can use it
	// for token refresh throughout the test
	cloudClient, err := cloud.NewClient(context.Background(), nil)
	if err != nil {
		panic(fmt.Sprintf("Failed to create evroc cloud client: %v", err))
	}

	// Start controller manager with reconcilers
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics server
		},
		HealthProbeBindAddress: "0", // Disable health probe
	})
	if err != nil {
		panic(fmt.Sprintf("Failed to create controller manager: %v", err))
	}

	// Setup controllers
	if err := setupControllers(mgr, cloudClient); err != nil {
		panic(fmt.Sprintf("Failed to setup controllers: %v", err))
	}

	// Start the manager with a cancellable context
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Printf("Manager stopped with error: %v\n", err)
		}
	}()

	return &TestEnvironment{
		Client:      k8sClient,
		CloudClient: cloudClient,
		Env:         testEnv,
		Scheme:      scheme,
		cancelMgr:   cancel,
	}
}

// SetupTestEnvironment creates a test environment with envtest
func SetupTestEnvironment(t *testing.T) *TestEnvironment {
	t.Helper()

	loadCredentialsFromE2EConfig()

	// Check for integration test flag
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("Skipping integration test - set INTEGRATION_TEST=1 to run")
	}

	// Check required environment variables
	requiredEnvVars := []string{"EVROC_PROJECT", "EVROC_REGION"}
	for _, env := range requiredEnvVars {
		if os.Getenv(env) == "" {
			t.Fatalf("%s environment variable must be set", env)
		}
	}

	// Either USERNAME/PASSWORD or TOKEN must be set
	hasUsernameAuth := os.Getenv("EVROC_USERNAME") != "" && os.Getenv("EVROC_PASSWORD") != ""
	hasTokenAuth := os.Getenv("EVROC_TOKEN") != ""
	if !hasUsernameAuth && !hasTokenAuth {
		t.Fatal("Either EVROC_USERNAME/EVROC_PASSWORD or EVROC_TOKEN must be set")
	}

	// Create scheme
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = infrav1.AddToScheme(scheme)

	// Setup envtest
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{"../../config/crd/bases"},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Failed to start test environment: %v", err)
	}

	// Setup logger for controller-runtime
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	// Create Kubernetes client
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Create evroc cloud client with background context
	// Use Background() instead of a timeout context so the SDK can use it
	// for token refresh throughout the test
	cloudClient, err := cloud.NewClient(context.Background(), nil)
	if err != nil {
		t.Fatalf("Failed to create evroc cloud client: %v", err)
	}

	// Start controller manager with reconcilers
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics server
		},
		HealthProbeBindAddress: "0", // Disable health probe
	})
	if err != nil {
		t.Fatalf("Failed to create controller manager: %v", err)
	}

	// Setup controllers
	if err := setupControllers(mgr, cloudClient); err != nil {
		t.Fatalf("Failed to setup controllers: %v", err)
	}

	// Start the manager
	go func() {
		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			t.Logf("Manager stopped with error: %v", err)
		}
	}()

	return &TestEnvironment{
		Client:      k8sClient,
		CloudClient: cloudClient,
		Env:         testEnv,
		Scheme:      scheme,
	}
}

// TeardownTestEnvironment cleans up the test environment
func (te *TestEnvironment) Teardown(t *testing.T) {
	t.Helper()
	if err := te.Env.Stop(); err != nil {
		t.Logf("Failed to stop test environment: %v", err)
	}
}

// TeardownSuite cleans up the test environment for the entire test suite
// This should be called from TestMain
func (te *TestEnvironment) TeardownSuite() {
	// Stop the controller manager
	if te.cancelMgr != nil {
		te.cancelMgr()
	}

	// Stop envtest
	if te.Env != nil {
		if err := te.Env.Stop(); err != nil {
			fmt.Printf("Failed to stop test environment: %v\n", err)
		}
	}
}

// WaitForCondition waits for a condition to be true
func WaitForCondition(t *testing.T, timeout time.Duration, condition func() bool, failureMsg string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Second)
	}

	t.Fatalf("Timeout waiting for condition: %s", failureMsg)
}

// WaitForResourceReady waits for a resource to become ready
func WaitForResourceReady(t *testing.T, ctx context.Context, k8sClient client.Client, name types.NamespacedName, obj client.Object, timeout time.Duration) {
	t.Helper()

	WaitForCondition(t, timeout, func() bool {
		if err := k8sClient.Get(ctx, name, obj); err != nil {
			t.Logf("Error getting resource %s: %v", name, err)
			return false
		}

		// Check if resource has a Status.Ready field
		switch v := obj.(type) {
		case *infrav1.EvrocMachine:
			return v.Status.Ready
		case *infrav1.EvrocCluster:
			return v.Status.Ready
		default:
			t.Fatalf("Unknown resource type: %T", obj)
			return false
		}
	}, fmt.Sprintf("resource %s to become ready", name))
}

// WaitForResourceDeleted waits for a resource to be deleted from evroc
func WaitForResourceDeleted(t *testing.T, timeout time.Duration, checkDeleted func() error) {
	t.Helper()

	WaitForCondition(t, timeout, func() bool {
		err := checkDeleted()
		if err != nil {
			// Check if it's a "not found" error
			if errors.Is(err, evroc.ErrNotFound) {
				return true
			}
			t.Logf("Resource still exists: %v", err)
			return false
		}
		return false
	}, "resource to be deleted from evroc")
}

// GenerateTestResourceName generates a unique test resource name
func GenerateTestResourceName(prefix string) string {
	return fmt.Sprintf("capi-test-%s-%d", prefix, time.Now().Unix())
}

// setupControllers registers all reconcilers with the manager
func setupControllers(mgr ctrl.Manager, cloudClient cloud.ClientInterface) error {
	// Setup EvrocMachine controller
	if err := (&controller.EvrocMachineReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		CloudClient: cloudClient,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup EvrocMachine controller: %w", err)
	}

	// Setup EvrocCluster controller
	if err := (&controller.EvrocClusterReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		CloudClient: cloudClient,
	}).SetupWithManager(context.Background(), mgr); err != nil {
		return fmt.Errorf("failed to setup EvrocCluster controller: %w", err)
	}

	return nil
}

// loadCredentialsFromE2EConfig loads EVROC_* env vars from test/e2e/config.yaml
// if they are not already set.
func loadCredentialsFromE2EConfig() {
	repoRoot := os.Getenv("REPO_ROOT")
	candidates := []string{
		filepath.Join(repoRoot, "test", "e2e", "config.yaml"),
		filepath.Join("test", "e2e", "config.yaml"),
		filepath.Join("..", "..", "test", "e2e", "config.yaml"),
	}

	var data []byte
	var err error
	for _, p := range candidates {
		if p == "" {
			continue
		}
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		return
	}

	cfg := map[string]map[string]string{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return
	}

	for key, val := range cfg["variables"] {
		if strings.HasPrefix(key, "EVROC_") && os.Getenv(key) == "" && val != "" {
			_ = os.Setenv(key, val)
		}
	}
}
