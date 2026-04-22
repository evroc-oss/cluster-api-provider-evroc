//go:build e2e
// +build e2e

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

// Package capi_test implements the upstream CAPI QuickStart E2E spec for the
// evroc infrastructure provider. It validates the provider meets CAPI upstream
// E2E requirements without Rancher/Turtles dependencies.
//
// See: https://cluster-api.sigs.k8s.io/developer/core/e2e
//
// Run via: make test-e2e-capi
package capi_test

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capiframework "sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/yaml"
)

// ─── Simple E2E config (no providers section required) ────────────────────────

// capiE2EConfig is a minimal config that reads intervals and variables from
// capi-e2e-config.yaml. We intentionally avoid clusterctlcfg.LoadE2EConfig
// because it requires a full providers section (core + bootstrap + cp + infra)
// which we don't need since we run clusterctl init manually.
type capiE2EConfig struct {
	Intervals map[string][]string `yaml:"intervals"`
	Variables map[string]string   `yaml:"variables"`
}

func loadCAPIE2EConfig(path string) *capiE2EConfig {
	data, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred(), "Failed to read CAPI E2E config: %s", path)
	cfg := &capiE2EConfig{}
	Expect(yaml.Unmarshal(data, cfg)).To(Succeed(), "Failed to parse CAPI E2E config")
	return cfg
}

// MustGetVariable returns the variable value from the config file first,
// falling back to the OS env; fails if unset in both.
func (c *capiE2EConfig) MustGetVariable(name string) string {
	if v, ok := c.Variables[name]; ok && v != "" {
		return v
	}
	if v := os.Getenv(name); v != "" {
		return v
	}
	Fail(fmt.Sprintf("required variable %q is not set", name))
	return ""
}

// GetVariable returns the variable value from the config file first,
// falling back to the OS env, or empty string.
func (c *capiE2EConfig) GetVariable(name string) string {
	if v, ok := c.Variables[name]; ok && v != "" {
		return v
	}
	return os.Getenv(name)
}

// GetIntervals returns the interval pair (timeout, poll) for the given key.
func (c *capiE2EConfig) GetIntervals(group, name string) []interface{} {
	key := group + "/" + name
	if pair, ok := c.Intervals[key]; ok && len(pair) == 2 {
		return []interface{}{pair[0], pair[1]}
	}
	return []interface{}{"30m", "30s"}
}

// ─── Suite globals ────────────────────────────────────────────────────────────

// Test suite entry point
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "evroc CAPI QuickStart E2E Suite")
}

var (
	ctx            context.Context
	cancelFunc     context.CancelFunc
	e2eConfig      *capiE2EConfig
	artifactFolder string
	repoRoot       string

	// kindClusterName is the name of the kind management cluster.
	kindClusterName string
	// kubeconfigPath is the absolute path to the kind cluster kubeconfig.
	kubeconfigPath string
	// clusterProxy wraps the kind bootstrap cluster.
	clusterProxy capiframework.ClusterProxy
)

var _ = BeforeSuite(func() {
	klog.SetOutput(GinkgoWriter)

	loadCredentialsFile()

	ctx, cancelFunc = context.WithCancel(context.Background())

	// Resolve repo root
	repoRoot = os.Getenv("REPO_ROOT")
	if repoRoot == "" {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		Expect(err).NotTo(HaveOccurred(), "Failed to find repo root")
		repoRoot = strings.TrimSpace(string(out))
	}

	// Load E2E config
	configPath := os.Getenv("CAPI_E2E_CONFIG_PATH")
	if configPath == "" {
		configPath = filepath.Join(repoRoot, "test", "e2e", "capi-e2e-config.yaml")
	}
	e2eConfig = loadCAPIE2EConfig(configPath)

	artifactFolder = os.Getenv("ARTIFACTS_FOLDER")
	if artifactFolder == "" {
		artifactFolder = filepath.Join(os.TempDir(), "capi-evroc-quickstart-artifacts")
	}
	Expect(os.MkdirAll(artifactFolder, 0750)).To(Succeed())

	// Create kind bootstrap cluster
	kindClusterName = "evroc-capi-quickstart"
	if n := os.Getenv("BOOTSTRAP_CLUSTER_NAME"); n != "" {
		kindClusterName = n
	}
	kubeconfigPath = filepath.Join(artifactFolder, "bootstrap-kubeconfig.yaml")

	By("Creating kind bootstrap cluster: " + kindClusterName)
	createKindCluster(ctx, kindClusterName, kubeconfigPath)

	// Pre-load the provider image into kind so the pod doesn't need to pull
	// from the registry (which may not be accessible or may not have the image yet).
	By("Loading provider image into kind")
	loadProviderImageIntoKind(ctx, kindClusterName)

	// Build cluster proxy from the kubeconfig
	runtimeScheme := runtime.NewScheme()
	capiframework.TryAddDefaultSchemes(runtimeScheme)
	clusterProxy = capiframework.NewClusterProxy(kindClusterName, kubeconfigPath, runtimeScheme)
	Expect(clusterProxy).ToNot(BeNil())

	// Pre-create the namespace and credentials secret so the provider pod
	// can start successfully after clusterctl init.
	By("Pre-creating evroc credentials secret")
	applyEvrocCredentials(ctx, kubeconfigPath)

	// Apply evroc CRDs directly before clusterctl init to avoid race conditions.
	By("Applying evroc CRDs")
	applyEvrocCRDs(ctx, kubeconfigPath)

	// Run clusterctl init to install CAPI core + kubeadm + evroc provider.
	By("Running clusterctl init --infrastructure evroc")
	clusterctlInit(ctx, kubeconfigPath, true)

	// Wait for all provider deployments to become ready.
	// The kubeadm control plane webhook must be serving before we can apply clusters.
	By("Waiting for evroc provider deployment to be ready")
	waitForDeploymentReady(ctx, kubeconfigPath, "capi-evroc-system", "infrastructure-evroc", 10*time.Minute)
	By("Waiting for CAPI core provider to be ready")
	waitForDeploymentReady(ctx, kubeconfigPath, "capi-evroc-system", "cluster-api", 5*time.Minute)
	By("Waiting for kubeadm control plane provider to be ready")
	waitForDeploymentReady(ctx, kubeconfigPath, "capi-evroc-system", "control-plane-kubeadm", 5*time.Minute)
	By("Waiting for kubeadm bootstrap provider to be ready")
	waitForDeploymentReady(ctx, kubeconfigPath, "capi-evroc-system", "bootstrap-kubeadm", 5*time.Minute)
})

var _ = AfterSuite(func() {
	cancelFunc()

	if os.Getenv("SKIP_CLUSTER_DELETE") == "true" {
		GinkgoWriter.Printf("Skipping kind cluster deletion (SKIP_CLUSTER_DELETE=true)\n")
		return
	}
	By("Deleting kind bootstrap cluster")
	deleteKindCluster(kindClusterName)
})

var _ = Describe("[evroc] CAPI QuickStart", Label("capi-quickstart"), func() {
	BeforeEach(func() {
		Expect(clusterProxy).ToNot(BeNil(), "bootstrap cluster proxy must be initialized")
	})

	Context("QuickStart", func() {
		It("Should create a workload cluster, verify it is provisioned, then delete it", func() {
			clusterName := fmt.Sprintf("evroc-qs-%s", randomSuffix())
			namespace := "default"

			By("Generating cluster manifest from default template")
			clusterYAML := generateKubeadmClusterYAML(clusterName)

			By("Applying cluster resources")
			applyManifest(ctx, kubeconfigPath, clusterYAML, clusterName)

			defer func() {
				// Collect logs, machine status, and events BEFORE deletion for CI/CD debugging
				collectClusterArtifacts(ctx, kubeconfigPath, clusterName, namespace)

				By("Deleting workload cluster " + clusterName)
				deleteCluster(ctx, kubeconfigPath, clusterName, namespace)

				By("Verifying cluster resources are cleaned up after deletion")
				verifyClusterDeleted(ctx, kubeconfigPath, clusterName, namespace)
			}()

			By("Waiting for EvrocCluster to become ready")
			waitForEvrocClusterReady(ctx, kubeconfigPath, clusterName, namespace,
				e2eConfig.GetIntervals("default", "wait-cluster")...)

			By("Waiting for control plane machine to be provisioned")
			waitForMachineProvisioned(ctx, kubeconfigPath, clusterName, namespace,
				e2eConfig.GetIntervals("default", "wait-machines")...)

			By("Verifying EvrocCluster security group status has role-based SGs")
			verifyClusterSGStatus(ctx, kubeconfigPath, clusterName, namespace)

			By("Verifying EvrocCluster control plane endpoint")
			verifyControlPlaneEndpoint(ctx, kubeconfigPath, clusterName, namespace)

			By("Verifying EvrocMachine addresses and providerID")
			verifyMachineStatus(ctx, kubeconfigPath, clusterName, namespace)

			By("Cluster provisioned successfully — QuickStart PASSED")
		})
	})

	// ClusterctlMove requires a CNI on the single-node workload cluster for
	// cert-manager to become available. Disabled until the test installs a CNI
	// or uses a multi-node cluster.
	Context("ClusterctlMove", Label("clusterctl-move"), func() {
		PIt("Should pivot a workload cluster to self-management without deleting VMs", func() {
			clusterName := fmt.Sprintf("evroc-mv-%s", randomSuffix())
			namespace := "default"

			// ── Phase 1: Create the workload cluster on the bootstrap (kind) management cluster ──
			By("Generating cluster manifest from minimal template")
			clusterYAML := generateKubeadmClusterYAML(clusterName)

			By("Applying cluster resources to bootstrap management cluster")
			applyManifest(ctx, kubeconfigPath, clusterYAML, clusterName)

			// We'll track which kubeconfig owns the cluster for cleanup.
			// Starts as the bootstrap cluster; after move it becomes the workload cluster.
			ownerKubeconfig := kubeconfigPath
			defer func() {
				collectClusterArtifacts(ctx, ownerKubeconfig, clusterName, namespace)

				By("Deleting workload cluster from current owner")
				deleteCluster(ctx, ownerKubeconfig, clusterName, namespace)
			}()

			By("Waiting for EvrocCluster to become ready")
			waitForEvrocClusterReady(ctx, kubeconfigPath, clusterName, namespace,
				e2eConfig.GetIntervals("default", "wait-cluster")...)

			By("Waiting for EvrocMachine to have providerID (VM ready in evroc)")
			waitForEvrocMachineReady(ctx, kubeconfigPath, clusterName, namespace,
				e2eConfig.GetIntervals("default", "wait-machines")...)

			// ── Phase 2: Get the workload cluster kubeconfig ──
			By("Waiting for workload cluster kubeconfig secret")
			workloadKubeconfig := filepath.Join(artifactFolder, clusterName+"-kubeconfig.yaml")
			waitForWorkloadKubeconfig(ctx, kubeconfigPath, clusterName, namespace, workloadKubeconfig,
				e2eConfig.GetIntervals("default", "wait-machines")...)

			By("Waiting for workload cluster API server to be reachable")
			waitForAPIServerReachable(ctx, workloadKubeconfig, 15*time.Minute)

			// ── Phase 3: Snapshot VM IDs before the move ──
			By("Collecting EvrocMachine providerIDs before move")
			providerIDsBefore := getEvrocMachineProviderIDs(ctx, kubeconfigPath, clusterName, namespace)
			Expect(providerIDsBefore).NotTo(BeEmpty(), "should have at least one EvrocMachine with providerID")
			GinkgoWriter.Printf("Provider IDs before move: %v\n", providerIDsBefore)

			// ── Phase 4: Install CAPI + evroc provider on the workload cluster ──
			By("Pre-loading provider image into workload cluster")
			loadProviderImageToRemoteCluster(ctx, workloadKubeconfig)

			By("Pre-creating evroc credentials on workload cluster")
			applyEvrocCredentials(ctx, workloadKubeconfig)

			By("Applying evroc CRDs on workload cluster")
			applyEvrocCRDs(ctx, workloadKubeconfig)

			By("Running clusterctl init on workload cluster")
			clusterctlInit(ctx, workloadKubeconfig, true)

			By("Waiting for evroc provider deployment on workload cluster")
			waitForDeploymentReady(ctx, workloadKubeconfig, "capi-evroc-system", "infrastructure-evroc", 10*time.Minute)

			// ── Phase 5: clusterctl move (pivot) ──
			By("Running clusterctl move from bootstrap to workload cluster")
			clusterctlMove(ctx, kubeconfigPath, workloadKubeconfig, namespace)

			// Ownership has moved to the workload cluster.
			ownerKubeconfig = workloadKubeconfig

			// ── Phase 6: Verify resources landed on the target cluster ──
			By("Verifying Cluster resource exists on target")
			verifyResourceExists(ctx, workloadKubeconfig, "cluster", clusterName, namespace)

			By("Verifying EvrocCluster resource exists on target")
			verifyResourceExists(ctx, workloadKubeconfig, "evroccluster", clusterName, namespace)

			By("Verifying EvrocMachine resources exist on target")
			providerIDsAfter := getEvrocMachineProviderIDs(ctx, workloadKubeconfig, clusterName, namespace)
			Expect(providerIDsAfter).NotTo(BeEmpty(), "EvrocMachines should exist on target cluster")

			// ── Phase 7: Verify VMs survived the move ──
			By("Verifying VM providerIDs are identical before and after move")
			Expect(providerIDsAfter).To(ConsistOf(providerIDsBefore),
				"VMs must not be deleted/recreated during clusterctl move")

			By("Verifying resources were removed from source bootstrap cluster")
			verifyResourceGone(ctx, kubeconfigPath, "cluster", clusterName, namespace)

			By("Verifying cluster is functional after move — machines still Running")
			waitForMachineProvisioned(ctx, workloadKubeconfig, clusterName, namespace,
				e2eConfig.GetIntervals("default", "wait-machines")...)

			By("clusterctl move (pivot) completed successfully — ClusterctlMove PASSED")
		})
	})

	Context("Webhook Validation", Label("webhook"), func() {
		It("Should reject an EvrocCluster with missing required fields", func() {
			By("Applying an EvrocCluster without project or region")
			invalidCluster := []byte(`apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocCluster
metadata:
  name: invalid-cluster-test
  namespace: default
spec: {}
`)
			cmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfigPath,
				"apply", "-f", "-",
			)
			cmd.Stdin = bytes.NewReader(invalidCluster)
			out, err := cmd.CombinedOutput()
			Expect(err).To(HaveOccurred(),
				"Webhook should reject EvrocCluster without required fields, but kubectl apply succeeded: %s", string(out))
			Expect(string(out)).To(ContainSubstring("project"),
				"Rejection message should mention missing project field, got: %s", string(out))

			GinkgoWriter.Printf("Webhook correctly rejected invalid EvrocCluster: %s\n",
				strings.TrimSpace(string(out)))
		})

		It("Should reject an EvrocCluster with invalid region format", func() {
			By("Applying an EvrocCluster with a bad region")
			invalidCluster := []byte(`apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocCluster
metadata:
  name: invalid-region-test
  namespace: default
spec:
  project: test-project
  region: invalid
  failureDomains: [a]
`)
			cmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfigPath,
				"apply", "-f", "-",
			)
			cmd.Stdin = bytes.NewReader(invalidCluster)
			out, err := cmd.CombinedOutput()
			Expect(err).To(HaveOccurred(),
				"Webhook should reject EvrocCluster with invalid region, but kubectl apply succeeded: %s", string(out))
			Expect(string(out)).To(ContainSubstring("region"),
				"Rejection message should mention region, got: %s", string(out))

			GinkgoWriter.Printf("Webhook correctly rejected invalid region: %s\n",
				strings.TrimSpace(string(out)))
		})

		It("Should default failureDomains when omitted", func() {
			By("Applying an EvrocCluster without failureDomains and verifying defaults are applied")
			cluster := []byte(`apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocCluster
metadata:
  name: fd-default-test
  namespace: default
spec:
  project: test-project
  region: se-sto
`)
			cmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfigPath,
				"apply", "-f", "-",
			)
			cmd.Stdin = bytes.NewReader(cluster)
			out, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(),
				"EvrocCluster with omitted failureDomains should be accepted (defaulted): %s", string(out))

			// Verify the default was applied
			getCmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfigPath,
				"get", "evroccluster", "fd-default-test", "-n", "default", "-o", "jsonpath={.spec.failureDomains}",
			)
			getOut, getErr := getCmd.CombinedOutput()
			Expect(getErr).NotTo(HaveOccurred(), "Failed to get EvrocCluster: %s", string(getOut))
			Expect(string(getOut)).To(ContainSubstring("a"),
				"failureDomains should contain default zone 'a', got: %s", string(getOut))
		})

		It("Should reject an EvrocCluster with duplicate SG names across sections", func() {
			By("Applying an EvrocCluster with the same SG name in common and controlPlane")
			invalidCluster := []byte(`apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocCluster
metadata:
  name: dup-sg-test
  namespace: default
spec:
  project: test-project
  region: se-sto
  failureDomains: [a]
  securityGroups:
    common:
      inlineSecurityGroups:
      - name: my-sg
        rules:
        - name: ssh
          direction: Ingress
          protocol: TCP
          port: 22
          remoteCIDR: "0.0.0.0/0"
    controlPlane:
      inlineSecurityGroups:
      - name: my-sg
        rules:
        - name: api
          direction: Ingress
          protocol: TCP
          port: 6443
          remoteCIDR: "0.0.0.0/0"
`)
			cmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfigPath,
				"apply", "-f", "-",
			)
			cmd.Stdin = bytes.NewReader(invalidCluster)
			out, err := cmd.CombinedOutput()
			Expect(err).To(HaveOccurred(),
				"Webhook should reject duplicate SG names across sections: %s", string(out))
			Expect(string(out)).To(ContainSubstring("unique"),
				"Rejection message should mention uniqueness, got: %s", string(out))
		})

		It("Should reject an EvrocMachine with disk too small", func() {
			By("Applying an EvrocMachine with rootDiskSize below minimum")
			invalidMachine := []byte(`apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocMachine
metadata:
  name: small-disk-test
  namespace: default
spec:
  project: test-project
  region: se-sto
  computeProfile: a1a.m
  image: ubuntu.22-04.1
  rootDiskSize: 5
`)
			cmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfigPath,
				"apply", "-f", "-",
			)
			cmd.Stdin = bytes.NewReader(invalidMachine)
			out, err := cmd.CombinedOutput()
			Expect(err).To(HaveOccurred(),
				"Webhook should reject EvrocMachine with disk too small: %s", string(out))
			Expect(string(out)).To(ContainSubstring("rootDiskSize"),
				"Rejection message should mention rootDiskSize, got: %s", string(out))
		})

		It("Should reject an EvrocMachine with invalid compute profile", func() {
			By("Applying an EvrocMachine with a bad compute profile format")
			invalidMachine := []byte(`apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocMachine
metadata:
  name: invalid-machine-test
  namespace: default
spec:
  project: test-project
  region: se-sto
  computeProfile: invalid-flavor
  image: ubuntu.22-04.1
  rootDiskSize: 100
`)
			cmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfigPath,
				"apply", "-f", "-",
			)
			cmd.Stdin = bytes.NewReader(invalidMachine)
			out, err := cmd.CombinedOutput()
			Expect(err).To(HaveOccurred(),
				"Webhook should reject EvrocMachine with invalid compute profile, but kubectl apply succeeded: %s", string(out))
			Expect(string(out)).To(ContainSubstring("computeProfile"),
				"Rejection message should mention computeProfile, got: %s", string(out))

			GinkgoWriter.Printf("Webhook correctly rejected invalid compute profile: %s\n",
				strings.TrimSpace(string(out)))
		})
	})

	Context("Provider Installation", func() {
		It("Should have the evroc provider running and CRDs installed", func() {
			By("Verifying evroc provider deployment is available")
			waitForDeploymentReady(ctx, kubeconfigPath, "capi-evroc-system", "infrastructure-evroc", 2*time.Minute)

			By("Verifying required CRDs are registered")
			for _, crd := range []string{
				"evrocclusters.infrastructure.cluster.x-k8s.io",
				"evrocmachines.infrastructure.cluster.x-k8s.io",
				"evrocclustertemplates.infrastructure.cluster.x-k8s.io",
				"evrocmachinetemplates.infrastructure.cluster.x-k8s.io",
			} {
				verifyCRDExists(ctx, kubeconfigPath, crd)
			}
		})
	})
})

// ─── Setup helpers ────────────────────────────────────────────────────────────

func createKindCluster(ctx context.Context, name, kubeconfigOut string) {
	// If the cluster already exists (e.g. pre-provisioned), just export the kubeconfig.
	checkCmd := exec.CommandContext(ctx, "kind", "get", "clusters")
	out, _ := checkCmd.Output()
	if strings.Contains(string(out), name) {
		By("Kind cluster already exists, exporting kubeconfig")
		exportCmd := exec.CommandContext(ctx, "kind", "export", "kubeconfig",
			"--name", name, "--kubeconfig", kubeconfigOut)
		Expect(exportCmd.Run()).To(Succeed(), "Failed to export kind kubeconfig")
		return
	}

	cmd := exec.CommandContext(ctx, "kind", "create", "cluster",
		"--name", name,
		"--kubeconfig", kubeconfigOut,
		"--wait", "5m",
	)
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	Expect(cmd.Run()).To(Succeed(), "Failed to create kind cluster %s", name)
}

func deleteKindCluster(name string) {
	cmd := exec.Command("kind", "delete", "cluster", "--name", name)
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	_ = cmd.Run() // best effort
}

// loadProviderImageIntoKind pre-loads the provider image into the kind cluster
// so the pod can start without pulling from the registry. Uses E2E_LOCAL_IMAGE
// if set, otherwise falls back to the default image from the components manifest.
func loadProviderImageIntoKind(ctx context.Context, clusterName string) {
	const defaultImage = "ghcr.io/evroc-oss/cluster-api-provider-evroc:latest"
	image := os.Getenv("E2E_LOCAL_IMAGE")
	if image == "" {
		image = defaultImage
	}
	cmd := exec.CommandContext(ctx, "kind", "load", "docker-image", image, "--name", clusterName)
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	// Best effort — if the image isn't in local Docker, kind will fail but the
	// pod may still pull from the registry if connectivity is available.
	if err := cmd.Run(); err != nil {
		GinkgoWriter.Printf("Warning: failed to pre-load image %s into kind: %v\n", image, err)
	}
}

// loadProviderImageToRemoteCluster pre-loads the provider image into a remote
// cluster's containerd by saving the Docker image as a tar and importing it
// via SSH. This is necessary when the remote cluster has no access to a
// container registry (e.g. ghcr.io).
func loadProviderImageToRemoteCluster(ctx context.Context, kubeconfig string) {
	const defaultImage = "ghcr.io/evroc-oss/cluster-api-provider-evroc:latest"
	image := os.Getenv("E2E_LOCAL_IMAGE")
	if image == "" {
		image = defaultImage
	}

	// Save the Docker image to a tar file.
	tarPath := filepath.Join(os.TempDir(), "provider-image.tar")
	saveCmd := exec.CommandContext(ctx, "docker", "save", "-o", tarPath, image)
	saveOut, err := saveCmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "docker save failed: %s", string(saveOut))
	defer os.Remove(tarPath)

	// Extract the control plane node IP from the kubeconfig's server URL.
	kubeconfigBytes, err := os.ReadFile(kubeconfig)
	Expect(err).ToNot(HaveOccurred(), "reading workload kubeconfig")

	var kc struct {
		Clusters []struct {
			Cluster struct {
				Server string `yaml:"server"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
	}
	Expect(yaml.Unmarshal(kubeconfigBytes, &kc)).To(Succeed())
	Expect(kc.Clusters).NotTo(BeEmpty(), "no clusters in workload kubeconfig")

	serverURL, err := url.Parse(kc.Clusters[0].Cluster.Server)
	Expect(err).ToNot(HaveOccurred(), "parsing server URL")
	host := serverURL.Hostname()

	GinkgoWriter.Printf("Importing provider image to workload node %s via SSH\n", host)

	// Determine SSH key path: prefer EVROC_SSH_PRIVATE_KEY env, fall back to default.
	sshKeyPath := os.Getenv("EVROC_SSH_PRIVATE_KEY")
	if sshKeyPath == "" {
		homeDir, _ := os.UserHomeDir()
		sshKeyPath = filepath.Join(homeDir, ".ssh", "id_ed25519")
	}

	// Pipe the tar file into ctr on the remote node via SSH.
	sshCmd := exec.CommandContext(ctx, "ssh",
		"-i", sshKeyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=30",
		fmt.Sprintf("evroc-user@%s", host),
		"sudo", "ctr", "-n", "k8s.io", "images", "import", "-",
	)
	tarFile, err := os.Open(tarPath)
	Expect(err).ToNot(HaveOccurred())
	defer tarFile.Close()
	sshCmd.Stdin = tarFile
	sshOut, err := sshCmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "SSH ctr import failed: %s", string(sshOut))
	GinkgoWriter.Printf("Image imported successfully: %s\n", string(sshOut))
}

// applyEvrocCredentials creates the capi-evroc-system namespace and the
// evroc-credentials secret from environment variables.
func applyEvrocCredentials(ctx context.Context, kubeconfig string) {
	secret := buildCredentialsYAML()
	applyRawYAML(ctx, kubeconfig, secret)
}

func buildCredentialsYAML() []byte {
	token := os.Getenv("EVROC_TOKEN")
	refreshToken := os.Getenv("EVROC_REFRESH_TOKEN")
	username := os.Getenv("EVROC_USERNAME")
	password := os.Getenv("EVROC_PASSWORD")
	project := os.Getenv("EVROC_PROJECT")
	region := os.Getenv("EVROC_REGION")
	organization := os.Getenv("EVROC_ORGANIZATION")
	if region == "" {
		region = "se-sto"
	}

	authSection := ""
	if token != "" {
		authSection += fmt.Sprintf("    token: %q\n", token)
	}
	if refreshToken != "" {
		authSection += fmt.Sprintf("    refresh_token: %q\n", refreshToken)
	}
	if username != "" {
		authSection += fmt.Sprintf("    username: %q\n", username)
	}
	if password != "" {
		authSection += fmt.Sprintf("    password: %q\n", password)
	}

	configYAML := fmt.Sprintf("auth:\n%scontext:\n  project: %q\n  region: %q\n  organization: %q\n",
		authSection, project, region, organization)

	return []byte(fmt.Sprintf(`apiVersion: v1
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
  config.yaml: |
%s---
apiVersion: v1
kind: Secret
metadata:
  name: evroc-credentials
  namespace: default
type: Opaque
stringData:
  config.yaml: |
%s`, indent(configYAML, "    "), indent(configYAML, "    ")))
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

// applyEvrocCRDs applies the evroc CRDs directly to avoid race conditions.
func applyEvrocCRDs(ctx context.Context, kubeconfig string) {
	crdDir := filepath.Join(repoRoot, "config", "crd", "bases")
	kubectlBin := kubectlPath()
	cmd := exec.CommandContext(ctx, kubectlBin,
		"--kubeconfig", kubeconfig,
		"apply", "-f", crdDir,
	)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "Failed to apply evroc CRDs: %s", string(out))
}

// clusterctlInit runs `clusterctl init --infrastructure evroc` using a
// dynamically-generated config file that points to local provider manifests.
// The components file is modified to strip CRDs (applied separately) and
// optionally substitute the provider image for a locally-built one.
func clusterctlInit(ctx context.Context, kubeconfig string, useLocalImage bool) {
	// Create a clean directory structure for clusterctl.
	// clusterctl parses the URL from the end:
	//   [-1] = components filename, [-2] = version (semver), [-3] = provider label
	// Use the real version from VERSION so it matches metadata.yaml releaseSeries.
	versionBytes, err := os.ReadFile(filepath.Join(repoRoot, "VERSION"))
	Expect(err).ToNot(HaveOccurred(), "reading VERSION file")
	providerVersion := strings.TrimSpace(string(versionBytes))
	homeDir, err := os.UserHomeDir()
	Expect(err).ToNot(HaveOccurred())
	clusterctlBasePath := filepath.Join(homeDir, "capi-providers")
	localProviderDir := filepath.Join(clusterctlBasePath, "infrastructure-evroc", providerVersion)
	Expect(os.MkdirAll(localProviderDir, 0750)).To(Succeed())

	// Build the modified components (no CRDs + optional image substitution)
	localComponentsFile := filepath.Join(localProviderDir, "infrastructure-components.yaml")
	Expect(os.WriteFile(localComponentsFile, buildNoCRDComponentsFile(useLocalImage), 0600)).To(Succeed())

	// Copy metadata.yaml — clusterctl needs it to determine the target namespace
	metadataSource := filepath.Join(repoRoot, "metadata.yaml")
	metadataContent, err := os.ReadFile(metadataSource)
	Expect(err).NotTo(HaveOccurred(), "Failed to read metadata.yaml")
	metadataFile := filepath.Join(localProviderDir, "metadata.yaml")
	Expect(os.WriteFile(metadataFile, metadataContent, 0600)).To(Succeed())

	configContent := fmt.Sprintf(`providers:
  - name: evroc
    url: "file://%s/infrastructure-evroc/%s/infrastructure-components.yaml"
    type: InfrastructureProvider
`, clusterctlBasePath, providerVersion)
	configFile := filepath.Join(homeDir, "capi-clusterctl.yaml")
	Expect(os.WriteFile(configFile, []byte(configContent), 0600)).To(Succeed())

	// Pin the CAPI stack to the same major version our provider was built against
	// (v1.12.x with v1beta2 contract).
	capiVersion := "v1.12.0"
	cmd := exec.CommandContext(ctx, clusterctlPath(),
		"init",
		"--core", "cluster-api:"+capiVersion,
		"--bootstrap", "kubeadm:"+capiVersion,
		"--control-plane", "kubeadm:"+capiVersion,
		"--infrastructure", "evroc:"+providerVersion,
		"--target-namespace", "capi-evroc-system",
		"--kubeconfig", kubeconfig,
		"--config", configFile,
	)
	cmd.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfig,
	)
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	Expect(cmd.Run()).To(Succeed(), "clusterctl init failed")
}

// buildNoCRDComponentsFile reads infrastructure-components.yaml and returns
// a version with CRD documents stripped (they're applied separately).
// If E2E_LOCAL_IMAGE is set, the provider image reference is substituted.
func buildNoCRDComponentsFile(useLocalImage bool) []byte {
	const imageRepo = "ghcr.io/evroc-oss/cluster-api-provider-evroc"

	componentsPath := filepath.Join(repoRoot, "templates", "infrastructure-components.yaml")
	componentsBytes, err := os.ReadFile(componentsPath)
	Expect(err).ToNot(HaveOccurred(), "reading infrastructure-components.yaml")

	// Read VERSION to build the exact image reference baked into infrastructure-components.yaml.
	versionBytes, err := os.ReadFile(filepath.Join(repoRoot, "VERSION"))
	Expect(err).ToNot(HaveOccurred(), "reading VERSION file")
	releaseImage := imageRepo + ":" + strings.TrimSpace(string(versionBytes))

	content := string(componentsBytes)
	if useLocalImage {
		if localImage := os.Getenv("E2E_LOCAL_IMAGE"); localImage != "" {
			content = strings.ReplaceAll(content, releaseImage, localImage)
		}
	}

	var kept []string
	for _, doc := range strings.Split(content, "\n---") {
		trimmed := strings.TrimSpace(doc)
		if trimmed == "" {
			continue
		}
		// Skip documents that are only comments (no YAML resources)
		hasResource := false
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				hasResource = true
				break
			}
		}
		if !hasResource {
			continue
		}
		// Skip CRDs (applied separately)
		if strings.Contains(doc, "kind: CustomResourceDefinition") {
			continue
		}
		kept = append(kept, doc)
	}
	return []byte(strings.Join(kept, "\n---"))
}

// ─── Cluster lifecycle helpers ────────────────────────────────────────────────

// collectClusterArtifacts captures controller logs, machine status, and events
// before cluster deletion. This ensures debugging information is available in CI/CD.
// Each test gets its own folder directly under _artifacts for easy CI/CD integration.
func collectClusterArtifacts(ctx context.Context, kubeconfig, clusterName, namespace string) {
	// Create test-specific folder directly under artifacts (no intermediate "logs" folder)
	// This makes each test self-contained and easier to find in CI/CD
	logDir := filepath.Join(artifactFolder, clusterName)
	if err := os.MkdirAll(logDir, 0750); err != nil {
		GinkgoWriter.Printf("Warning: failed to create log directory %s: %v\n", logDir, err)
		return
	}

	// Collect controller logs from capi-evroc-system namespace
	By(fmt.Sprintf("Collecting controller logs for cluster %s", clusterName))
	collectControllerLogs(ctx, kubeconfig, logDir)

	// Collect EvrocMachine status for debugging
	By(fmt.Sprintf("Collecting machine status for cluster %s", clusterName))
	collectMachineStatus(ctx, kubeconfig, clusterName, namespace, logDir)

	// Collect cluster events
	By(fmt.Sprintf("Collecting events for cluster %s", clusterName))
	collectClusterEvents(ctx, kubeconfig, clusterName, namespace, logDir)
}

// collectControllerLogs captures logs from all pods in capi-evroc-system namespace
func collectControllerLogs(ctx context.Context, kubeconfig, logDir string) {
	// Get pod names from capi-evroc-system namespace
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "pods",
		"-n", "capi-evroc-system",
		"-o", "jsonpath={.items[*].metadata.name}",
	)
	out, err := cmd.Output()
	if err != nil {
		GinkgoWriter.Printf("Warning: failed to get controller pods: %v\n", err)
		return
	}

	podNames := strings.Fields(string(out))
	for _, podName := range podNames {
		logFile := filepath.Join(logDir, fmt.Sprintf("controller-%s.log", podName))
		logCmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"logs", podName,
			"-n", "capi-evroc-system",
			"--all-containers=true",
			"--tail=-1", // Get all logs
		)
		logOut, err := logCmd.Output()
		if err != nil {
			GinkgoWriter.Printf("Warning: failed to get logs for pod %s: %v\n", podName, err)
			continue
		}
		if err := os.WriteFile(logFile, logOut, 0600); err != nil {
			GinkgoWriter.Printf("Warning: failed to write log file %s: %v\n", logFile, err)
		}
	}
}

// collectMachineStatus captures EvrocMachine status (conditions, managed resources, etc.)
func collectMachineStatus(ctx context.Context, kubeconfig, clusterName, namespace, logDir string) {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evrocmachines",
		"-n", namespace,
		"-l", "cluster.x-k8s.io/cluster-name="+clusterName,
		"-o", "yaml",
	)
	out, err := cmd.Output()
	if err != nil {
		GinkgoWriter.Printf("Warning: failed to get EvrocMachine status: %v\n", err)
		return
	}

	statusFile := filepath.Join(logDir, "evrocmachine-status.yaml")
	if err := os.WriteFile(statusFile, out, 0600); err != nil {
		GinkgoWriter.Printf("Warning: failed to write machine status file %s: %v\n", statusFile, err)
	}

	// Also collect regular Machine status
	cmd = exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "machines",
		"-n", namespace,
		"-l", "cluster.x-k8s.io/cluster-name="+clusterName,
		"-o", "yaml",
	)
	out, err = cmd.Output()
	if err != nil {
		GinkgoWriter.Printf("Warning: failed to get Machine status: %v\n", err)
		return
	}

	machineFile := filepath.Join(logDir, "machine-status.yaml")
	if err := os.WriteFile(machineFile, out, 0600); err != nil {
		GinkgoWriter.Printf("Warning: failed to write machine status file %s: %v\n", machineFile, err)
	}
}

// collectClusterEvents captures events related to the cluster
func collectClusterEvents(ctx context.Context, kubeconfig, clusterName, namespace, logDir string) {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "events",
		"-n", namespace,
		"--field-selector", "involvedObject.name="+clusterName,
		"-o", "yaml",
	)
	out, err := cmd.Output()
	if err != nil {
		GinkgoWriter.Printf("Warning: failed to get cluster events: %v\n", err)
		// Don't return - try to get machine events too
	} else {
		eventFile := filepath.Join(logDir, "cluster-events.yaml")
		if err := os.WriteFile(eventFile, out, 0600); err != nil {
			GinkgoWriter.Printf("Warning: failed to write event file %s: %v\n", eventFile, err)
		}
	}

	// Also get events for all machines in the cluster
	cmd = exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "events",
		"-n", namespace,
		"-o", "yaml",
	)
	out, err = cmd.Output()
	if err != nil {
		GinkgoWriter.Printf("Warning: failed to get namespace events: %v\n", err)
		return
	}

	allEventsFile := filepath.Join(logDir, "all-events.yaml")
	if err := os.WriteFile(allEventsFile, out, 0600); err != nil {
		GinkgoWriter.Printf("Warning: failed to write all events file %s: %v\n", allEventsFile, err)
	}
}

// generateKubeadmClusterYAML runs clusterctl generate cluster using the default
// kubeadm-based template and returns the manifest bytes.
func generateKubeadmClusterYAML(clusterName string) []byte {
	templatePath := filepath.Join(repoRoot, "templates", "cluster-template-minimal.yaml")

	cmd := exec.CommandContext(ctx, clusterctlPath(),
		"generate", "cluster", clusterName,
		"--from", templatePath,
		"--target-namespace", "default",
	)
	// Build override map first so we can filter conflicting vars from os.Environ().
	overrides := map[string]string{
		"CLUSTER_NAME":                  clusterName,
		"EVROC_PROJECT":                 e2eConfig.GetVariable("EVROC_PROJECT"),
		"EVROC_REGION":                  e2eConfig.GetVariable("EVROC_REGION"),
		"EVROC_AVAILABILITY_ZONE":       e2eConfig.GetVariable("EVROC_AVAILABILITY_ZONE"),
		"KUBERNETES_VERSION":            e2eConfig.MustGetVariable("KUBERNETES_VERSION"),
		"CONTROL_PLANE_MACHINE_COUNT":   e2eConfig.MustGetVariable("CONTROL_PLANE_MACHINE_COUNT"),
		"WORKER_MACHINE_COUNT":          e2eConfig.MustGetVariable("WORKER_MACHINE_COUNT"),
		"EVROC_CONTROL_PLANE_FLAVOR":    e2eConfig.MustGetVariable("EVROC_CONTROL_PLANE_FLAVOR"),
		"EVROC_IMAGE":                   e2eConfig.MustGetVariable("EVROC_IMAGE"),
		"EVROC_CONTROL_PLANE_DISK_SIZE": e2eConfig.MustGetVariable("EVROC_CONTROL_PLANE_DISK_SIZE"),
		"EVROC_SSH_KEY":                 e2eConfig.GetVariable("EVROC_SSH_KEY"),
		"EVROC_CREDENTIALS_NAMESPACE":   e2eConfig.GetVariable("EVROC_CREDENTIALS_NAMESPACE"),
		"EVROC_CREDENTIALS_SECRET":      e2eConfig.GetVariable("EVROC_CREDENTIALS_SECRET"),
		"XDG_CONFIG_HOME":               filepath.Join(artifactFolder, "xdg"),
	}
	// Filter os.Environ() to remove keys that we override, preventing
	// stale env vars (e.g. KUBERNETES_VERSION from RKE2) from winning.
	for _, env := range os.Environ() {
		key := strings.SplitN(env, "=", 2)[0]
		if _, overridden := overrides[key]; !overridden {
			cmd.Env = append(cmd.Env, env)
		}
	}
	for k, v := range overrides {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	Expect(cmd.Run()).To(Succeed(), "clusterctl generate cluster failed: %s", stderr.String())

	outputFile := filepath.Join(artifactFolder, clusterName+".yaml")
	Expect(os.WriteFile(outputFile, stdout.Bytes(), os.ModePerm)).To(Succeed())
	return stdout.Bytes()
}

func applyManifest(ctx context.Context, kubeconfig string, manifest []byte, clusterName string) {
	applyRawYAML(ctx, kubeconfig, manifest)
}

func applyRawYAML(ctx context.Context, kubeconfig string, manifest []byte) {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"apply", "-f", "-",
	)
	cmd.Stdin = bytes.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "kubectl apply failed: %s", string(out))
}

func deleteCluster(ctx context.Context, kubeconfig, clusterName, namespace string) {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"delete", "cluster", clusterName,
		"-n", namespace,
		"--timeout", "15m",
		"--ignore-not-found",
	)
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	_ = cmd.Run() // best effort
}

// ─── Wait helpers ─────────────────────────────────────────────────────────────

// waitForDeploymentReady polls until the named deployment has at least 1 ready replica.
func waitForDeploymentReady(ctx context.Context, kubeconfig, namespace, labelSelector string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	logged := false
	for time.Now().Before(deadline) {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"get", "deployment",
			"-n", namespace,
			"-l", "cluster.x-k8s.io/provider="+labelSelector,
			"-o", "jsonpath={.items[*].status.readyReplicas}",
		)
		out, err := cmd.Output()
		if err == nil && strings.TrimSpace(string(out)) != "" && strings.TrimSpace(string(out)) != "0" {
			return
		}
		// Log pod status periodically for debugging.
		if !logged || time.Until(deadline) < 5*time.Minute {
			podCmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfig,
				"get", "pods", "-n", namespace,
				"-l", "cluster.x-k8s.io/provider="+labelSelector,
				"-o", "wide",
			)
			podOut, _ := podCmd.CombinedOutput()
			GinkgoWriter.Printf("  [debug] deployment %s pods: %s\n", labelSelector, strings.TrimSpace(string(podOut)))
			logged = true
		}
		time.Sleep(10 * time.Second)
	}
	// Capture final pod descriptions for debugging before failing.
	descCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"describe", "pods", "-n", namespace,
		"-l", "cluster.x-k8s.io/provider="+labelSelector,
	)
	descOut, _ := descCmd.CombinedOutput()
	GinkgoWriter.Printf("  [debug] final pod describe:\n%s\n", string(descOut))
	Fail(fmt.Sprintf("Deployment with provider label %q in namespace %q not ready after %s", labelSelector, namespace, timeout))
}

// waitForEvrocClusterReady polls until the cluster reports Provisioned phase.
func waitForEvrocClusterReady(ctx context.Context, kubeconfig, clusterName, namespace string, intervals ...interface{}) {
	timeout, poll := intervalsToTimeDuration(intervals, 35*time.Minute, 30*time.Second)

	Eventually(func() error {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"get", "cluster", clusterName,
			"-n", namespace,
			"-o", "jsonpath={.status.phase}",
		)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("kubectl get cluster failed: %w", err)
		}
		phase := strings.TrimSpace(string(out))
		if phase != string(clusterv1.ClusterPhaseProvisioned) {
			return fmt.Errorf("cluster %s phase is %q, want %q", clusterName, phase, clusterv1.ClusterPhaseProvisioned)
		}
		return nil
	}, timeout, poll).Should(Succeed(), "Cluster %s/%s did not reach Provisioned phase", namespace, clusterName)
}

// waitForMachineProvisioned waits until at least one machine for the cluster is Running.
func waitForMachineProvisioned(ctx context.Context, kubeconfig, clusterName, namespace string, intervals ...interface{}) {
	timeout, poll := intervalsToTimeDuration(intervals, 20*time.Minute, 30*time.Second)

	Eventually(func() error {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"get", "machines",
			"-n", namespace,
			"-l", "cluster.x-k8s.io/cluster-name="+clusterName,
			"-o", "jsonpath={.items[*].status.phase}",
		)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("kubectl get machines failed: %w", err)
		}
		phases := strings.Fields(string(out))
		for _, p := range phases {
			if p == string(clusterv1.MachinePhaseRunning) {
				return nil
			}
		}
		return fmt.Errorf("no machines in Running phase for cluster %s (phases: %v)", clusterName, phases)
	}, timeout, poll).Should(Succeed(), "No machines reached Running phase for cluster %s", clusterName)
}

func verifyCRDExists(ctx context.Context, kubeconfig, crdName string) {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "crd", crdName,
	)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "CRD %s must exist: %s", crdName, string(out))
}

// ─── Deletion verification helpers ────────────────────────────────────────────

// verifyClusterDeleted waits for the EvrocCluster and EvrocMachines to be fully
// removed after cluster deletion, confirming finalizers completed cleanup.
func verifyClusterDeleted(ctx context.Context, kubeconfig, clusterName, namespace string) {
	timeout, poll := 10*time.Minute, 15*time.Second

	// Wait for EvrocCluster to be gone
	Eventually(func() bool {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"get", "evroccluster", clusterName,
			"-n", namespace,
		)
		return cmd.Run() != nil // true when gone (kubectl returns error)
	}, timeout, poll).Should(BeTrue(),
		"EvrocCluster %s should be deleted after cluster deletion", clusterName)

	// Wait for all EvrocMachines to be gone
	Eventually(func() bool {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"get", "evrocmachines",
			"-n", namespace,
			"-l", "cluster.x-k8s.io/cluster-name="+clusterName,
			"-o", "jsonpath={.items}",
		)
		out, err := cmd.Output()
		if err != nil {
			return true // CRD gone or other error = resources are gone
		}
		return strings.TrimSpace(string(out)) == "[]"
	}, timeout, poll).Should(BeTrue(),
		"EvrocMachines for cluster %s should be deleted after cluster deletion", clusterName)

	GinkgoWriter.Printf("Cluster %s resources fully cleaned up\n", clusterName)
}

// ─── Post-provision verification helpers ──────────────────────────────────────

// verifyClusterSGStatus checks that the EvrocCluster has security groups in status
// with the expected role-based structure (common, controlPlane).
func verifyClusterSGStatus(ctx context.Context, kubeconfig, clusterName, namespace string) {
	// Get SG roles from status
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evroccluster", clusterName,
		"-n", namespace,
		"-o", "jsonpath={.status.resources.securityGroups[*].role}",
	)
	out, err := cmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get EvrocCluster SG status")

	roles := strings.Fields(strings.TrimSpace(string(out)))
	Expect(roles).ToNot(BeEmpty(), "EvrocCluster should have security groups in status")

	// The minimal template defines common + controlPlane sections
	Expect(roles).To(ContainElement("common"),
		"SG status should contain a security group with role=common, got: %v", roles)
	Expect(roles).To(ContainElement("controlPlane"),
		"SG status should contain a security group with role=controlPlane, got: %v", roles)

	// Verify SG names are non-empty
	nameCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evroccluster", clusterName,
		"-n", namespace,
		"-o", "jsonpath={.status.resources.securityGroups[*].name}",
	)
	nameOut, err := nameCmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get SG names from status")
	names := strings.Fields(strings.TrimSpace(string(nameOut)))
	Expect(names).To(HaveLen(len(roles)), "Each SG should have a name")
	for _, name := range names {
		Expect(name).ToNot(BeEmpty(), "SG name should not be empty")
		Expect(name).To(ContainSubstring(clusterName),
			"SG cloud name should contain the cluster name, got: %s", name)
	}

	GinkgoWriter.Printf("SG status verified: roles=%v names=%v\n", roles, names)
}

// verifyMachineStatus checks that EvrocMachines have populated addresses and
// a providerID in the expected evroc:/// format.
func verifyMachineStatus(ctx context.Context, kubeconfig, clusterName, namespace string) {
	// Check providerIDs
	pidCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evrocmachines",
		"-n", namespace,
		"-l", "cluster.x-k8s.io/cluster-name="+clusterName,
		"-o", "jsonpath={.items[*].spec.providerID}",
	)
	pidOut, err := pidCmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get EvrocMachine providerIDs")
	providerIDs := strings.Fields(strings.TrimSpace(string(pidOut)))
	Expect(providerIDs).ToNot(BeEmpty(), "At least one EvrocMachine should have a providerID")

	for _, pid := range providerIDs {
		Expect(pid).To(HavePrefix("evroc://"),
			"providerID should have format evroc://<vm-id>, got: %s", pid)
	}

	// Check addresses are populated
	addrCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evrocmachines",
		"-n", namespace,
		"-l", "cluster.x-k8s.io/cluster-name="+clusterName,
		"-o", "jsonpath={.items[*].status.addresses[0].address}",
	)
	addrOut, err := addrCmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get EvrocMachine addresses")
	addresses := strings.Fields(strings.TrimSpace(string(addrOut)))
	Expect(addresses).ToNot(BeEmpty(), "At least one EvrocMachine should have an address")

	for _, addr := range addresses {
		Expect(strings.Contains(addr, ".")).To(BeTrue(),
			"Machine address should look like an IP, got: %s", addr)
	}

	GinkgoWriter.Printf("Machine status verified: providerIDs=%v addresses=%v\n", providerIDs, addresses)
}

// verifyControlPlaneEndpoint checks that the EvrocCluster has a valid control
// plane endpoint with a non-empty host (public IP) and port 6443.
func verifyControlPlaneEndpoint(ctx context.Context, kubeconfig, clusterName, namespace string) {
	// Check host
	hostCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evroccluster", clusterName,
		"-n", namespace,
		"-o", "jsonpath={.spec.controlPlaneEndpoint.host}",
	)
	hostOut, err := hostCmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get controlPlaneEndpoint.host")
	host := strings.TrimSpace(string(hostOut))
	Expect(host).ToNot(BeEmpty(), "controlPlaneEndpoint.host should be set after provisioning")

	// Basic IP format check (contains dots, no alpha except for IPv6)
	Expect(strings.Contains(host, ".")).To(BeTrue(),
		"controlPlaneEndpoint.host should look like an IP address, got: %s", host)

	// Check port
	portCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evroccluster", clusterName,
		"-n", namespace,
		"-o", "jsonpath={.spec.controlPlaneEndpoint.port}",
	)
	portOut, err := portCmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get controlPlaneEndpoint.port")
	port := strings.TrimSpace(string(portOut))
	Expect(port).To(Equal("6443"), "controlPlaneEndpoint.port should be 6443, got: %s", port)

	// Verify public IP is tracked in status
	pipCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evroccluster", clusterName,
		"-n", namespace,
		"-o", "jsonpath={.status.resources.publicIP.name}",
	)
	pipOut, err := pipCmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get publicIP status")
	pipName := strings.TrimSpace(string(pipOut))
	Expect(pipName).ToNot(BeEmpty(), "publicIP.name should be tracked in status")

	GinkgoWriter.Printf("CP endpoint verified: host=%s port=%s publicIP=%s\n", host, port, pipName)
}

// ─── clusterctl move helpers ──────────────────────────────────────────────────

// waitForEvrocMachineReady waits until at least one EvrocMachine for the cluster
// has a non-empty providerID, indicating the VM is Ready in evroc. This does NOT
// require the CAPI Machine to reach Running phase (which needs the management
// cluster to reach the workload cluster API to verify the Node).
func waitForEvrocMachineReady(ctx context.Context, kubeconfig, clusterName, namespace string, intervals ...interface{}) {
	timeout, poll := intervalsToTimeDuration(intervals, 20*time.Minute, 30*time.Second)

	Eventually(func() error {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"get", "evrocmachines",
			"-n", namespace,
			"-l", "cluster.x-k8s.io/cluster-name="+clusterName,
			"-o", "jsonpath={.items[*].spec.providerID}",
		)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("kubectl get evrocmachines failed: %w", err)
		}
		ids := strings.Fields(strings.TrimSpace(string(out)))
		if len(ids) == 0 {
			return fmt.Errorf("no EvrocMachines with providerID for cluster %s yet", clusterName)
		}
		GinkgoWriter.Printf("EvrocMachine providerIDs ready: %v\n", ids)
		return nil
	}, timeout, poll).Should(Succeed(), "No EvrocMachine reached ready (providerID set) for cluster %s", clusterName)
}

// waitForWorkloadKubeconfig polls until the <cluster>-kubeconfig secret exists
// and writes the decoded kubeconfig to outputPath. The secret is created by the
// KubeadmControlPlane controller once bootstrap data is generated.
func waitForWorkloadKubeconfig(ctx context.Context, mgmtKubeconfig, clusterName, namespace, outputPath string, intervals ...interface{}) {
	timeout, poll := intervalsToTimeDuration(intervals, 20*time.Minute, 30*time.Second)

	Eventually(func() error {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", mgmtKubeconfig,
			"get", "secret", clusterName+"-kubeconfig",
			"-n", namespace,
			"-o", "jsonpath={.data.value}",
		)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("kubeconfig secret not available yet: %w", err)
		}
		if len(out) == 0 {
			return fmt.Errorf("kubeconfig secret exists but value is empty")
		}

		decodeCmd := exec.CommandContext(ctx, "base64", "-d")
		decodeCmd.Stdin = bytes.NewReader(out)
		decoded, err := decodeCmd.Output()
		if err != nil {
			return fmt.Errorf("failed to base64 decode kubeconfig: %w", err)
		}

		if err := os.WriteFile(outputPath, decoded, 0600); err != nil {
			return fmt.Errorf("failed to write kubeconfig to %s: %w", outputPath, err)
		}
		GinkgoWriter.Printf("Workload kubeconfig written to %s\n", outputPath)
		return nil
	}, timeout, poll).Should(Succeed(), "Workload cluster kubeconfig secret not available for cluster %s", clusterName)
}

// getWorkloadKubeconfig retrieves the kubeconfig secret for the workload cluster
// and writes it to the given file path. The secret is named <cluster>-kubeconfig
// by convention and stored in the same namespace as the Cluster resource.
func getWorkloadKubeconfig(ctx context.Context, mgmtKubeconfig, clusterName, namespace, outputPath string) {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", mgmtKubeconfig,
		"get", "secret", clusterName+"-kubeconfig",
		"-n", namespace,
		"-o", "jsonpath={.data.value}",
	)
	out, err := cmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get workload cluster kubeconfig secret")
	Expect(out).NotTo(BeEmpty(), "workload cluster kubeconfig secret is empty")

	// The value is base64-encoded in the secret.
	decodeCmd := exec.CommandContext(ctx, "base64", "-d")
	decodeCmd.Stdin = bytes.NewReader(out)
	decodedBytes, err := decodeCmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to base64 decode workload kubeconfig")

	Expect(os.WriteFile(outputPath, decodedBytes, 0600)).To(Succeed(),
		"Failed to write workload kubeconfig to %s", outputPath)
	GinkgoWriter.Printf("Workload kubeconfig written to %s\n", outputPath)
}

// waitForAPIServerReachable polls until kubectl cluster-info succeeds against
// the given kubeconfig, indicating the workload cluster API server is reachable.
func waitForAPIServerReachable(ctx context.Context, kubeconfig string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"cluster-info",
		)
		if err := cmd.Run(); err == nil {
			return
		}
		time.Sleep(15 * time.Second)
	}
	Fail(fmt.Sprintf("Workload cluster API server not reachable after %s", timeout))
}

// getEvrocMachineProviderIDs returns the providerID values from all EvrocMachines
// belonging to the given cluster. These are the evroc VM identifiers and are used
// to verify VMs survive the clusterctl move.
func getEvrocMachineProviderIDs(ctx context.Context, kubeconfig, clusterName, namespace string) []string {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evrocmachines",
		"-n", namespace,
		"-l", "cluster.x-k8s.io/cluster-name="+clusterName,
		"-o", "jsonpath={.items[*].spec.providerID}",
	)
	out, err := cmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get EvrocMachine providerIDs")
	ids := strings.Fields(strings.TrimSpace(string(out)))
	return ids
}

// clusterctlMove runs `clusterctl move` to pivot CAPI resources from the source
// management cluster to the target cluster.
func clusterctlMove(ctx context.Context, fromKubeconfig, toKubeconfig, namespace string) {
	cmd := exec.CommandContext(ctx, clusterctlPath(),
		"move",
		"--kubeconfig", fromKubeconfig,
		"--to-kubeconfig", toKubeconfig,
		"--namespace", namespace,
		"-v", "5",
	)
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	Expect(cmd.Run()).To(Succeed(), "clusterctl move failed")
}

// verifyResourceExists asserts that a resource of the given kind/name exists in the
// target cluster. Used to confirm resources landed after clusterctl move.
func verifyResourceExists(ctx context.Context, kubeconfig, kind, name, namespace string) {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", kind, name,
		"-n", namespace,
	)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "%s/%s must exist on target cluster: %s", kind, name, string(out))
}

// verifyResourceGone asserts that a resource no longer exists on the given cluster.
// Used to confirm resources were removed from the source after clusterctl move.
func verifyResourceGone(ctx context.Context, kubeconfig, kind, name, namespace string) {
	Eventually(func() bool {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"get", kind, name,
			"-n", namespace,
		)
		err := cmd.Run()
		return err != nil // true when the resource is gone (kubectl returns error)
	}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
		"%s/%s should have been removed from source cluster after move", kind, name)
}

// ─── Utility helpers ──────────────────────────────────────────────────────────

func intervalsToTimeDuration(intervals []interface{}, defaultTimeout, defaultPoll time.Duration) (time.Duration, time.Duration) {
	if len(intervals) >= 2 {
		t := parseDuration(intervals[0], defaultTimeout)
		p := parseDuration(intervals[1], defaultPoll)
		return t, p
	}
	return defaultTimeout, defaultPoll
}

func clusterctlPath() string {
	if p := os.Getenv("CLUSTERCTL_BINARY_PATH"); p != "" {
		return p
	}
	return "clusterctl"
}

func kubectlPath() string {
	if p := os.Getenv("CAPI_KUBECTL_PATH"); p != "" {
		return p
	}
	return "kubectl"
}

func randomSuffix() string {
	return fmt.Sprintf("%08x", GinkgoRandomSeed())
}

func parseDuration(v interface{}, def time.Duration) time.Duration {
	s, ok := v.(string)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}

// loadCredentialsFile loads EVROC credentials from a YAML/env file into the
// process environment. Only sets vars that are not already set.
func loadCredentialsFile() {
	credFile := os.Getenv("EVROC_CREDENTIALS_FILE")
	if credFile == "" {
		r := os.Getenv("REPO_ROOT")
		if r == "" {
			out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
			if err != nil {
				return
			}
			r = strings.TrimSpace(string(out))
		}
		credFile = filepath.Join(r, "test", "e2e", "config.yaml")
	}
	data, err := os.ReadFile(credFile)
	if err != nil {
		return // credentials via env vars is fine
	}

	// Use a generic intermediate type because config.yaml contains mixed
	// value types (strings, lists, maps). Strict typing to
	// map[string]map[string]string fails on non-variable sections.
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return
	}
	vars, ok := raw["variables"].(map[string]interface{})
	if !ok {
		return
	}

	for key, v := range vars {
		val, ok := v.(string)
		if !ok || val == "" {
			continue
		}
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}
