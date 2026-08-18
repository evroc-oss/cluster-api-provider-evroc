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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/evroc-oss/evroc-go-sdk/compute"
	"github.com/evroc-oss/evroc-go-sdk/filter"
	"github.com/evroc-oss/evroc-go-sdk/networking"
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

	// Wait for ALL provider deployments (core CAPI + kubeadm + evroc) to become ready.
	// All providers share the capi-evroc-system namespace (--target-namespace).
	By("Waiting for all provider deployments to be ready")
	waitForAllDeploymentsReady(ctx, kubeconfigPath, "capi-evroc-system", 10*time.Minute)
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

	Context("ClusterctlMove", Label("clusterctl-move"), func() {
		It("Should pivot a workload cluster to self-management without deleting VMs", func() {
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
			// Recorded once the LB exists; used post-teardown to assert cloud cleanup.
			var moveLBID string
			var moveOwnershipID string
			defer func() {
				collectClusterArtifacts(ctx, ownerKubeconfig, clusterName, namespace)

				if CurrentSpecReport().Failed() && os.Getenv("SKIP_CLEANUP_ON_FAILURE") == "true" {
					GinkgoWriter.Printf("SKIP_CLEANUP_ON_FAILURE=true and test failed — leaving VMs alive for inspection\n")
					GinkgoWriter.Printf("  ownerKubeconfig: %s\n", ownerKubeconfig)
					GinkgoWriter.Printf("  clusterName:     %s\n", clusterName)
					return
				}

				By("Deleting workload cluster from current owner")
				deleteCluster(ctx, ownerKubeconfig, clusterName, namespace)

				// The point of clusterctl move is not just that the cluster moves,
				// but that once moved it can be torn down and cleans up all its
				// cloud resources. The controller (now on the post-move owner with
				// a new cluster UID) must still find them by the stable
				// capi_cluster-id label. Assert that it did.
				if moveLBID != "" && moveOwnershipID != "" && !CurrentSpecReport().Failed() {
					By("Verifying all owned cloud resources were cleaned up after post-move teardown")
					verifyOwnedResourcesCleanedUp(ctx, newEvrocSDKClient(ctx), moveLBID, moveOwnershipID)
				}
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

			// ── Phase 2b: Install CNI so pods (especially cert-manager) can schedule ──
			By("Installing Calico CNI on workload cluster")
			installCalicoCNI(ctx, workloadKubeconfig)

			By("Waiting for Calico to be ready")
			waitForCNIReady(ctx, workloadKubeconfig, 5*time.Minute)

			// ── Phase 3: Deploy bastion and pre-load provider image ──
			bastionName := clusterName + "-bastion"
			evrocClient := newEvrocSDKClient(ctx)

			By("Creating bastion VM for SSH access to workload cluster nodes")
			bastionPublicIP := createBastionVM(ctx, evrocClient, bastionName)
			defer func() {
				By("Cleaning up bastion VM")
				deleteBastionVM(ctx, evrocClient, bastionName)
			}()

			By("Pre-loading provider image into workload cluster")
			loadProviderImageToRemoteCluster(ctx, kubeconfigPath, clusterName, namespace, bastionPublicIP)

			// Snapshot VM IDs after all machines are ready (loadProviderImageToRemoteCluster
			// polls until every EvrocMachine has an IP, so providerIDs are guaranteed set).
			By("Collecting EvrocMachine providerIDs before move")
			providerIDsBefore := getEvrocMachineProviderIDs(ctx, kubeconfigPath, clusterName, namespace)
			Expect(providerIDsBefore).NotTo(BeEmpty(), "should have at least one EvrocMachine with providerID")
			GinkgoWriter.Printf("Provider IDs before move: %v\n", providerIDsBefore)
			loadBalancerIDBefore := getEvrocClusterLoadBalancerID(ctx, kubeconfigPath, clusterName, namespace)
			GinkgoWriter.Printf("Load balancer ID before move: %s\n", loadBalancerIDBefore)
			moveLBID = loadBalancerIDBefore // for post-teardown cloud-cleanup assertion
			moveOwnershipID = getEvrocClusterOwnershipID(ctx, kubeconfigPath, clusterName, namespace)

			By("Pre-creating evroc credentials on workload cluster")
			applyEvrocCredentials(ctx, workloadKubeconfig)

			By("Applying evroc CRDs on workload cluster")
			applyEvrocCRDs(ctx, workloadKubeconfig)

			By("Running clusterctl init on workload cluster")
			clusterctlInit(ctx, workloadKubeconfig, true)

			By("Waiting for all provider deployments on workload cluster")
			waitForAllDeploymentsReady(ctx, workloadKubeconfig, "capi-evroc-system", 10*time.Minute)

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

			By("Verifying EvrocCluster is ready on target (controller reconciled after move)")
			waitForEvrocClusterReady(ctx, workloadKubeconfig, clusterName, namespace,
				e2eConfig.GetIntervals("default", "wait-cluster")...)

			By("Verifying the managed load balancer was adopted rather than recreated")
			loadBalancerIDAfter := getEvrocClusterLoadBalancerID(ctx, workloadKubeconfig, clusterName, namespace)
			Expect(loadBalancerIDAfter).To(Equal(loadBalancerIDBefore),
				"clusterctl move must preserve the managed load balancer identity")

			By("Verifying EvrocMachines are ready on target (post-move status reconstruction)")
			waitForEvrocMachineReady(ctx, workloadKubeconfig, clusterName, namespace,
				e2eConfig.GetIntervals("default", "wait-machines")...)

			// clusterctl move refuses to start until every Machine reports a
			// nodeRef (phase Running) and the control plane is initialized.
			// Right after the forward pivot the self-managing controller has not
			// yet re-populated Machine.status.nodeRef, so wait for all Machines
			// (control plane and workers) to reach Running before moving back.
			By("Waiting for all Machines to be Running on target before move-back")
			Eventually(func() error {
				cmd := exec.CommandContext(ctx, kubectlPath(),
					"--kubeconfig", workloadKubeconfig,
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
				if len(phases) == 0 {
					return fmt.Errorf("no machines found yet for cluster %s", clusterName)
				}
				for _, p := range phases {
					if p != string(clusterv1.MachinePhaseRunning) {
						return fmt.Errorf("cluster %s: machine phases not all Running: %v", clusterName, phases)
					}
				}
				return nil
			}, e2eConfig.GetIntervals("default", "wait-machines")...).Should(Succeed(),
				"not all Machines reached Running on target before move-back")

			// Move ownership back before cleanup. A self-managed cluster cannot
			// reliably finish deleting its own control plane after its API goes down.
			By("Moving the cluster back to the bootstrap manager for cleanup")
			clusterctlMove(ctx, workloadKubeconfig, kubeconfigPath, namespace)
			ownerKubeconfig = kubeconfigPath

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
			// An empty spec is missing several required fields (project,
			// credentialsRef). The CRD schema rejects credentialsRef before the
			// webhook reaches project, so accept either required field.
			Expect(string(out)).To(SatisfyAny(
				ContainSubstring("project"),
				ContainSubstring("credentialsRef"),
			), "Rejection should name a required field, got: %s", string(out))

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
  credentialsRef:
    name: evroc-credentials
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

		It("Should default failureDomains when not specified", func() {
			By("Applying an EvrocCluster with no failureDomains")
			cluster := []byte(`apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocCluster
metadata:
  name: fd-default-test
  namespace: default
spec:
  project: test-project
  region: se-sto
  credentialsRef:
    name: evroc-credentials
`)
			cmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfigPath,
				"apply", "-f", "-",
			)
			cmd.Stdin = bytes.NewReader(cluster)
			out, err := cmd.CombinedOutput()
			Expect(err).ToNot(HaveOccurred(),
				"Defaulting webhook should accept EvrocCluster and fill failureDomains: %s", string(out))

			// Verify defaults were applied
			getCmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfigPath,
				"get", "evroccluster", "fd-default-test", "-n", "default",
				"-o", "jsonpath={.spec.failureDomains}",
			)
			fdOut, err := getCmd.Output()
			Expect(err).ToNot(HaveOccurred(), "Failed to get EvrocCluster failureDomains")
			Expect(string(fdOut)).To(ContainSubstring("a"),
				"Defaulting webhook should set failureDomains to [a,b,c], got: %s", string(fdOut))

			// Clean up
			delCmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfigPath,
				"delete", "evroccluster", "fd-default-test", "-n", "default", "--ignore-not-found",
			)
			_ = delCmd.Run()
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
  credentialsRef:
    name: evroc-credentials
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

	Context("HA with Load Balancer (kubeadm)", Label("ha-lb"), func() {
		It("Should create a 3-CP HA cluster behind a load balancer and verify all CPs register as LB backends", func() {
			clusterName := fmt.Sprintf("evroc-ha-%s", randomSuffix())
			namespace := "default"

			By("Generating HA cluster manifest with LB (kubeadm, 3 CP)")
			clusterYAML := generateHALBClusterYAML(clusterName)

			By("Applying cluster resources")
			applyManifest(ctx, kubeconfigPath, clusterYAML, clusterName)

			defer func() {
				collectClusterArtifacts(ctx, kubeconfigPath, clusterName, namespace)

				By("Deleting HA workload cluster " + clusterName)
				deleteCluster(ctx, kubeconfigPath, clusterName, namespace)

				By("Verifying cluster resources are cleaned up after deletion")
				verifyClusterDeleted(ctx, kubeconfigPath, clusterName, namespace)
			}()

			By("Waiting for EvrocCluster to become ready")
			waitForEvrocClusterReady(ctx, kubeconfigPath, clusterName, namespace,
				e2eConfig.GetIntervals("default", "wait-cluster")...)

			By("Verifying EvrocCluster control plane endpoint (LB address)")
			verifyControlPlaneEndpoint(ctx, kubeconfigPath, clusterName, namespace)

			By("Waiting for all 3 control plane machines to reach Running")
			waitForAllMachinesRunning(ctx, kubeconfigPath, clusterName, namespace, 3,
				e2eConfig.GetIntervals("default", "wait-machines")...)

			By("Verifying LB has all 3 control plane backends registered")
			verifyLBBackends(ctx, kubeconfigPath, clusterName, namespace, 3)

			By("Verifying API server is reachable through the LB")
			workloadKubeconfig := filepath.Join(artifactFolder, clusterName+"-kubeconfig.yaml")
			waitForWorkloadKubeconfig(ctx, kubeconfigPath, clusterName, namespace, workloadKubeconfig,
				e2eConfig.GetIntervals("default", "wait-machines")...)
			waitForAPIServerReachable(ctx, workloadKubeconfig, 10*time.Minute)

			By("Verifying EvrocMachine addresses and providerID")
			verifyMachineStatus(ctx, kubeconfigPath, clusterName, namespace)

			By("HA cluster with LB provisioned successfully — HA-LB PASSED")
		})
	})

	Context("HA with Load Balancer (RKE2)", Label("ha-lb-rke2"), func() {
		It("Should create a 3-CP HA RKE2 cluster behind a load balancer and verify all CPs register as LB backends", func() {
			clusterName := fmt.Sprintf("evroc-rke2ha-%s", randomSuffix())
			namespace := "default"

			By("Installing CAPRKE2 bootstrap and control-plane providers")
			installCAPRKE2(ctx, kubeconfigPath)

			By("Generating HA RKE2 cluster manifest with LB (3 CP)")
			clusterYAML := generateRKE2HALBClusterYAML(clusterName)

			By("Applying cluster resources")
			applyManifest(ctx, kubeconfigPath, clusterYAML, clusterName)

			defer func() {
				collectClusterArtifacts(ctx, kubeconfigPath, clusterName, namespace)

				By("Deleting HA RKE2 workload cluster " + clusterName)
				deleteCluster(ctx, kubeconfigPath, clusterName, namespace)

				By("Verifying cluster resources are cleaned up after deletion")
				verifyClusterDeleted(ctx, kubeconfigPath, clusterName, namespace)
			}()

			By("Waiting for EvrocCluster to become ready")
			waitForEvrocClusterReady(ctx, kubeconfigPath, clusterName, namespace,
				e2eConfig.GetIntervals("default", "wait-cluster")...)

			By("Verifying EvrocCluster control plane endpoint (LB address)")
			verifyControlPlaneEndpoint(ctx, kubeconfigPath, clusterName, namespace)

			By("Waiting for all 3 control plane machines to reach Running")
			waitForAllMachinesRunning(ctx, kubeconfigPath, clusterName, namespace, 3,
				e2eConfig.GetIntervals("default", "wait-machines")...)

			By("Verifying LB has all 3 control plane backends registered")
			verifyLBBackends(ctx, kubeconfigPath, clusterName, namespace, 3)

			By("Verifying API server is reachable through the LB")
			workloadKubeconfig := filepath.Join(artifactFolder, clusterName+"-kubeconfig.yaml")
			waitForWorkloadKubeconfig(ctx, kubeconfigPath, clusterName, namespace, workloadKubeconfig,
				e2eConfig.GetIntervals("default", "wait-machines")...)
			waitForAPIServerReachable(ctx, workloadKubeconfig, 10*time.Minute)

			By("Verifying EvrocMachine addresses and providerID")
			verifyMachineStatus(ctx, kubeconfigPath, clusterName, namespace)

			By("HA RKE2 cluster with LB provisioned successfully — HA-LB-RKE2 PASSED")
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
// via SSH on every node. It queries EvrocMachine addresses from the bootstrap
// cluster. Nodes with ExternalIPs are reached directly; internal-only nodes
// are reached via SSH ProxyJump through bastionIP (or the first node with an ExternalIP).
func loadProviderImageToRemoteCluster(ctx context.Context, bootstrapKubeconfig, clusterName, namespace string, bastionIP ...string) {
	const defaultImage = "ghcr.io/evroc-oss/cluster-api-provider-evroc:latest"
	image := os.Getenv("E2E_LOCAL_IMAGE")
	if image == "" {
		image = defaultImage
	}

	tarPath := filepath.Join(os.TempDir(), "provider-image.tar")
	saveCmd := exec.CommandContext(ctx, "docker", "save", "-o", tarPath, image)
	saveOut, err := saveCmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "docker save failed: %s", string(saveOut))
	defer os.Remove(tarPath)

	sshKeyPath := os.Getenv("EVROC_SSH_PRIVATE_KEY")
	if sshKeyPath == "" {
		homeDir, _ := os.UserHomeDir()
		sshKeyPath = filepath.Join(homeDir, ".ssh", "id_ed25519")
	}

	type nodeTarget struct {
		name       string
		externalIP string
		internalIP string
	}

	// Poll until all EvrocMachines have at least an InternalIP.
	// Workers take longer to provision than CP, so we wait for all.
	// We use JSON output instead of jsonpath to avoid parsing edge-cases.
	type evrocMachineList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Addresses []struct {
					Type    string `json:"type"`
					Address string `json:"address"`
				} `json:"addresses"`
			} `json:"status"`
		} `json:"items"`
	}

	var nodes []nodeTarget
	var jumpHost string
	if len(bastionIP) > 0 && bastionIP[0] != "" {
		jumpHost = bastionIP[0]
	}
	Eventually(func() error {
		addrCmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", bootstrapKubeconfig,
			"get", "evrocmachines", "-n", namespace,
			"-l", fmt.Sprintf("cluster.x-k8s.io/cluster-name=%s", clusterName),
			"-o", "json",
		)
		addrOut, err := addrCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("kubectl get evrocmachines failed: %w\noutput: %s", err, string(addrOut))
		}

		var machineList evrocMachineList
		if err := json.Unmarshal(addrOut, &machineList); err != nil {
			return fmt.Errorf("failed to parse evrocmachines JSON: %w\nraw output (first 500 bytes): %s", err, string(addrOut[:min(len(addrOut), 500)]))
		}

		nodes = nil
		for _, item := range machineList.Items {
			n := nodeTarget{name: item.Metadata.Name}
			for _, addr := range item.Status.Addresses {
				// Dual-stack machines report both an IPv4 and an IPv6 address
				// per type. Prefer IPv4 for SSH: it avoids IPv6-literal parsing
				// issues (e.g. the "-W host:port" proxy spec) and the jump-host
				// path. Only fall back to IPv6 when no IPv4 is present.
				switch addr.Type {
				case "ExternalIP":
					if n.externalIP == "" || isIPv4(addr.Address) {
						n.externalIP = addr.Address
					}
				case "InternalIP":
					if n.internalIP == "" || isIPv4(addr.Address) {
						n.internalIP = addr.Address
					}
				}
			}
			nodes = append(nodes, n)
			if n.externalIP != "" && jumpHost == "" {
				jumpHost = n.externalIP
			}
		}

		if len(nodes) == 0 {
			return fmt.Errorf("no EvrocMachines found for cluster %s", clusterName)
		}
		for _, n := range nodes {
			if n.internalIP == "" && n.externalIP == "" {
				return fmt.Errorf("machine %s has no IP yet (waiting for VM to be provisioned)", n.name)
			}
		}
		GinkgoWriter.Printf("All %d EvrocMachines have addresses (jumpHost=%s)\n", len(nodes), jumpHost)
		return nil
	}, 10*time.Minute, 15*time.Second).Should(Succeed(),
		"not all EvrocMachines for cluster %s have IP addresses", clusterName)

	GinkgoWriter.Printf("Importing provider image to %d node(s), jumpHost=%s\n", len(nodes), jumpHost)

	for _, n := range nodes {
		targetIP := n.externalIP
		useJump := false
		if targetIP == "" {
			targetIP = n.internalIP
			useJump = true
		}
		Expect(targetIP).NotTo(BeEmpty(), "no IP for machine %s", n.name)

		GinkgoWriter.Printf("  Loading image on %s (%s, jump=%v)...\n", n.name, targetIP, useJump)

		args := []string{
			"-i", sshKeyPath,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "ConnectTimeout=30",
		}
		if useJump && jumpHost != "" {
			// Use ProxyCommand instead of -J so that the jump-host connection
			// also honours StrictHostKeyChecking=no / UserKnownHostsFile=/dev/null.
			// -J passes no extra options to the proxy hop, which causes
			// "Host key verification failed" on freshly-provisioned VMs.
			proxyCmd := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -W %%h:%%p evroc-user@%s", sshKeyPath, jumpHost)
			args = append(args, "-o", fmt.Sprintf("ProxyCommand=%s", proxyCmd))
		}
		args = append(args, fmt.Sprintf("evroc-user@%s", targetIP),
			"sudo", "ctr", "-n", "k8s.io", "images", "import", "-")

		// Retry SSH import until the node accepts it. A freshly provisioned VM
		// reports "Permission denied (publickey)" until cloud-init finishes
		// creating evroc-user and installing its authorized key, which on a
		// control-plane node can take several minutes. Poll at a fixed 30s
		// interval long enough (~15min) to outlast cloud-init.
		var sshOut []byte
		var sshErr error
		const maxRetries = 30
		const retryInterval = 30 * time.Second
		for attempt := 1; attempt <= maxRetries; attempt++ {
			sshCmd := exec.CommandContext(ctx, "ssh", args...)
			tarFile, openErr := os.Open(tarPath)
			Expect(openErr).ToNot(HaveOccurred())
			sshCmd.Stdin = tarFile
			sshOut, sshErr = sshCmd.CombinedOutput()
			tarFile.Close()
			if sshErr == nil {
				break
			}
			GinkgoWriter.Printf("  SSH attempt %d/%d to %s failed: %s (output: %s)\n",
				attempt, maxRetries, targetIP, sshErr, strings.TrimSpace(string(sshOut)))
			if attempt < maxRetries {
				time.Sleep(retryInterval)
			}
		}
		Expect(sshErr).ToNot(HaveOccurred(), "SSH ctr import to %s (%s) failed after %d attempts: %s", n.name, targetIP, maxRetries, string(sshOut))
		GinkgoWriter.Printf("  Image imported to %s: %s\n", n.name, strings.TrimSpace(string(sshOut)))
	}
}

// isIPv4 reports whether addr is an IPv4 literal. On dual-stack clusters the
// same address type carries both families; SSH targets prefer the IPv4 one.
// IPv6 literals always contain a colon, IPv4 never does.
func isIPv4(addr string) bool {
	return addr != "" && !strings.Contains(addr, ":")
}

// applyEvrocCredentials creates the capi-evroc-system namespace and the
// evroc-credentials secret from environment variables.
func applyEvrocCredentials(ctx context.Context, kubeconfig string) {
	secret := buildCredentialsYAML()
	applyRawYAML(ctx, kubeconfig, secret)
}

func buildCredentialsYAML() []byte {
	saID := os.Getenv("EVROC_SERVICE_ACCOUNT_ID")
	saSecret := os.Getenv("EVROC_SERVICE_ACCOUNT_SECRET")
	organization := os.Getenv("EVROC_ORGANIZATION")

	if saID == "" || saSecret == "" {
		Fail("EVROC_SERVICE_ACCOUNT_ID and EVROC_SERVICE_ACCOUNT_SECRET must be set")
	}

	// The provider requires flat service-account keys (serviceAccountID,
	// serviceAccountSecret, optional organization). Project and region come
	// from the EvrocCluster spec, not the secret. The pre-v0.2.1 config.yaml
	// format is removed and rejected by the controller.
	stringData := fmt.Sprintf("  serviceAccountID: %q\n  serviceAccountSecret: %q\n", saID, saSecret)
	if organization != "" {
		stringData += fmt.Sprintf("  organization: %q\n", organization)
	}

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
%s---
apiVersion: v1
kind: Secret
metadata:
  name: evroc-credentials
  namespace: default
type: Opaque
stringData:
%s`, stringData, stringData))
}

// applyEvrocCRDs applies the evroc CRDs directly to avoid race conditions.
func applyEvrocCRDs(ctx context.Context, kubeconfig string) {
	// CRDs are generated into the Helm chart, not config/crd/bases.
	crdDir := filepath.Join(repoRoot, "helm", "cluster-api-provider-evroc", "crds")
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
		"CLUSTER_NAME":                        clusterName,
		"EVROC_PROJECT":                       e2eConfig.GetVariable("EVROC_PROJECT"),
		"EVROC_REGION":                        e2eConfig.GetVariable("EVROC_REGION"),
		"EVROC_API_BASE_URL":                  e2eConfig.GetVariable("EVROC_API_BASE_URL"),
		"EVROC_ISSUER_URL":                    e2eConfig.GetVariable("EVROC_ISSUER_URL"),
		"EVROC_AVAILABILITY_ZONE":             e2eConfig.GetVariable("EVROC_AVAILABILITY_ZONE"),
		"KUBERNETES_VERSION":                  e2eConfig.MustGetVariable("KUBERNETES_VERSION"),
		"CONTROL_PLANE_MACHINE_COUNT":         e2eConfig.MustGetVariable("CONTROL_PLANE_MACHINE_COUNT"),
		"WORKER_MACHINE_COUNT":                e2eConfig.MustGetVariable("WORKER_MACHINE_COUNT"),
		"EVROC_CONTROL_PLANE_COMPUTE_PROFILE": e2eConfig.MustGetVariable("EVROC_CONTROL_PLANE_FLAVOR"),
		"EVROC_IMAGE":                         e2eConfig.MustGetVariable("EVROC_IMAGE"),
		"EVROC_CONTROL_PLANE_DISK_SIZE":       e2eConfig.MustGetVariable("EVROC_CONTROL_PLANE_DISK_SIZE"),
		"EVROC_SSH_KEY":                       e2eConfig.GetVariable("EVROC_SSH_KEY"),
		"EVROC_CREDENTIALS_SECRET":            e2eConfig.GetVariable("EVROC_CREDENTIALS_SECRET"),
		"XDG_CONFIG_HOME":                     filepath.Join(artifactFolder, "xdg"),
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

// generateHALBClusterYAML generates a 3-CP HA cluster manifest using the
// ha-lb template (kubeadm with load balancer).
func generateHALBClusterYAML(clusterName string) []byte {
	templatePath := filepath.Join(repoRoot, "templates", "cluster-template-ha-lb.yaml")

	cmd := exec.CommandContext(ctx, clusterctlPath(),
		"generate", "cluster", clusterName,
		"--from", templatePath,
		"--target-namespace", "default",
	)
	overrides := map[string]string{
		"CLUSTER_NAME":                        clusterName,
		"EVROC_PROJECT":                       e2eConfig.GetVariable("EVROC_PROJECT"),
		"EVROC_REGION":                        e2eConfig.GetVariable("EVROC_REGION"),
		"EVROC_API_BASE_URL":                  e2eConfig.GetVariable("EVROC_API_BASE_URL"),
		"EVROC_ISSUER_URL":                    e2eConfig.GetVariable("EVROC_ISSUER_URL"),
		"EVROC_AVAILABILITY_ZONE":             e2eConfig.GetVariable("EVROC_AVAILABILITY_ZONE"),
		"KUBERNETES_VERSION":                  e2eConfig.MustGetVariable("KUBERNETES_VERSION"),
		"CONTROL_PLANE_MACHINE_COUNT":         "3",
		"WORKER_MACHINE_COUNT":                "0",
		"EVROC_CONTROL_PLANE_COMPUTE_PROFILE": e2eConfig.MustGetVariable("EVROC_CONTROL_PLANE_FLAVOR"),
		"EVROC_IMAGE":                         e2eConfig.MustGetVariable("EVROC_IMAGE"),
		"EVROC_CONTROL_PLANE_DISK_SIZE":       e2eConfig.MustGetVariable("EVROC_CONTROL_PLANE_DISK_SIZE"),
		"EVROC_SSH_KEY":                       e2eConfig.GetVariable("EVROC_SSH_KEY"),
		"EVROC_CREDENTIALS_SECRET":            e2eConfig.GetVariable("EVROC_CREDENTIALS_SECRET"),
		"XDG_CONFIG_HOME":                     filepath.Join(artifactFolder, "xdg"),
	}
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
	Expect(cmd.Run()).To(Succeed(), "clusterctl generate cluster (ha-lb) failed: %s", stderr.String())

	outputFile := filepath.Join(artifactFolder, clusterName+".yaml")
	Expect(os.WriteFile(outputFile, stdout.Bytes(), os.ModePerm)).To(Succeed())
	return stdout.Bytes()
}

// generateRKE2HALBClusterYAML generates a 3-CP HA RKE2 cluster manifest using the
// rke2 template with load balancer (auto-created by EvrocCluster controller).
func generateRKE2HALBClusterYAML(clusterName string) []byte {
	templatePath := filepath.Join(repoRoot, "templates", "cluster-template-rke2.yaml")

	cmd := exec.CommandContext(ctx, clusterctlPath(),
		"generate", "cluster", clusterName,
		"--from", templatePath,
		"--target-namespace", "default",
	)
	overrides := map[string]string{
		"CLUSTER_NAME":                        clusterName,
		"EVROC_PROJECT":                       e2eConfig.GetVariable("EVROC_PROJECT"),
		"EVROC_REGION":                        e2eConfig.GetVariable("EVROC_REGION"),
		"EVROC_API_BASE_URL":                  e2eConfig.GetVariable("EVROC_API_BASE_URL"),
		"EVROC_ISSUER_URL":                    e2eConfig.GetVariable("EVROC_ISSUER_URL"),
		"EVROC_AVAILABILITY_ZONE":             e2eConfig.GetVariable("EVROC_AVAILABILITY_ZONE"),
		"KUBERNETES_VERSION":                  "v1.30.0+rke2r1",
		"CONTROL_PLANE_MACHINE_COUNT":         "3",
		"WORKER_MACHINE_COUNT":                "0",
		"EVROC_CONTROL_PLANE_COMPUTE_PROFILE": e2eConfig.GetVariable("EVROC_CONTROL_PLANE_FLAVOR"),
		"EVROC_IMAGE":                         "ubuntu.22-04.1",
		"EVROC_CONTROL_PLANE_DISK_SIZE":       e2eConfig.MustGetVariable("EVROC_CONTROL_PLANE_DISK_SIZE"),
		"EVROC_SSH_KEY":                       e2eConfig.GetVariable("EVROC_SSH_KEY"),
		"EVROC_CREDENTIALS_SECRET":            e2eConfig.GetVariable("EVROC_CREDENTIALS_SECRET"),
		"XDG_CONFIG_HOME":                     filepath.Join(artifactFolder, "xdg"),
	}
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
	Expect(cmd.Run()).To(Succeed(), "clusterctl generate cluster (rke2 ha) failed: %s", stderr.String())

	outputFile := filepath.Join(artifactFolder, clusterName+".yaml")
	Expect(os.WriteFile(outputFile, stdout.Bytes(), os.ModePerm)).To(Succeed())
	return stdout.Bytes()
}

// installCAPRKE2 downloads and installs the CAPRKE2 bootstrap and control-plane
// providers from their GitHub release YAMLs. Variable placeholders are replaced
// with defaults before applying.
func installCAPRKE2(ctx context.Context, kubeconfig string) {
	caprke2Version := "v0.24.1"
	baseURL := "https://github.com/rancher/cluster-api-provider-rke2/releases/download/" + caprke2Version

	varPattern := regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*:=([^}]*)\}`)

	for _, comp := range []string{"bootstrap-components.yaml", "control-plane-components.yaml"} {
		compURL := baseURL + "/" + comp
		GinkgoWriter.Printf("Downloading CAPRKE2 %s from %s\n", comp, compURL)

		dlCmd := exec.CommandContext(ctx, "curl", "-sL", compURL)
		body, err := dlCmd.Output()
		Expect(err).ToNot(HaveOccurred(), "Failed to download %s", comp)

		content := varPattern.ReplaceAllString(string(body), "$1")

		compFile := filepath.Join(artifactFolder, "caprke2-"+comp)
		Expect(os.WriteFile(compFile, []byte(content), 0600)).To(Succeed())

		applyCmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"apply", "-f", compFile,
		)
		out, applyErr := applyCmd.CombinedOutput()
		Expect(applyErr).ToNot(HaveOccurred(), "Failed to apply CAPRKE2 %s: %s", comp, string(out))
		GinkgoWriter.Printf("Applied CAPRKE2 %s successfully\n", comp)
	}

	// Wait for CAPRKE2 CRDs to be available.
	for _, crd := range []string{
		"rke2controlplanes.controlplane.cluster.x-k8s.io",
		"rke2configtemplates.bootstrap.cluster.x-k8s.io",
	} {
		Eventually(func() error {
			cmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfig,
				"get", "crd", crd,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("CRD %s not ready: %s", crd, string(out))
			}
			return nil
		}, 5*time.Minute, 10*time.Second).Should(Succeed(), "CAPRKE2 CRD not established: "+crd)
	}

	// Wait for webhook endpoints to be serving.
	for _, svc := range []struct{ name, ns string }{
		{"rke2-bootstrap-webhook-service", "rke2-bootstrap-system"},
		{"rke2-control-plane-webhook-service", "rke2-control-plane-system"},
	} {
		Eventually(func() error {
			cmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfig,
				"get", "endpoints", svc.name, "-n", svc.ns,
				"-o", "jsonpath={.subsets[*].addresses[*].ip}",
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("webhook %s not ready: %s", svc.name, string(out))
			}
			if len(strings.TrimSpace(string(out))) < 3 {
				return fmt.Errorf("webhook %s has no endpoints", svc.name)
			}
			return nil
		}, 5*time.Minute, 10*time.Second).Should(Succeed(), "CAPRKE2 webhook not ready: "+svc.name)
	}
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
		"--wait=false",
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
	debugInterval := 30 * time.Second
	lastDebug := time.Time{}
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
		// Log namespace-wide status periodically for debugging.
		if time.Since(lastDebug) >= debugInterval {
			// Show ALL deployments in namespace (not just label-filtered).
			allDeplCmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfig,
				"get", "deployments", "-n", namespace, "-o", "wide",
			)
			allDeplOut, _ := allDeplCmd.CombinedOutput()
			GinkgoWriter.Printf("  [debug] all deployments in %s:\n%s\n", namespace, strings.TrimSpace(string(allDeplOut)))

			// Show ALL pods in namespace.
			allPodsCmd := exec.CommandContext(ctx, kubectlPath(),
				"--kubeconfig", kubeconfig,
				"get", "pods", "-n", namespace, "-o", "wide",
			)
			allPodsOut, _ := allPodsCmd.CombinedOutput()
			GinkgoWriter.Printf("  [debug] all pods in %s:\n%s\n", namespace, strings.TrimSpace(string(allPodsOut)))
			lastDebug = time.Now()
		}
		time.Sleep(10 * time.Second)
	}
	// Capture final state for debugging before failing.
	descCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"describe", "deployments,pods", "-n", namespace,
	)
	descOut, _ := descCmd.CombinedOutput()
	GinkgoWriter.Printf("  [debug] final describe of all deployments/pods in %s:\n%s\n", namespace, string(descOut))
	Fail(fmt.Sprintf("Deployment with provider label %q in namespace %q not ready after %s", labelSelector, namespace, timeout))
}

// waitForAllDeploymentsReady polls until every deployment in the given namespace
// has at least 1 ready replica. This ensures all CAPI providers (core, bootstrap,
// control-plane, infrastructure) and their webhooks are serving before tests proceed.
func waitForAllDeploymentsReady(ctx context.Context, kubeconfig, namespace string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Get all deployments and their ready/desired replica counts.
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"get", "deployments", "-n", namespace,
			"-o", "jsonpath={range .items[*]}{.metadata.name}={.status.readyReplicas}/{.spec.replicas} {end}",
		)
		out, err := cmd.Output()
		if err == nil {
			fields := strings.Fields(strings.TrimSpace(string(out)))
			if len(fields) > 0 {
				allReady := true
				for _, f := range fields {
					// Each field is "name=ready/desired"
					parts := strings.SplitN(f, "=", 2)
					if len(parts) != 2 {
						allReady = false
						break
					}
					counts := strings.SplitN(parts[1], "/", 2)
					if len(counts) != 2 || counts[0] == "" || counts[0] == "<nil>" || counts[0] == "0" {
						allReady = false
						break
					}
				}
				if allReady {
					GinkgoWriter.Printf("All deployments ready in %s: %s\n", namespace, string(out))
					return
				}
			}
		}
		GinkgoWriter.Printf("  [debug] deployments in %s: %s\n", namespace, strings.TrimSpace(string(out)))
		time.Sleep(10 * time.Second)
	}
	// Final debug dump before failing.
	descCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"describe", "deployments,pods", "-n", namespace,
	)
	descOut, _ := descCmd.CombinedOutput()
	GinkgoWriter.Printf("  [debug] final describe of all deployments/pods in %s:\n%s\n", namespace, string(descOut))
	Fail(fmt.Sprintf("Not all deployments in namespace %q became ready after %s", namespace, timeout))
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
	timeout, poll := 5*time.Minute, 10*time.Second

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

	// Verify SG IDs are non-empty
	idCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evroccluster", clusterName,
		"-n", namespace,
		"-o", "jsonpath={.status.resources.securityGroups[*].id}",
	)
	idOut, err := idCmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get SG IDs from status")
	names := strings.Fields(strings.TrimSpace(string(idOut)))
	Expect(names).To(HaveLen(len(roles)), "Each SG should have an ID")
	for _, name := range names {
		Expect(name).ToNot(BeEmpty(), "SG ID should not be empty")
		Expect(name).To(ContainSubstring(clusterName),
			"SG cloud ID should contain the cluster name, got: %s", name)
	}

	GinkgoWriter.Printf("SG status verified: roles=%v ids=%v\n", roles, names)
}

// verifyMachineStatus checks that EvrocMachines have populated addresses and
// a providerID in the expected evroc://<vm-id> format.
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

	// Verify LB is tracked in status
	lbCmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evroccluster", clusterName,
		"-n", namespace,
		"-o", "jsonpath={.status.resources.loadBalancer.id}",
	)
	lbOut, err := lbCmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get loadBalancer status")
	lbID := strings.TrimSpace(string(lbOut))
	Expect(lbID).ToNot(BeEmpty(), "loadBalancer.id should be tracked in status")

	GinkgoWriter.Printf("CP endpoint verified: host=%s port=%s loadBalancer=%s\n", host, port, lbID)
}

// ─── HA / Load Balancer verification helpers ─────────────────────────────────

// waitForAllMachinesRunning waits until exactly expectedCount machines for the
// cluster are in Running phase. This is stricter than waitForMachineProvisioned
// (which only requires at least one).
func waitForAllMachinesRunning(ctx context.Context, kubeconfig, clusterName, namespace string, expectedCount int, intervals ...interface{}) {
	timeout, poll := intervalsToTimeDuration(intervals, 30*time.Minute, 30*time.Second)

	Eventually(func() error {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"get", "machines",
			"-n", namespace,
			"-l", "cluster.x-k8s.io/cluster-name="+clusterName+",cluster.x-k8s.io/control-plane",
			"-o", "jsonpath={.items[*].status.phase}",
		)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("kubectl get machines failed: %w", err)
		}
		phases := strings.Fields(string(out))
		running := 0
		for _, p := range phases {
			if p == string(clusterv1.MachinePhaseRunning) {
				running++
			}
		}
		if running < expectedCount {
			return fmt.Errorf("cluster %s: %d/%d CP machines Running (phases: %v)", clusterName, running, expectedCount, phases)
		}
		return nil
	}, timeout, poll).Should(Succeed(), "Not all %d CP machines reached Running for cluster %s", expectedCount, clusterName)
}

// verifyLBBackends checks that the EvrocCluster status reports the expected
// number of load balancer backends, confirming all CP machines registered.
func verifyLBBackends(ctx context.Context, kubeconfig, clusterName, namespace string, expectedCount int) {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evroccluster", clusterName,
		"-n", namespace,
		"-o", "jsonpath={.status.resources.loadBalancer.backends}",
	)
	out, err := cmd.Output()
	Expect(err).ToNot(HaveOccurred(), "Failed to get LB backends from EvrocCluster status")

	backends := strings.TrimSpace(string(out))
	Expect(backends).ToNot(BeEmpty(), "LB backends list should not be empty")

	// The jsonpath returns a JSON array like ["vm1","vm2","vm3"]
	// Count the entries by splitting on commas within the array.
	backendCount := strings.Count(backends, ",") + 1
	if backends == "[]" {
		backendCount = 0
	}

	Expect(backendCount).To(Equal(expectedCount),
		"Expected %d LB backends but got %d: %s", expectedCount, backendCount, backends)

	GinkgoWriter.Printf("LB backends verified: count=%d backends=%s\n", backendCount, backends)
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

// installCalicoCNI applies Calico's static manifest to the workload cluster.
// This is required so that pods (especially cert-manager, needed by clusterctl init)
// can be scheduled on the single-node workload cluster.
// Calico auto-detects the pod CIDR from kubeadm's cluster configuration.
func installCalicoCNI(ctx context.Context, kubeconfig string) {
	calicoURL := "https://raw.githubusercontent.com/projectcalico/calico/v3.29.3/manifests/calico.yaml"
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"apply", "-f", calicoURL,
	)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "Failed to install Calico CNI: %s", string(out))
	GinkgoWriter.Printf("Calico CNI installed successfully\n")
}

// waitForCNIReady waits for calico-node pods to be running in kube-system,
// indicating that the CNI is operational and pods can be scheduled.
func waitForCNIReady(ctx context.Context, kubeconfig string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.CommandContext(ctx, kubectlPath(),
			"--kubeconfig", kubeconfig,
			"get", "pods",
			"-n", "kube-system",
			"-l", "k8s-app=calico-node",
			"-o", "jsonpath={.items[*].status.phase}",
		)
		out, err := cmd.Output()
		if err == nil {
			phases := strings.Fields(strings.TrimSpace(string(out)))
			if len(phases) > 0 {
				allRunning := true
				for _, p := range phases {
					if p != "Running" {
						allRunning = false
						break
					}
				}
				if allRunning {
					GinkgoWriter.Printf("Calico pods are Running\n")
					return
				}
			}
		}
		time.Sleep(15 * time.Second)
	}
	Fail(fmt.Sprintf("Calico CNI pods not ready after %s", timeout))
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

// getEvrocClusterLoadBalancerID returns the managed load balancer's stable evroc
// resource ID. A clusterctl move must not replace it when the Kubernetes object
// receives a new UID in the target management cluster.
func getEvrocClusterLoadBalancerID(ctx context.Context, kubeconfig, clusterName, namespace string) string {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evroccluster", clusterName,
		"-n", namespace,
		"-o", "jsonpath={.status.resources.loadBalancer.id}",
	)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "Failed to get EvrocCluster load balancer ID: %s", string(out))
	id := strings.TrimSpace(string(out))
	Expect(id).ToNot(BeEmpty(), "EvrocCluster load balancer ID must be recorded in status")
	return id
}

func getEvrocClusterOwnershipID(ctx context.Context, kubeconfig, clusterName, namespace string) string {
	cmd := exec.CommandContext(ctx, kubectlPath(),
		"--kubeconfig", kubeconfig,
		"get", "evroccluster", clusterName,
		"-n", namespace,
		"-o", "jsonpath={.metadata.annotations.evroccluster\\.infrastructure\\.cluster\\.x-k8s\\.io/ownership-id}",
	)
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "Failed to get EvrocCluster ownership ID: %s", string(out))
	id := strings.TrimSpace(string(out))
	Expect(id).ToNot(BeEmpty(), "EvrocCluster immutable ownership ID must be persisted")
	return id
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

// ─── Bastion VM helpers ──────────────────────────────────────────────────────

// newEvrocSDKClient creates an evroc SDK client from environment variables.
func newEvrocSDKClient(ctx context.Context) *evroc.Client {
	client, err := evroc.NewFromEnv(ctx)
	Expect(err).ToNot(HaveOccurred(), "Failed to create evroc SDK client from env")
	return client
}

// verifyLBResourcesCleanedUp asserts that, after the cluster has been deleted,
// the managed load balancer and every sub-resource it owned are gone from
// evroc. Sub-resources are matched by the stable capi_cluster-id ownership
// label (clusterID = the LB resource prefix), which is what makes teardown work
// after a clusterctl move — the live UID changes, so selecting on it would miss
// pre-move resources and leak them. This is the assertion that proves the fix:
// move the cluster, delete it from its new owner, and confirm nothing is left.
func verifyOwnedResourcesCleanedUp(ctx context.Context, sdk *evroc.Client, lbID, ownershipID string) {
	lbc := sdk.LoadBalancer()
	selector := filter.WithLabelSelector(fmt.Sprintf(
		"capi_managed-by=cluster-api-provider-evroc,capi_cluster-id=%s", ownershipID))

	Eventually(func() error {
		if _, err := lbc.LoadBalancers().Get(ctx, lbID); err == nil {
			return fmt.Errorf("load balancer %q still exists", lbID)
		} else if !errors.Is(err, evroc.ErrNotFound) {
			return fmt.Errorf("get load balancer %q: %w", lbID, err)
		}
		pools, err := lbc.BackendPools().List(ctx, selector)
		if err != nil {
			return fmt.Errorf("list backend pools: %w", err)
		}
		if n := len(pools.Items); n > 0 {
			return fmt.Errorf("%d owned backend pool(s) remain", n)
		}
		svcs, err := lbc.BackendServices().List(ctx, selector)
		if err != nil {
			return fmt.Errorf("list backend services: %w", err)
		}
		if n := len(svcs.Items); n > 0 {
			return fmt.Errorf("%d owned backend service(s) remain", n)
		}
		routes, err := lbc.L4Routes().List(ctx, selector)
		if err != nil {
			return fmt.Errorf("list l4 routes: %w", err)
		}
		if n := len(routes.Items); n > 0 {
			return fmt.Errorf("%d owned l4 route(s) remain", n)
		}
		ips, err := sdk.Networking().PublicIPs().List(ctx, selector)
		if err != nil {
			return fmt.Errorf("list public IPs: %w", err)
		}
		if n := len(ips.Items); n > 0 {
			return fmt.Errorf("%d owned public IP(s) remain", n)
		}
		sgs, err := sdk.Networking().SecurityGroups().List(ctx, selector)
		if err != nil {
			return fmt.Errorf("list security groups: %w", err)
		}
		if n := len(sgs.Items); n > 0 {
			return fmt.Errorf("%d owned security group(s) remain", n)
		}
		disks, err := sdk.Compute().Disks().List(ctx, selector)
		if err != nil {
			return fmt.Errorf("list disks: %w", err)
		}
		if n := len(disks.Items); n > 0 {
			return fmt.Errorf("%d owned disk(s) remain", n)
		}
		return nil
	}, 10*time.Minute, 15*time.Second).Should(Succeed(),
		"all resources owned by cluster %q must be cleaned up after post-move teardown", ownershipID)
}

// createBastionVM creates a small VM with a public IP to use as an SSH jump host.
// Returns the public IP address of the bastion. The caller must call deleteBastionVM
// to clean up.
func createBastionVM(ctx context.Context, client *evroc.Client, bastionName string) string {
	sshKey := os.Getenv("EVROC_SSH_KEY")
	zone := e2eConfig.GetVariable("EVROC_AVAILABILITY_ZONE")
	if zone == "" {
		zone = "a"
	}

	GinkgoWriter.Printf("Creating bastion VM %s with public IP...\n", bastionName)

	// 1. Create a public IP for the bastion
	publicIPName := bastionName + "-ip"
	_, err := networking.NewPublicIPBuilder(publicIPName).
		WithLabels(map[string]string{"purpose": "e2e-bastion"}).
		Create(ctx, client.Networking().PublicIPs())
	Expect(err).ToNot(HaveOccurred(), "Failed to create bastion public IP")

	// 2. Create a boot disk for the bastion
	diskName := bastionName + "-disk"
	_, err = compute.NewDiskBuilder(diskName).
		WithImage("ubuntu-minimal.24-04.1").
		WithSizeGB(20).
		WithZone(zone).
		WithLabels(map[string]string{"purpose": "e2e-bastion"}).
		Create(ctx, client.Compute().Disks())
	Expect(err).ToNot(HaveOccurred(), "Failed to create bastion disk")

	// Wait for disk to be ready
	_, err = client.Compute().Disks().WaitForReady(ctx, diskName, 5*time.Minute)
	Expect(err).ToNot(HaveOccurred(), "Bastion disk did not become ready")

	// 3. Create a security group for the bastion (SSH in + all egress)
	sgName := bastionName + "-sg"
	_, err = networking.NewSecurityGroupBuilder(sgName).
		AllowIngressRule("allow-ssh", "TCP", 22, 0, "0.0.0.0/0").
		AllowAllEgress().
		WithLabels(map[string]string{"purpose": "e2e-bastion"}).
		Create(ctx, client.Networking().SecurityGroups())
	if err != nil {
		GinkgoWriter.Printf("Warning: failed to create bastion SG (may already exist): %v\n", err)
	}

	// 4. Create the bastion VM
	vmReq := compute.NewVirtualMachineBuilder(bastionName).
		WithVMInstanceType("a1a.xs").
		WithBootDisk(compute.DiskRef(diskName)).
		WithPublicIP(compute.PublicIPRef(publicIPName)).
		WithSecurityGroup(compute.SecurityGroupRef(sgName)).
		WithZone(zone).
		WithLabels(map[string]string{"purpose": "e2e-bastion"})
	if sshKey != "" {
		vmReq = vmReq.WithSSHKey(sshKey)
	}

	_, err = client.Compute().VirtualMachines().Create(ctx, vmReq.Build())
	Expect(err).ToNot(HaveOccurred(), "Failed to create bastion VM")

	// 4. Wait for the VM to be ready and get its public IP
	vm, err := client.Compute().VirtualMachines().WaitForReady(ctx, bastionName, 10*time.Minute)
	Expect(err).ToNot(HaveOccurred(), "Bastion VM did not become ready")

	Expect(vm.Status.Networking).ToNot(BeNil(), "Bastion VM has no networking status")
	Expect(vm.Status.Networking.PublicIPv4Address).ToNot(BeNil(), "Bastion VM has no public IP")

	publicIP := *vm.Status.Networking.PublicIPv4Address
	Expect(publicIP).ToNot(BeEmpty(), "Bastion public IP is empty")

	GinkgoWriter.Printf("Bastion VM %s ready with public IP %s\n", bastionName, publicIP)
	return publicIP
}

// deleteBastionVM deletes the bastion VM, its disk, and its public IP. Best effort.
func deleteBastionVM(ctx context.Context, client *evroc.Client, bastionName string) {
	GinkgoWriter.Printf("Cleaning up bastion VM %s...\n", bastionName)

	// Delete VM (best effort)
	if err := client.Compute().VirtualMachines().Delete(ctx, bastionName); err != nil {
		GinkgoWriter.Printf("Warning: failed to delete bastion VM: %v\n", err)
	} else {
		_ = client.Compute().VirtualMachines().WaitForDeleted(ctx, bastionName, 5*time.Minute)
	}

	// Delete disk (best effort)
	diskName := bastionName + "-disk"
	if err := client.Compute().Disks().Delete(ctx, diskName); err != nil {
		GinkgoWriter.Printf("Warning: failed to delete bastion disk: %v\n", err)
	}

	// Delete public IP (best effort)
	publicIPName := bastionName + "-ip"
	if err := client.Networking().PublicIPs().Delete(ctx, publicIPName); err != nil {
		GinkgoWriter.Printf("Warning: failed to delete bastion public IP: %v\n", err)
	}

	// Delete security group (best effort — must wait for VM to be fully gone)
	sgName := bastionName + "-sg"
	if err := client.Networking().SecurityGroups().Delete(ctx, sgName); err != nil {
		GinkgoWriter.Printf("Warning: failed to delete bastion SG: %v\n", err)
	}

	GinkgoWriter.Printf("Bastion cleanup done\n")
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

	// Test helpers that build their own SDK client via evroc.NewFromEnv read the
	// SDK's own endpoint variables, which differ from the ones the templates use.
	// Without this mapping such a helper silently falls back to the public evroc
	// cloud and fails to authenticate with another environment's credentials.
	if v := os.Getenv("EVROC_API_BASE_URL"); v != "" && os.Getenv("EVROC_API_URL") == "" {
		_ = os.Setenv("EVROC_API_URL", v)
	}
	if v := os.Getenv("EVROC_ISSUER_URL"); v != "" && os.Getenv("EVROC_TOKEN_URL") == "" {
		// Mirrors EndpointsConfig.GetAuthTokenURL in api/v1beta1.
		_ = os.Setenv("EVROC_TOKEN_URL", strings.TrimSuffix(v, "/")+"/protocol/openid-connect/token")
	}
}
