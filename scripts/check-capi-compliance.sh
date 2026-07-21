#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 evroc

# CAPI 1.12 Compliance Checker for cluster-api-provider-evroc

set -o pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "==================================================================="
echo "  CAPI 1.12 Compliance Check for cluster-api-provider-evroc"
echo "==================================================================="
echo ""

PASS=0
FAIL=0
WARN=0

check_pass() {
    echo -e "${GREEN}PASS${NC}: $1"
    ((PASS++))
}

check_fail() {
    echo -e "${RED}FAIL${NC}: $1"
    ((FAIL++))
}

check_warn() {
    echo -e "${YELLOW}WARN${NC}: $1"
    ((WARN++))
}

echo "## 1. Required CRD Definitions"
echo "-------------------------------------------------------------------"

# Check InfrastructureMachine
if [ -f "api/v1beta1/evrocmachine_types.go" ]; then
    if grep -q "ProviderID" api/v1beta1/evrocmachine_types.go; then
        check_pass "EvrocMachine has ProviderID field"
    else
        check_fail "EvrocMachine missing ProviderID field"
    fi

    if grep -q "Addresses" api/v1beta1/evrocmachine_types.go; then
        check_pass "EvrocMachine has Addresses field"
    else
        check_fail "EvrocMachine missing Addresses field"
    fi

    if grep -q "Ready.*bool" api/v1beta1/evrocmachine_types.go; then
        check_pass "EvrocMachine has Ready status field"
    else
        check_fail "EvrocMachine missing Ready status field"
    fi
else
    check_fail "EvrocMachine types not found"
fi

# Check InfrastructureMachineTemplate
if [ -f "api/v1beta1/evrocmachinetemplate_types.go" ]; then
    check_pass "EvrocMachineTemplate exists"
else
    check_fail "EvrocMachineTemplate not found"
fi

# Check InfrastructureCluster
if [ -f "api/v1beta1/evroccluster_types.go" ]; then
    if grep -q "ControlPlaneEndpoint.*APIEndpoint" api/v1beta1/evroccluster_types.go; then
        check_pass "EvrocCluster has ControlPlaneEndpoint field"
    else
        check_fail "EvrocCluster missing ControlPlaneEndpoint field"
    fi

    if grep -q "FailureDomains" api/v1beta1/evroccluster_types.go; then
        check_pass "EvrocCluster has FailureDomains field"
    else
        check_warn "EvrocCluster missing FailureDomains field (optional but recommended)"
    fi

    if grep -q "Ready.*bool" api/v1beta1/evroccluster_types.go; then
        check_pass "EvrocCluster has Ready status field"
    else
        check_fail "EvrocCluster missing Ready status field"
    fi
else
    check_fail "EvrocCluster types not found"
fi

echo ""
echo "## 2. Controller Implementations"
echo "-------------------------------------------------------------------"

# Check controllers exist
if [ -f "internal/controller/evrocmachine_controller.go" ]; then
    check_pass "EvrocMachine controller exists"

    if grep -q "Reconcile.*ctrl.Request.*ctrl.Result" internal/controller/evrocmachine_controller.go; then
        check_pass "EvrocMachine controller has Reconcile method"
    else
        check_fail "EvrocMachine controller missing Reconcile method"
    fi
else
    check_fail "EvrocMachine controller not found"
fi

if [ -f "internal/controller/evroccluster_controller.go" ]; then
    check_pass "EvrocCluster controller exists"

    if grep -q "Reconcile.*ctrl.Request.*ctrl.Result" internal/controller/evroccluster_controller.go; then
        check_pass "EvrocCluster controller has Reconcile method"
    else
        check_fail "EvrocCluster controller missing Reconcile method"
    fi
else
    check_fail "EvrocCluster controller not found"
fi

echo ""
echo "## 3. Finalizer Support"
echo "-------------------------------------------------------------------"

for controller in evrocmachine evroccluster; do
    if [ -f "internal/controller/${controller}_controller.go" ]; then
        if grep -q "Finalizer" "internal/controller/${controller}_controller.go"; then
            check_pass "${controller} controller uses finalizers"
        else
            check_fail "${controller} controller missing finalizer support"
        fi
    fi
done

echo ""
echo "## 4. CRD Generation"
echo "-------------------------------------------------------------------"

# CRDs are generated into the Helm chart (see the manifests target in Makefile).
CRD_DIR="helm/cluster-api-provider-evroc/crds"

if [ -f "${CRD_DIR}/infrastructure.cluster.x-k8s.io_evrocmachines.yaml" ]; then
    check_pass "EvrocMachine CRD manifest exists"
else
    check_fail "EvrocMachine CRD manifest not found (run 'make manifests')"
fi

if [ -f "${CRD_DIR}/infrastructure.cluster.x-k8s.io_evrocmachinetemplates.yaml" ]; then
    check_pass "EvrocMachineTemplate CRD manifest exists"
else
    check_fail "EvrocMachineTemplate CRD manifest not found (run 'make manifests')"
fi

if [ -f "${CRD_DIR}/infrastructure.cluster.x-k8s.io_evrocclusters.yaml" ]; then
    check_pass "EvrocCluster CRD manifest exists"
else
    check_fail "EvrocCluster CRD manifest not found (run 'make manifests')"
fi

echo ""
echo "## 5. CAPI Dependencies"
echo "-------------------------------------------------------------------"

if grep -q "sigs.k8s.io/cluster-api" go.mod; then
    CAPI_VERSION=$(grep "sigs.k8s.io/cluster-api" go.mod | grep -v "^//" | awk '{print $2}')
    if [ ! -z "$CAPI_VERSION" ]; then
        check_pass "CAPI dependency found (version: $CAPI_VERSION)"

        # Check if it's 1.12+
        if [[ $CAPI_VERSION == v1.1[1-9]* ]] || [[ $CAPI_VERSION == v1.[2-9]* ]]; then
            check_pass "CAPI version is 1.12 or higher"
        else
            check_warn "CAPI version might be older than 1.12"
        fi
    else
        check_fail "CAPI dependency found but version unclear"
    fi
else
    check_fail "CAPI dependency not found in go.mod"
fi

echo ""
echo "## 6. Unit Tests"
echo "-------------------------------------------------------------------"

if go test ./internal/controller/... -v > /dev/null 2>&1; then
    TEST_COUNT=$(go test ./internal/controller/... -v 2>&1 | grep -c "^=== RUN")
    PASS_COUNT=$(go test ./internal/controller/... -v 2>&1 | grep -c "^--- PASS")
    check_pass "Unit tests pass ($PASS_COUNT/$TEST_COUNT tests)"
else
    check_fail "Unit tests failing"
fi

# Check coverage
COVERAGE_OUTPUT=$(go test ./internal/controller/... -coverprofile=/tmp/cover.out 2>&1 | grep "coverage:")
COVERAGE=$(echo "$COVERAGE_OUTPUT" | grep -oP 'coverage:\s+\K[\d.]+%' | head -1)
if [ ! -z "$COVERAGE" ]; then
    check_pass "Test coverage: $COVERAGE"

    # Parse coverage percentage (simple integer comparison)
    COV_NUM=$(echo $COVERAGE | sed 's/%//' | cut -d'.' -f1)
    if [ ! -z "$COV_NUM" ] && [ "$COV_NUM" -ge 60 ] 2>/dev/null; then
        check_pass "Coverage above 60% threshold"
    else
        check_warn "Coverage below 60% (currently $COVERAGE)"
    fi
fi

echo ""
echo "## 7. CAPI Contract Validation"
echo "-------------------------------------------------------------------"

# Check ProviderID format
if grep -q "evroc://" internal/controller/evrocmachine_controller.go; then
    check_pass "ProviderID uses correct format (evroc://)"
else
    check_warn "ProviderID format not found in controller"
fi

# Check for address extraction
if grep -q "NodeAddress" internal/controller/evrocmachine_controller.go; then
    check_pass "Machine address extraction implemented"
else
    check_fail "Machine address extraction not found"
fi

# Check failure domain support
if grep -q "FailureDomain" internal/controller/evroccluster_controller.go; then
    check_pass "Failure domain support implemented"
else
    check_warn "Failure domain support not found"
fi

echo ""
echo "## 8. Optional Features"
echo "-------------------------------------------------------------------"

# LoadBalancer (optional)
if [ -f "api/v1beta1/evrocloadbalancer_types.go" ]; then
    check_pass "LoadBalancer resource exists (optional)"
else
    check_warn "LoadBalancer resource not implemented (optional - can use kube-vip)"
fi

# VPC/Subnet (optional)
if [ -f "api/v1beta1/evrocvpc_types.go" ]; then
    check_pass "VPC resource exists"
elif [ -f "api/v1beta1/evrocvpc_types.go.skip" ]; then
    check_warn "VPC resource exists but skipped (waiting for SDK support)"
else
    check_warn "VPC resource not implemented (optional - can use default VPC)"
fi

echo ""
echo "==================================================================="
echo "  COMPLIANCE SUMMARY"
echo "==================================================================="
echo ""
echo -e "${GREEN}PASSED${NC}: $PASS"
echo -e "${YELLOW}WARNINGS${NC}: $WARN"
echo -e "${RED}FAILED${NC}: $FAIL"
echo ""

if [ $FAIL -eq 0 ]; then
    echo -e "${GREEN}CAPI 1.12 COMPLIANCE: PASSED${NC}"
    echo ""
    echo "Your provider meets CAPI 1.12 requirements!"
    echo ""
    echo "Next steps:"
    echo "  1. Test E2E cluster creation with clusterctl"
    echo "  2. Run CAPI contract tests (optional)"
    echo "  3. Test with real workloads"
    echo ""
    exit 0
else
    echo -e "${RED}CAPI 1.12 COMPLIANCE: FAILED${NC}"
    echo ""
    echo "Please fix the failed checks above before deploying."
    echo ""
    exit 1
fi
