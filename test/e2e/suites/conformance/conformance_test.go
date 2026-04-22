// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

// Package conformance_test validates that the evroc CAPI provider types implement
// the required CAPI provider contracts without requiring a live cluster.
// Tests validate the generated CRD YAML files to ensure all mandatory fields
// defined by the CAPI contract are present.
//
// Run with: go test ./suites/conformance/... (or via make test-e2e-conformance)
// Requires 'make manifests' to have been run first.
package conformance_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Test suite entry point
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Evroc Provider CAPI Conformance Suite")
}

var (
	// repoRoot is the path to the repository root, resolved relative to this file's location.
	// Tests run from suites/conformance/ so we go up 4 levels to reach the repo root.
	repoRoot string
)

var _ = BeforeSuite(func() {
	fmt.Fprintf(GinkgoWriter, "Setting up CAPI conformance test suite\n")

	// Resolve repo root: suites/conformance → test/e2e → test → repo root
	// Allow override via REPO_ROOT env var for flexibility
	if r := os.Getenv("REPO_ROOT"); r != "" {
		repoRoot = r
	} else {
		// Resolve repo root via git
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		Expect(err).NotTo(HaveOccurred(), "Failed to find repo root via 'git rev-parse --show-toplevel'")
		repoRoot = strings.TrimSpace(string(out))
	}

	fmt.Fprintf(GinkgoWriter, "Using repo root: %s\n", repoRoot)

	// Verify CRD directory exists
	crdDir := filepath.Join(repoRoot, "config", "crd", "bases")
	Expect(crdDir).To(BeADirectory(),
		"CRD directory must exist at %s - run 'make manifests' first", crdDir)
})

var _ = AfterSuite(func() {
	fmt.Fprintf(GinkgoWriter, "CAPI conformance suite complete\n")
})

// CAPI contract compliance tests.
// These tests verify that the evroc provider types implement the required CAPI contracts
// as defined in https://cluster-api.sigs.k8s.io/developer/providers/contracts/
var _ = Describe("[conformance] CAPI Provider Contract", Label("capi-contract"), func() {

	Context("InfrastructureCluster (EvrocCluster)", func() {
		It("should have required spec fields", func() {
			By("Checking ControlPlaneEndpoint field is present")
			// ControlPlaneEndpoint is required by the InfrastructureCluster contract:
			// https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-cluster
			crdContent := readCRD("evrocclusters.infrastructure.cluster.x-k8s.io")
			Expect(crdContent).To(ContainSubstring("controlPlaneEndpoint"),
				"EvrocCluster spec must contain controlPlaneEndpoint (required by CAPI InfrastructureCluster contract)")
		})

		It("should have required status fields", func() {
			By("Checking ready status field is present")
			// Ready is required by the InfrastructureCluster contract
			crdContent := readCRD("evrocclusters.infrastructure.cluster.x-k8s.io")
			Expect(crdContent).To(ContainSubstring("ready"),
				"EvrocCluster status must contain ready field (required by CAPI InfrastructureCluster contract)")
		})

		It("should support FailureDomains in status", func() {
			By("Checking failureDomains field is present in status")
			// FailureDomains enables multi-AZ cluster spreading
			crdContent := readCRD("evrocclusters.infrastructure.cluster.x-k8s.io")
			Expect(crdContent).To(ContainSubstring("failureDomains"),
				"EvrocCluster status must contain failureDomains (required for multi-AZ support)")
		})

		It("should have role-based security group structure in spec", func() {
			crdContent := readCRD("evrocclusters.infrastructure.cluster.x-k8s.io")

			By("Checking securityGroups field exists at spec level")
			Expect(crdContent).To(ContainSubstring("securityGroups"),
				"EvrocCluster spec must contain securityGroups field")

			By("Checking common section exists")
			Expect(crdContent).To(ContainSubstring("Common security groups applied to ALL nodes"),
				"EvrocCluster securityGroups must have a common section")

			By("Checking controlPlane section exists")
			Expect(crdContent).To(ContainSubstring("controlPlane"),
				"EvrocCluster securityGroups must have a controlPlane section")

			By("Checking worker section exists")
			Expect(crdContent).To(ContainSubstring("worker"),
				"EvrocCluster securityGroups must have a worker section")
		})

		It("should have role field in security group status entries", func() {
			crdContent := readCRD("evrocclusters.infrastructure.cluster.x-k8s.io")

			By("Checking role field exists in ManagedSecurityGroup status")
			Expect(crdContent).To(ContainSubstring("Role indicates which section"),
				"ManagedSecurityGroup status must include a role field")
		})

		It("should belong to the infrastructure.cluster.x-k8s.io API group", func() {
			By("Checking API group in CRD")
			crdContent := readCRD("evrocclusters.infrastructure.cluster.x-k8s.io")
			Expect(crdContent).To(ContainSubstring("infrastructure.cluster.x-k8s.io"),
				"EvrocCluster must be in the infrastructure.cluster.x-k8s.io API group")
		})
	})

	Context("InfrastructureMachine (EvrocMachine)", func() {
		It("should have required spec fields", func() {
			By("Checking providerID field is present")
			// ProviderID is required by the InfrastructureMachine contract:
			// https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-machine
			crdContent := readCRD("evrocmachines.infrastructure.cluster.x-k8s.io")
			Expect(crdContent).To(ContainSubstring("providerID"),
				"EvrocMachine spec must contain providerID (required by CAPI InfrastructureMachine contract)")
		})

		It("should have required status fields", func() {
			crdContent := readCRD("evrocmachines.infrastructure.cluster.x-k8s.io")

			By("Checking ready field is present")
			Expect(crdContent).To(ContainSubstring("ready"),
				"EvrocMachine status must contain ready field (required by CAPI InfrastructureMachine contract)")

			By("Checking addresses field is present")
			Expect(crdContent).To(ContainSubstring("addresses"),
				"EvrocMachine status must contain addresses field (required by CAPI InfrastructureMachine contract)")
		})

		It("should support inline networking configuration", func() {
			crdContent := readCRD("evrocmachines.infrastructure.cluster.x-k8s.io")

			By("Checking networkingConfig field for inline networking support")
			Expect(crdContent).To(ContainSubstring("networkingConfig"),
				"EvrocMachine spec must support networkingConfig for inline configuration pattern")

			By("Checking placementConfig field for inline placement support")
			Expect(crdContent).To(ContainSubstring("placementConfig"),
				"EvrocMachine spec must support placementConfig for inline configuration pattern")
		})
	})

	Context("InfrastructureMachineTemplate (EvrocMachineTemplate)", func() {
		It("should exist as a valid CRD with the EvrocMachineTemplate kind", func() {
			By("Verifying EvrocMachineTemplate CRD file exists and contains the correct kind")
			crdContent := readCRD("evrocmachinetemplates.infrastructure.cluster.x-k8s.io")
			Expect(crdContent).To(ContainSubstring("EvrocMachineTemplate"),
				"CRD must define EvrocMachineTemplate kind (required for MachineDeployments)")
		})
	})

	Context("InfrastructureClusterTemplate (EvrocClusterTemplate)", func() {
		It("should exist as a valid CRD", func() {
			By("Verifying EvrocClusterTemplate CRD file exists")
			// Just check the file exists - readCRD will fail the test if not
			_ = readCRD("evrocclustertemplates.infrastructure.cluster.x-k8s.io")
		})
	})

	Context("CRD Registration", func() {
		It("should have all required CRDs generated", func() {
			By("Verifying all evroc CRDs are present in config/crd/bases/")
			// These CRDs are required for the simplified evroc CAPI provider (v2 inline configuration)
			// We no longer use separate CRDs for PublicIP, SecurityGroup, PlacementGroup, or Disk
			// as those are now inline configurations within EvrocCluster and EvrocMachine
			requiredCRDs := []string{
				"evrocclusters.infrastructure.cluster.x-k8s.io",
				"evrocclustertemplates.infrastructure.cluster.x-k8s.io",
				"evrocmachines.infrastructure.cluster.x-k8s.io",
				"evrocmachinetemplates.infrastructure.cluster.x-k8s.io",
			}

			for _, crdName := range requiredCRDs {
				crdFile := crdFilePath(crdName)
				Expect(crdFile).To(BeAnExistingFile(),
					"CRD %s must be generated at %s - run 'make manifests'", crdName, crdFile)
			}
		})

		It("should have all CRDs expose the v1beta1 API version", func() {
			By("Verifying v1beta1 version in core CRD files")
			coreCRDs := []string{
				"evrocclusters.infrastructure.cluster.x-k8s.io",
				"evrocmachines.infrastructure.cluster.x-k8s.io",
			}

			for _, crdName := range coreCRDs {
				crdContent := readCRD(crdName)
				Expect(crdContent).To(ContainSubstring("v1beta1"),
					"CRD %s must expose v1beta1 version", crdName)
			}
		})
	})
})

// crdFilePath resolves the absolute path to a generated CRD YAML file.
// controller-gen names files as: <group>_<plural>.yaml
// e.g. "evrocclusters.infrastructure.cluster.x-k8s.io" →
//
//	"config/crd/bases/infrastructure.cluster.x-k8s.io_evrocclusters.yaml"
func crdFilePath(crdName string) string {
	// Split "<plural>.<group>" on the first "."
	dotIdx := 0
	for i, c := range crdName {
		if c == '.' {
			dotIdx = i
			break
		}
	}
	plural := crdName[:dotIdx]
	group := crdName[dotIdx+1:]
	fileName := group + "_" + plural + ".yaml"
	return filepath.Join(repoRoot, "config", "crd", "bases", fileName)
}

// readCRD reads a CRD file and returns its content as a string.
// The test fails immediately if the file does not exist or cannot be read.
func readCRD(crdName string) string {
	crdFile := crdFilePath(crdName)
	Expect(crdFile).To(BeAnExistingFile(),
		"CRD file %s must exist - run 'make manifests' to generate it", crdFile)
	content, err := os.ReadFile(crdFile)
	Expect(err).NotTo(HaveOccurred(), "Failed to read CRD file %s", crdFile)
	return string(content)
}
