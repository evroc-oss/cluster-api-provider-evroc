//go:build e2e
// +build e2e

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package rancher_turtles_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	turtlesframework "github.com/rancher/turtles/test/framework"
	"github.com/rancher/turtles/test/testenv"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capiframework "sigs.k8s.io/cluster-api/test/framework"
	clusterctl "sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/yaml"
)

// Test suite entry point
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SUSE Rancher integration test suite")
}

var (
	ctx            context.Context
	cancelFunc     context.CancelFunc
	e2eConfig      *clusterctl.E2EConfig
	artifactFolder string
	scheme         *runtime.Scheme

	setupResult *testenv.SetupTestClusterResult
)

// Setup before running any tests
var _ = BeforeSuite(func() {
	klog.SetOutput(GinkgoWriter)

	// Load credentials from file before reading env vars (file values are
	// lower priority than env vars that are already set).
	loadCredentialsFile()

	ctx, cancelFunc = context.WithCancel(context.Background())

	// Load E2E configuration
	configPath := os.Getenv("E2E_CONFIG_PATH")
	if configPath == "" {
		configPath = filepath.Join("..", "..", "config.yaml")
	}

	e2eConfig = turtlesframework.LoadE2EConfig(configPath)
	Expect(e2eConfig).ToNot(BeNil(), "Failed to load E2E config from %s", configPath)

	By(fmt.Sprintf("E2E config loaded from: %s", configPath))

	artifactFolder = os.Getenv("ARTIFACTS_FOLDER")
	if artifactFolder == "" {
		artifactFolder = filepath.Join(os.TempDir(), "capi-evroc-e2e-artifacts")
	}
	Expect(os.MkdirAll(artifactFolder, 0750)).To(Succeed())

	scheme = runtime.NewScheme()
	capiframework.TryAddDefaultSchemes(scheme)
	Expect(apiextensionsv1.AddToScheme(scheme)).To(Succeed())

	// Set up the management cluster (kind or existing)
	By("Setting up management cluster")
	setupResult = testenv.SetupTestCluster(ctx, testenv.SetupTestClusterInput{
		E2EConfig:      e2eConfig,
		Scheme:         scheme,
		ArtifactFolder: artifactFolder,
	})
	Expect(setupResult).ToNot(BeNil(), "SetupTestCluster must return a result")
	Expect(setupResult.BootstrapClusterProxy).ToNot(BeNil(), "Bootstrap cluster proxy must not be nil")

	// Deploy cert-manager (Rancher dependency)
	By("Deploying cert-manager")
	certManagerInput := testenv.DeployCertManagerInput{
		BootstrapClusterProxy: setupResult.BootstrapClusterProxy,
	}
	Expect(turtlesframework.Parse(&certManagerInput)).To(Succeed())
	testenv.DeployCertManager(ctx, certManagerInput)

	// Deploy Rancher
	By("Deploying Rancher")
	rancherInput := testenv.DeployRancherInput{
		BootstrapClusterProxy: setupResult.BootstrapClusterProxy,
	}
	Expect(turtlesframework.Parse(&rancherInput)).To(Succeed())
	testenv.DeployRancher(ctx, rancherInput)

	// Rancher v2.14+ bundles Turtles (embedded-cluster-api=true), so we skip
	// the standalone Turtles install and let Rancher manage CAPI Operator.
	By("Skipping standalone Turtles install (embedded in Rancher v2.14+)")

	// Wait for core CAPI CRDs to be available (deployed by Rancher's embedded CAPI).
	By("Waiting for core CAPI CRDs to be available")
	Eventually(func() error {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		return setupResult.BootstrapClusterProxy.GetClient().Get(ctx,
			types.NamespacedName{Name: "clusters.cluster.x-k8s.io"},
			crd)
	}, e2eConfig.GetIntervals("default", "wait-controllers")...).Should(Succeed(),
		"Core CAPI CRDs must be registered by Rancher's embedded CAPI")

	// Deploy the evroc CAPI provider directly (no CAPI Operator in embedded mode).
	By("Deploying evroc CAPI provider")
	localImage := os.Getenv("E2E_LOCAL_IMAGE")

	// Load the locally-built image into kind when running in isolated mode.
	if localImage != "" {
		By(fmt.Sprintf("Loading local provider image %s into kind", localImage))
		kindCluster := os.Getenv("BOOTSTRAP_CLUSTER_NAME")
		if kindCluster == "" {
			kindCluster = setupResult.BootstrapClusterProxy.GetName()
		}
		loadCmd := exec.CommandContext(ctx, "kind", "load", "docker-image", localImage, "--name", kindCluster)
		out, err := loadCmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "kind load docker-image failed: %s", string(out))
	}

	evrocCredentialsSecret := buildEvrocCredentialsSecret()

	// Apply the namespace + credentials secret first.
	Expect(turtlesframework.Apply(ctx, setupResult.BootstrapClusterProxy, evrocCredentialsSecret)).To(Succeed(),
		"Failed to apply evroc credentials secret")

	// Apply CRDs directly.
	By("Applying evroc CRDs directly")
	kubectlBin := os.Getenv("CAPI_KUBECTL_PATH")
	if kubectlBin == "" {
		kubectlBin = "kubectl"
	}
	repoRoot := os.Getenv("REPO_ROOT")
	if repoRoot == "" {
		repoRoot = filepath.Join("..", "..", "..", "..")
	}
	// CRDs are generated into the Helm chart, not config/crd/bases.
	crdDir := filepath.Join(repoRoot, "helm", "cluster-api-provider-evroc", "crds")
	applyCmd := exec.CommandContext(ctx, kubectlBin,
		"--kubeconfig", setupResult.BootstrapClusterProxy.GetKubeconfigPath(),
		"apply", "-f", crdDir,
	)
	out, err := applyCmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "Failed to apply evroc CRDs: %s", string(out))

	// Apply the provider components (Deployment, RBAC, etc.) directly via kubectl.
	By("Applying provider components directly")
	componentsYAML := buildProviderComponentsYAML(localImage)
	componentsFile := filepath.Join(artifactFolder, "evroc-provider-components.yaml")
	Expect(os.WriteFile(componentsFile, []byte(componentsYAML), 0644)).To(Succeed())
	applyCmd = exec.CommandContext(ctx, kubectlBin,
		"--kubeconfig", setupResult.BootstrapClusterProxy.GetKubeconfigPath(),
		"apply", "-f", componentsFile,
	)
	out, err = applyCmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "Failed to apply provider components: %s", string(out))

	// Wait for the evroc provider deployment to be ready.
	By("Waiting for evroc provider deployment to be ready")
	Eventually(func() error {
		cmd := exec.CommandContext(ctx, kubectlBin,
			"--kubeconfig", setupResult.BootstrapClusterProxy.GetKubeconfigPath(),
			"rollout", "status", "deployment/cluster-api-provider-evroc-controller-manager",
			"-n", "capi-evroc-system", "--timeout=10s",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("deployment not ready: %s", string(out))
		}
		return nil
	}, e2eConfig.GetIntervals("default", "wait-controllers")...).Should(Succeed(),
		"evroc provider deployment must be ready")

	// Wait for all evroc CRDs to be established in the API server.
	// Use kubectl directly to avoid the cached REST mapper which doesn't see new CRDs.
	By("Waiting for all evroc CRDs to be established")
	evrocCRDs := []string{
		"evrocclusters.infrastructure.cluster.x-k8s.io",
		"evrocclustertemplates.infrastructure.cluster.x-k8s.io",
		"evrocmachines.infrastructure.cluster.x-k8s.io",
		"evrocmachinetemplates.infrastructure.cluster.x-k8s.io",
	}
	for _, crd := range evrocCRDs {
		crd := crd
		Eventually(func() error {
			cmd := exec.CommandContext(ctx, kubectlBin,
				"--kubeconfig", setupResult.BootstrapClusterProxy.GetKubeconfigPath(),
				"get", "crd", crd,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("CRD %s not ready: %s", crd, string(out))
			}
			return nil
		}, e2eConfig.GetIntervals("default", "wait-controllers")...).Should(Succeed(),
			"CRD not established: "+crd)
	}

	// Deploy CAPRKE2 bootstrap and control-plane providers.
	// Rancher's embedded CAPI deploys core controllers but not CAPRKE2.
	// The release YAMLs contain ${VARIABLE} placeholders (for clusterctl substitution),
	// so we replace them with defaults before applying via kubectl.
	By("Installing CAPRKE2 bootstrap and control-plane providers")
	caprke2Version := "v0.24.1"
	caprke2BaseURL := "https://github.com/rancher/cluster-api-provider-rke2/releases/download/" + caprke2Version
	caprke2Components := []string{
		"bootstrap-components.yaml",
		"control-plane-components.yaml",
	}
	for _, compName := range caprke2Components {
		compURL := caprke2BaseURL + "/" + compName
		GinkgoWriter.Printf("Downloading %s from %s\n", compName, compURL)
		dlCmd := exec.CommandContext(ctx, "curl", "-sL", compURL)
		body, err := dlCmd.Output()
		Expect(err).ToNot(HaveOccurred(), "Failed to download %s", compName)
		// Substitute clusterctl variables (${VAR:=default}) with their default values.
		// The release YAMLs use clusterctl-style variable placeholders that Kubernetes
		// does not process; we must replace them before applying.
		varPattern := regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*:=([^}]*)\}`)
		content := varPattern.ReplaceAllString(string(body), "$1")
		compFile := filepath.Join(artifactFolder, "caprke2-"+compName)
		Expect(os.WriteFile(compFile, []byte(content), 0644)).To(Succeed())
		applyCmd = exec.CommandContext(ctx, kubectlBin,
			"--kubeconfig", setupResult.BootstrapClusterProxy.GetKubeconfigPath(),
			"apply", "-f", compFile,
		)
		out, err = applyCmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "Failed to apply %s: %s", compName, string(out))
		GinkgoWriter.Printf("Applied %s successfully\n", compName)
	}

	// Wait for CAPRKE2 CRDs to be established before tests run.
	By("Waiting for CAPRKE2 CRDs to be established")
	caprke2CRDs := []string{
		"rke2controlplanes.controlplane.cluster.x-k8s.io",
		"rke2configtemplates.bootstrap.cluster.x-k8s.io",
	}
	for _, crd := range caprke2CRDs {
		crd := crd
		Eventually(func() error {
			cmd := exec.CommandContext(ctx, kubectlBin,
				"--kubeconfig", setupResult.BootstrapClusterProxy.GetKubeconfigPath(),
				"get", "crd", crd,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("CRD %s not ready: %s", crd, string(out))
			}
			return nil
		}, e2eConfig.GetIntervals("default", "wait-controllers")...).Should(Succeed(),
			"CRD not established: "+crd)
	}

	// Wait for CAPRKE2 webhook endpoints to be ready
	By("Waiting for CAPRKE2 webhook endpoints to be ready")
	webhookChecks := []struct {
		service   string
		namespace string
	}{
		{"rke2-bootstrap-webhook-service", "rke2-bootstrap-system"},
		{"rke2-control-plane-webhook-service", "rke2-control-plane-system"},
	}
	for _, check := range webhookChecks {
		check := check
		Eventually(func() error {
			cmd := exec.CommandContext(ctx, kubectlBin,
				"--kubeconfig", setupResult.BootstrapClusterProxy.GetKubeconfigPath(),
				"get", "endpoints", check.service, "-n", check.namespace,
				"-o", "jsonpath='{.subsets[*].addresses[*].ip}'",
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("webhook service %s not ready: %s", check.service, string(out))
			}
			// Check that the endpoint has at least one address
			if len(strings.TrimSpace(string(out))) < 3 { // At least 'x.x.x.x' with quotes
				return fmt.Errorf("webhook service %s has no endpoints", check.service)
			}
			return nil
		}, e2eConfig.GetIntervals("default", "wait-controllers")...).Should(Succeed(),
			"Webhook service not ready: "+check.service)
	}
})

// Cleanup after all tests
var _ = AfterSuite(func() {
	By("Collecting artifacts")
	if setupResult != nil && setupResult.KubeconfigPath != "" {
		if err := testenv.CollectArtifacts(ctx, testenv.CollectArtifactsInput{
			KubeconfigPath:  setupResult.KubeconfigPath,
			ArtifactsFolder: artifactFolder,
			Path:            "management-cluster",
		}); err != nil {
			GinkgoWriter.Printf("Warning: artifact collection failed: %v\n", err)
		}
	}

	By("Tearing down management cluster")
	if setupResult != nil {
		testenv.CleanupTestCluster(ctx, testenv.CleanupTestClusterInput{
			SetupTestClusterResult: *setupResult,
			ArtifactFolder:         artifactFolder,
		})
	}

	cancelFunc()
})

var _ = Describe("[evroc] Rancher Turtles Integration", Label("rancher-turtles"), func() {
	BeforeEach(func() {
		Expect(setupResult).ToNot(BeNil(), "Test setup must complete before running tests")
		Expect(setupResult.BootstrapClusterProxy).ToNot(BeNil())
	})

	Context("Shared Cluster Lifecycle", func() {
		It("Should import, scale, remediate, and delete an evroc cluster", func() {
			clusterName := fmt.Sprintf("evroc-e2e-%s", randomSuffix())

			By("Creating cluster resources via clusterctl generate")
			clusterYAML := generateClusterYAML(clusterName)

			By("Applying cluster resources to management cluster")
			Expect(applyYAML(setupResult.BootstrapClusterProxy, clusterYAML)).To(Succeed())

			By("Waiting for cluster control plane to be ready")
			waitForClusterControlPlaneReady(ctx, clusterName, "default")

			By("Verifying cluster is provisioned and healthy")
			turtlesframework.VerifyCluster(ctx, turtlesframework.VerifyClusterInput{
				BootstrapClusterProxy:   setupResult.BootstrapClusterProxy,
				Name:                    clusterName,
				DeleteAfterVerification: false,
			})

			By(fmt.Sprintf("Cluster %s successfully provisioned and imported into Rancher", clusterName))

			By("Scaling MachineDeployment to 1 worker")
			patchMachineDeploymentReplicas(ctx, clusterName, "default", 1)
			waitForMachineDeploymentReady(ctx, clusterName, "default", 1)

			originalMachine := getMachineDeploymentMachineNames(ctx, clusterName, "default")
			Expect(originalMachine).To(HaveLen(1), "Expected exactly 1 worker machine")

			By(fmt.Sprintf("Deleting worker Machine %s to trigger remediation", originalMachine[0]))
			deleteMachine(ctx, originalMachine[0], "default")
			waitForMachineReplacement(ctx, clusterName, "default", originalMachine[0], 1)
			waitForMachineDeploymentReady(ctx, clusterName, "default", 1)

			By("Scaling MachineDeployment back to 0 workers")
			patchMachineDeploymentReplicas(ctx, clusterName, "default", 0)
			waitForMachineDeploymentReady(ctx, clusterName, "default", 0)

			By("Deleting the shared workload cluster")
			deleteCluster(ctx, clusterName, "default")
			waitForClusterResourcesDeleted(ctx, clusterName, "default")
		})
	})

	Context("Provider Installation", func() {
		It("Should have the evroc provider running", func() {
			By("Verifying evroc provider deployment is available")
			clusterProxy := setupResult.BootstrapClusterProxy
			Expect(clusterProxy).ToNot(BeNil())

			// The provider was deployed in BeforeSuite - verify it's running
			turtlesframework.WaitForCAPIProviderRollout(ctx, turtlesframework.WaitForCAPIProviderRolloutInput{
				Getter:    clusterProxy.GetClient(),
				Name:      "evroc",
				Namespace: "capi-evroc-system",
			}, e2eConfig.GetIntervals("default", "wait-controllers")...)

			By("Verifying CRDs are installed")
			Expect(clusterProxy.GetClient()).ToNot(BeNil())

			// Log expected CRDs - their presence is guaranteed by the successful provider deployment in BeforeSuite
			for _, crd := range []string{
				"evrocclusters.infrastructure.cluster.x-k8s.io",
				"evrocmachines.infrastructure.cluster.x-k8s.io",
				"evrocmachinetemplates.infrastructure.cluster.x-k8s.io",
			} {
				verifyCRDExists(ctx, crd)
			}
		})
	})

	Context("Webhook Validation", func() {
		It("Should reject an EvrocCluster with an invalid region", func() {
			By("Applying an EvrocCluster with region 'invalid-region-format'")
			invalidClusterYAML := []byte(fmt.Sprintf(`
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocCluster
metadata:
  name: evroc-webhook-test-%s
  namespace: default
spec:
  project: %s
  region: invalid-region-format
  credentialsRef:
    name: evroc-credentials
  failureDomains: ["a"]
`, randomSuffix(), os.Getenv("EVROC_PROJECT")))

			err := applyYAML(setupResult.BootstrapClusterProxy, invalidClusterYAML)
			Expect(err).To(HaveOccurred(), "Webhook must reject EvrocCluster with invalid region format")
			Expect(err.Error()).To(ContainSubstring("region"),
				"Error message should mention the invalid region")
		})

		It("Should reject an EvrocMachine with an invalid compute profile", func() {
			By("Applying an EvrocMachine with compute profile 'nonexistent-flavor'")
			invalidMachineYAML := []byte(fmt.Sprintf(`
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocMachine
metadata:
  name: evroc-webhook-test-%s
  namespace: default
spec:
  project: %s
  region: se-sto
  computeProfile: nonexistent-flavor
  image: ubuntu.22-04.1
  rootDiskSize: 50
`, randomSuffix(), os.Getenv("EVROC_PROJECT")))

			err := applyYAML(setupResult.BootstrapClusterProxy, invalidMachineYAML)
			Expect(err).To(HaveOccurred(), "Webhook must reject EvrocMachine with invalid compute profile")
			Expect(err.Error()).To(ContainSubstring("computeProfile"),
				"Error message should mention the invalid compute profile")
		})

		It("Should reject an EvrocMachine with an invalid image", func() {
			By("Applying an EvrocMachine with image 'nonexistent.image'")
			invalidMachineYAML := []byte(fmt.Sprintf(`
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocMachine
metadata:
  name: evroc-webhook-test-%s
  namespace: default
spec:
  project: %s
  region: se-sto
  computeProfile: a1a.s
  image: nonexistent.image
  rootDiskSize: 50
`, randomSuffix(), os.Getenv("EVROC_PROJECT")))

			err := applyYAML(setupResult.BootstrapClusterProxy, invalidMachineYAML)
			Expect(err).To(HaveOccurred(), "Webhook must reject EvrocMachine with invalid image")
			Expect(err.Error()).To(ContainSubstring("image"),
				"Error message should mention the invalid image")
		})
	})
})

// loadCredentialsFile loads EVROC credentials from test/e2e/config.yaml into the process environment.
// It looks for the file at EVROC_CREDENTIALS_FILE env var, or defaults to
// test/e2e/config.yaml relative to REPO_ROOT (or the working directory).
// Only sets env vars that are not already set — env vars always take precedence.
// Credentials are read from the variables map.
func loadCredentialsFile() {
	credFile := os.Getenv("EVROC_CREDENTIALS_FILE")
	if credFile == "" {
		repoRoot := os.Getenv("REPO_ROOT")
		if repoRoot == "" {
			repoRoot = filepath.Join("..", "..", "..", "..")
		}
		credFile = filepath.Join(repoRoot, "test", "e2e", "config.yaml")
	}
	data, err := os.ReadFile(credFile)
	if err != nil {
		// Not an error if file doesn't exist — credentials via env vars is fine.
		return
	}

	cfg := map[string]map[string]string{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return
	}

	for key, val := range cfg["variables"] {
		if os.Getenv(key) == "" && val != "" {
			_ = os.Setenv(key, val)
		}
	}
}

// buildEvrocCredentialsSecret returns the YAML for a secret holding evroc API credentials.
// The secret uses the service-account keys (serviceAccountID, serviceAccountSecret,
// organization) referenced by an EvrocCluster's spec.credentialsRef. Project and
// region come from the EvrocCluster spec, not the secret.
func buildEvrocCredentialsSecret() []byte {
	saID := os.Getenv("EVROC_SERVICE_ACCOUNT_ID")
	saSecret := os.Getenv("EVROC_SERVICE_ACCOUNT_SECRET")
	organization := os.Getenv("EVROC_ORGANIZATION")

	stringData := fmt.Sprintf("  serviceAccountID: %q\n  serviceAccountSecret: %q\n  organization: %q\n",
		saID, saSecret, organization)

	return []byte(fmt.Sprintf(`
apiVersion: v1
kind: Namespace
metadata:
  name: capi-evroc-system
---
apiVersion: v1
kind: Secret
metadata:
  name: evroc-credentials
  namespace: capi-evroc-system
type: Opaque
stringData:
%s---
apiVersion: v1
kind: Secret
metadata:
  name: evroc-credentials
  namespace: %s
type: Opaque
stringData:
%s`, stringData, "default", stringData))
}

// buildProviderComponentsYAML reads the infrastructure-components.yaml, strips
// CRDs (applied separately), and patches the image for local mode.
func buildProviderComponentsYAML(localImage string) string {
	repoRoot := os.Getenv("REPO_ROOT")
	if repoRoot == "" {
		repoRoot = filepath.Join("..", "..", "..", "..")
	}

	componentsPath := filepath.Join(repoRoot, "templates", "infrastructure-components.yaml")
	componentsBytes, err := os.ReadFile(componentsPath)
	Expect(err).ToNot(HaveOccurred(), "reading infrastructure-components.yaml")

	// Strip CRDs — they are applied directly via kubectl before this step.
	docSeparator := regexp.MustCompile(`(?m)^---[ \t]*$`)
	crdKindPattern := regexp.MustCompile(`(?m)^kind:[ \t]*CustomResourceDefinition[ \t]*$`)
	docs := docSeparator.Split(string(componentsBytes), -1)
	var kept []string
	for _, doc := range docs {
		trimmed := strings.TrimSpace(doc)
		if trimmed == "" || crdKindPattern.MatchString(trimmed) {
			continue
		}
		kept = append(kept, trimmed)
	}
	components := "---\n" + strings.Join(kept, "\n---\n") + "\n"

	if localImage != "" {
		providerImagePattern := regexp.MustCompile(`ghcr\.io/evroc-oss/cluster-api-provider-evroc:[^\s"']+`)
		components = providerImagePattern.ReplaceAllString(components, localImage)
		components = strings.ReplaceAll(components,
			"imagePullPolicy: IfNotPresent",
			"imagePullPolicy: Never")
	}

	return components
}

// verifyCRDExists asserts that the named CRD exists in the API server.
func verifyCRDExists(ctx context.Context, crdName string) {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	Expect(setupResult.BootstrapClusterProxy.GetClient().Get(ctx,
		types.NamespacedName{Name: crdName}, crd)).To(Succeed(),
		"CRD must exist: "+crdName)
}

// randomSuffix returns a short hex string derived from the Ginkgo random seed.
func randomSuffix() string {
	return fmt.Sprintf("%08x", GinkgoRandomSeed())
}

// evrocAvailabilityZone returns the evroc availability zone from env var, defaulting to "a".
func evrocAvailabilityZone() string {
	if az := os.Getenv("EVROC_AVAILABILITY_ZONE"); az != "" {
		return az
	}
	return "a"
}

// waitForClusterControlPlaneReady waits for the cluster's ControlPlaneReady status using Eventually.
// VerifyCluster uses Consistently (expects cluster to already be ready), so this must be called first.
func waitForClusterControlPlaneReady(waitCtx context.Context, clusterName, namespace string) {
	Eventually(func() error {
		cluster := &clusterv1.Cluster{}
		if err := setupResult.BootstrapClusterProxy.GetClient().Get(waitCtx,
			types.NamespacedName{Name: clusterName, Namespace: namespace}, cluster); err != nil {
			return err
		}
		// Check if control plane is available using v1beta2 conditions
		cpReady := false
		for _, cond := range cluster.Status.Conditions {
			if cond.Type == "ControlPlaneAvailable" && cond.Status == metav1.ConditionTrue {
				cpReady = true
				break
			}
		}
		if !cpReady {
			return fmt.Errorf("cluster %s/%s control plane not ready", namespace, clusterName)
		}
		return nil
	}, e2eConfig.GetIntervals("default", "wait-cluster")...).Should(Succeed(),
		"Timed out waiting for cluster %s control plane to be ready", clusterName)
}

// waitForClusterResourcesDeleted verifies that CAPI and Evroc resources associated
// with a cluster are gone from the management cluster after deletion.
func waitForClusterResourcesDeleted(waitCtx context.Context, clusterName, namespace string) {
	kubeconfigPath := setupResult.BootstrapClusterProxy.GetKubeconfigPath()

	resourceKinds := []string{
		"machines.cluster.x-k8s.io",
		"machinedeployments.cluster.x-k8s.io",
		"evrocmachines.infrastructure.cluster.x-k8s.io",
		"evrocmachinetemplates.infrastructure.cluster.x-k8s.io",
	}

	Eventually(func() error {
		for _, named := range []string{
			"cluster.cluster.x-k8s.io/" + clusterName,
			"evroccluster.infrastructure.cluster.x-k8s.io/" + clusterName,
		} {
			cmd := exec.CommandContext(waitCtx, "kubectl",
				"--kubeconfig", kubeconfigPath,
				"-n", namespace,
				"get", named,
			)
			if out, err := cmd.CombinedOutput(); err == nil {
				return fmt.Errorf("%s still exists: %s", named, strings.TrimSpace(string(out)))
			}
		}

		for _, kind := range resourceKinds {
			cmd := exec.CommandContext(waitCtx, "kubectl",
				"--kubeconfig", kubeconfigPath,
				"-n", namespace,
				"get", kind,
				"-l", fmt.Sprintf("cluster.x-k8s.io/cluster-name=%s", clusterName),
				"-o", "name",
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("failed listing %s: %w (%s)", kind, err, strings.TrimSpace(string(out)))
			}
			if strings.TrimSpace(string(out)) != "" {
				return fmt.Errorf("resources still present in %s: %s", kind, strings.TrimSpace(string(out)))
			}
		}

		return nil
	}, e2eConfig.GetIntervals("default", "wait-cluster")...).Should(Succeed(),
		"Timed out waiting for cluster %s resources to be fully deleted", clusterName)
}

// generateClusterYAML runs clusterctl generate cluster for the RKE2 template and
// returns the resulting manifest bytes. It writes the output to artifactFolder
// for debugging.
func generateClusterYAML(clusterName string) []byte {
	outputFile := filepath.Join(artifactFolder, clusterName+".yaml")
	templatePath := filepath.Join("..", "..", "..", "..", "templates", "cluster-template-rke2.yaml")

	cmd := exec.CommandContext(ctx, clusterctlBinaryPath(),
		"generate", "cluster", clusterName,
		"--from", templatePath,
		"--target-namespace", "default")

	cmd.Env = os.Environ()
	for name, val := range map[string]string{
		"CLUSTER_NAME":                clusterName,
		"EVROC_PROJECT":               os.Getenv("EVROC_PROJECT"),
		"EVROC_REGION":                os.Getenv("EVROC_REGION"),
		"EVROC_AVAILABILITY_ZONE":     evrocAvailabilityZone(),
		"KUBERNETES_VERSION":          e2eConfig.MustGetVariable("KUBERNETES_VERSION"),
		"CONTROL_PLANE_MACHINE_COUNT": "1",
		"WORKER_MACHINE_COUNT":        "0",
		"XDG_CONFIG_HOME":             filepath.Join(artifactFolder, "xdg"),
	} {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", name, val))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	Expect(cmd.Run()).To(Succeed(), fmt.Sprintf("clusterctl generate failed: %s", stderr.String()))

	Expect(os.WriteFile(outputFile, stdout.Bytes(), os.ModePerm)).To(Succeed(),
		"Failed writing template to file")

	clusterYAML := stdout.Bytes()
	Expect(clusterYAML).ToNot(BeEmpty(), "Generated cluster YAML must not be empty")
	return clusterYAML
}

// clusterctlBinaryPath returns the path to the clusterctl binary.
// It uses CLUSTERCTL_BINARY_PATH env var if set, otherwise assumes it's on PATH.
func clusterctlBinaryPath() string {
	if p := os.Getenv("CLUSTERCTL_BINARY_PATH"); p != "" {
		return p
	}
	// Default: expect clusterctl to be in PATH - look for it
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		candidate := filepath.Join(dir, "clusterctl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "clusterctl"
}

// generateClusterYAMLWithWorkers runs clusterctl generate cluster with a specific worker count.
func generateClusterYAMLWithWorkers(clusterName string, workerCount int) []byte {
	outputFile := filepath.Join(artifactFolder, clusterName+".yaml")
	templatePath := filepath.Join("..", "..", "..", "..", "templates", "cluster-template-rke2.yaml")

	cmd := exec.CommandContext(ctx, clusterctlBinaryPath(),
		"generate", "cluster", clusterName,
		"--from", templatePath,
		"--target-namespace", "default")

	cmd.Env = os.Environ()
	for name, val := range map[string]string{
		"CLUSTER_NAME":                clusterName,
		"EVROC_PROJECT":               os.Getenv("EVROC_PROJECT"),
		"EVROC_REGION":                os.Getenv("EVROC_REGION"),
		"EVROC_AVAILABILITY_ZONE":     evrocAvailabilityZone(),
		"KUBERNETES_VERSION":          e2eConfig.MustGetVariable("KUBERNETES_VERSION"),
		"CONTROL_PLANE_MACHINE_COUNT": "1",
		"WORKER_MACHINE_COUNT":        fmt.Sprintf("%d", workerCount),
		"XDG_CONFIG_HOME":             filepath.Join(artifactFolder, "xdg"),
	} {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", name, val))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	Expect(cmd.Run()).To(Succeed(), fmt.Sprintf("clusterctl generate failed: %s", stderr.String()))

	Expect(os.WriteFile(outputFile, stdout.Bytes(), os.ModePerm)).To(Succeed(),
		"Failed writing template to file")

	clusterYAML := stdout.Bytes()
	Expect(clusterYAML).ToNot(BeEmpty(), "Generated cluster YAML must not be empty")
	return clusterYAML
}

// patchMachineDeploymentReplicas patches the MachineDeployment replica count.
func patchMachineDeploymentReplicas(patchCtx context.Context, clusterName, namespace string, replicas int) {
	kubeconfigPath := setupResult.BootstrapClusterProxy.GetKubeconfigPath()
	mdName := clusterName + "-md-0"
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)

	cmd := exec.CommandContext(patchCtx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", namespace,
		"patch", "machinedeployment", mdName,
		"--type=merge", "-p", patch,
	)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "Failed to patch MachineDeployment replicas: %s", string(out))
}

// waitForMachineDeploymentReady waits until the MachineDeployment has the expected number of ready replicas.
func waitForMachineDeploymentReady(waitCtx context.Context, clusterName, namespace string, expectedReplicas int) {
	kubeconfigPath := setupResult.BootstrapClusterProxy.GetKubeconfigPath()
	mdName := clusterName + "-md-0"

	Eventually(func() error {
		cmd := exec.CommandContext(waitCtx, "kubectl",
			"--kubeconfig", kubeconfigPath,
			"-n", namespace,
			"get", "machinedeployment", mdName,
			"-o", "jsonpath={.spec.replicas},{.status.replicas},{.status.readyReplicas}",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to get MachineDeployment status: %s", string(out))
		}

		output := strings.TrimSpace(string(out))
		// Format: spec.replicas,status.replicas,status.readyReplicas
		// When scaling to 0, status fields may be absent or 0
		parts := strings.Split(output, ",")
		if len(parts) != 3 {
			return fmt.Errorf("unexpected MachineDeployment %s status format: %q", mdName, output)
		}

		wantStr := fmt.Sprintf("%d", expectedReplicas)

		// spec.replicas must match
		if parts[0] != wantStr {
			return fmt.Errorf("MachineDeployment %s spec.replicas=%s, want %s", mdName, parts[0], wantStr)
		}

		// For scale-to-zero, status fields may be empty or "0"
		if expectedReplicas == 0 {
			replicasOK := parts[1] == "" || parts[1] == "0"
			readyOK := parts[2] == "" || parts[2] == "0"
			if !replicasOK || !readyOK {
				return fmt.Errorf("MachineDeployment %s not scaled to 0: status.replicas=%s, status.readyReplicas=%s", mdName, parts[1], parts[2])
			}
			return nil
		}

		// For scale-up, require status.readyReplicas to match
		if parts[2] != wantStr {
			return fmt.Errorf("MachineDeployment %s status.readyReplicas=%s, want %s (status.replicas=%s)", mdName, parts[2], wantStr, parts[1])
		}
		return nil
	}, e2eConfig.GetIntervals("default", "wait-cluster")...).Should(Succeed(),
		"Timed out waiting for MachineDeployment %s to have %d ready replicas", mdName, expectedReplicas)
}

// getMachineDeploymentMachineNames returns the names of Machines owned by the cluster's MachineDeployment.
func getMachineDeploymentMachineNames(getCtx context.Context, clusterName, namespace string) []string {
	kubeconfigPath := setupResult.BootstrapClusterProxy.GetKubeconfigPath()

	cmd := exec.CommandContext(getCtx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", namespace,
		"get", "machines.cluster.x-k8s.io",
		"-l", fmt.Sprintf("cluster.x-k8s.io/cluster-name=%s,cluster.x-k8s.io/deployment-name=%s-md-0", clusterName, clusterName),
		"-o", "jsonpath={.items[*].metadata.name}",
	)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "Failed to list worker machines: %s", string(out))

	names := strings.Fields(strings.TrimSpace(string(out)))
	return names
}

// deleteMachine deletes a specific CAPI Machine by name.
func deleteMachine(delCtx context.Context, machineName, namespace string) {
	kubeconfigPath := setupResult.BootstrapClusterProxy.GetKubeconfigPath()

	cmd := exec.CommandContext(delCtx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", namespace,
		"delete", "machine.cluster.x-k8s.io", machineName,
	)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "Failed to delete machine %s: %s", machineName, string(out))
}

// waitForMachineReplacement waits until the deleted machine is gone and a new one exists.
func waitForMachineReplacement(waitCtx context.Context, clusterName, namespace, deletedMachineName string, expectedCount int) {
	Eventually(func() error {
		machines := getMachineDeploymentMachineNames(waitCtx, clusterName, namespace)
		if len(machines) != expectedCount {
			return fmt.Errorf("expected %d machines, got %d: %v", expectedCount, len(machines), machines)
		}
		for _, m := range machines {
			if m == deletedMachineName {
				return fmt.Errorf("deleted machine %s still present", deletedMachineName)
			}
		}
		return nil
	}, e2eConfig.GetIntervals("default", "wait-cluster")...).Should(Succeed(),
		"Timed out waiting for machine %s to be replaced", deletedMachineName)
}

// deleteCluster deletes a CAPI Cluster by name.
func deleteCluster(delCtx context.Context, clusterName, namespace string) {
	kubeconfigPath := setupResult.BootstrapClusterProxy.GetKubeconfigPath()

	cmd := exec.CommandContext(delCtx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", namespace,
		"delete", "cluster.cluster.x-k8s.io", clusterName,
	)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "Failed to delete cluster %s: %s", clusterName, string(out))
}

// applyYAML applies raw YAML bytes to the management cluster using kubectl directly,
// capturing full stdout/stderr for debugging when turtlesframework.Apply swallows errors.
func applyYAML(clusterProxy capiframework.ClusterProxy, yamlBytes []byte) error {
	kubeconfigPath := clusterProxy.GetKubeconfigPath()
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", kubeconfigPath, "-f", "-", "--validate=false")
	cmd.Stdin = bytes.NewReader(yamlBytes)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdout.Len() > 0 {
		klog.Infof("kubectl apply stdout:\n%s", stdout.String())
	}
	if stderr.Len() > 0 {
		klog.Infof("kubectl apply stderr:\n%s", stderr.String())
	}
	if err != nil {
		return fmt.Errorf("kubectl apply failed: %w\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	return nil
}
