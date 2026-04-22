#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 evroc

set -euo pipefail

# Test the published Helm chart from GHCR
# This validates that the released artifacts work correctly

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_VERSION="${CHART_VERSION:-latest}"
TEST_NAMESPACE="${TEST_NAMESPACE:-cape-test-published}"
RELEASE_NAME="${RELEASE_NAME:-cape}"
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-cape-test-$(date +%s)}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.2}"

# KUBECONFIG must use absolute path (not relative)
KUBECONFIG_FILE="$(mktemp /tmp/kubeconfig-published-test.XXXXXX)"
export KUBECONFIG="${KUBECONFIG_FILE}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."

    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl not found. Please install kubectl."
        exit 1
    fi

    if ! command -v helm &> /dev/null; then
        log_error "helm not found. Please install Helm 3+."
        exit 1
    fi

    if ! command -v kind &> /dev/null; then
        log_error "kind not found. Please install kind: https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
        exit 1
    fi

    log_info "[OK] All prerequisites satisfied"
}

# Create kind cluster
create_kind_cluster() {
    log_info "Creating kind cluster ${KIND_CLUSTER_NAME}..."

    kind create cluster \
        --name="${KIND_CLUSTER_NAME}" \
        --image=kindest/node:v1.30.0 \
        --wait=5m \
        --quiet

    log_info "[OK] Kind cluster created"
}

# Install cert-manager
install_cert_manager() {
    log_info "Installing cert-manager ${CERT_MANAGER_VERSION}..."

    kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"

    log_info "Waiting for cert-manager to be ready..."
    kubectl wait --for=condition=available --timeout=5m \
        -n cert-manager deployment/cert-manager \
        deployment/cert-manager-cainjector \
        deployment/cert-manager-webhook

    log_info "[OK] cert-manager installed"
}

# Verify credentials file exists
verify_credentials() {
    log_info "Verifying credentials..."

    if [[ ! -f "${SCRIPT_DIR}/credentials.yaml" ]]; then
        log_error "Credentials not found at ${SCRIPT_DIR}/credentials.yaml"
        log_info ""
        log_info "Please create test/e2e/credentials.yaml in SDK format:"
        log_info ""
        log_info "evroc:"
        log_info "  organization: \"your-org-id\""
        log_info "  project: \"your-project-id\""
        log_info ""
        log_info "auth:"
        log_info "  token: \"your-access-token\""
        log_info "  refresh_token: \"your-refresh-token\""
        log_info "  # OR username/password:"
        log_info "  # username: \"user@example.com\""
        log_info "  # password: \"your-password\""
        log_info ""
        log_info "infrastructure:"
        log_info "  region: \"se-sto\""
        log_info ""
        exit 1
    fi

    # Verify required fields are present
    if ! grep -q "project:" "${SCRIPT_DIR}/credentials.yaml"; then
        log_error "credentials.yaml is missing 'project' field"
        exit 1
    fi

    if ! grep -q "region:" "${SCRIPT_DIR}/credentials.yaml"; then
        log_error "credentials.yaml is missing 'region' field"
        exit 1
    fi

    log_info "[OK] Credentials verified"
}

# Create test namespace
create_namespace() {
    log_info "Creating namespace ${TEST_NAMESPACE}..."

    kubectl create namespace "${TEST_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

    log_info "[OK] Namespace ready"
}

# Create config secret
create_config_secret() {
    log_info "Creating evroc credentials secret..."

    # Create secret directly from credentials.yaml (already in SDK format)
    kubectl create secret generic evroc-credentials \
        --from-file=config.yaml="${SCRIPT_DIR}/credentials.yaml" \
        --namespace="${TEST_NAMESPACE}" \
        --dry-run=client -o yaml | kubectl apply -f -

    log_info "[OK] Secret created"
}

# Build local image
build_local_image() {
    log_info "Building local controller image..."

    local chart_app_version
    chart_app_version=$(grep '^appVersion:' "${SCRIPT_DIR}/../../helm/cluster-api-provider-evroc/Chart.yaml" | awk '{print $2}' | tr -d '"')

    local image_name="ghcr.io/evroc-oss/cluster-api-provider-evroc:v${chart_app_version}"

    # Check for GitHub token for private SDK access
    if [[ -z "${GITHUB_TOKEN:-}" ]]; then
        log_error "GITHUB_TOKEN environment variable required for building (private SDK dependency)"
        log_info "Set it with: export GITHUB_TOKEN=<your-token>"
        exit 1
    fi

    # Build from parent directory context with GitHub token secret
    DOCKER_BUILDKIT=1 docker build \
        -f "${SCRIPT_DIR}/../../Dockerfile" \
        -t "${image_name}" \
        --build-arg PROVIDER_DIR=. \
        --secret id=github_token,env=GITHUB_TOKEN \
        "${SCRIPT_DIR}/../.."

    log_info "Loading image into kind cluster..."
    kind load docker-image "${image_name}" --name="${KIND_CLUSTER_NAME}"

    log_info "[OK] Local image built and loaded: ${image_name}"
}

# Install Helm chart (published or local)
install_chart() {
    if [[ "${USE_LOCAL_CHART:-false}" == "true" ]]; then
        log_info "Installing local Helm chart from ${SCRIPT_DIR}/../../helm/cluster-api-provider-evroc..."

        # Extract appVersion from Chart.yaml for local testing
        local chart_app_version
        chart_app_version=$(grep '^appVersion:' "${SCRIPT_DIR}/../../helm/cluster-api-provider-evroc/Chart.yaml" | awk '{print $2}' | tr -d '"')

        helm upgrade --install "${RELEASE_NAME}" \
            "${SCRIPT_DIR}/../../helm/cluster-api-provider-evroc" \
            --namespace="${TEST_NAMESPACE}" \
            --create-namespace \
            --set evroc.existingConfigSecret=evroc-credentials \
            --set controller.image.tag="v${chart_app_version}" \
            --set controller.image.pullPolicy=IfNotPresent \
            --wait \
            --timeout=5m
    else
        log_info "Installing published Helm chart v${CHART_VERSION} from GHCR..."

        helm upgrade --install "${RELEASE_NAME}" \
            oci://ghcr.io/evroc-oss/charts/cluster-api-provider-evroc \
            --version="${CHART_VERSION}" \
            --namespace="${TEST_NAMESPACE}" \
            --create-namespace \
            --set evroc.existingConfigSecret=evroc-credentials \
            --set controller.image.pullPolicy=Always \
            --wait \
            --timeout=5m
    fi

    log_info "[OK] Chart installed"
}

# Wait for deployment to be ready
wait_for_deployment() {
    log_info "Waiting for controller deployment to be ready..."

    kubectl wait --for=condition=available \
        --timeout=5m \
        -l control-plane=controller-manager \
        -n "${TEST_NAMESPACE}" \
        deployment

    log_info "[OK] Deployment ready"
}

# Verify CRDs are installed
verify_crds() {
    log_info "Verifying CRDs are installed..."

    local crds=(
        "evrocclusters.infrastructure.cluster.x-k8s.io"
        "evrocmachines.infrastructure.cluster.x-k8s.io"
        "evrocclustertemplates.infrastructure.cluster.x-k8s.io"
        "evrocmachinetemplates.infrastructure.cluster.x-k8s.io"
    )

    for crd in "${crds[@]}"; do
        if ! kubectl get crd "${crd}" &> /dev/null; then
            log_error "CRD ${crd} not found"
            return 1
        fi
        log_info "  [OK] ${crd}"
    done

    log_info "[OK] All CRDs present"
}

# Check controller logs for errors
check_controller_logs() {
    log_info "Checking controller logs for errors..."

    local pod_name
    pod_name=$(kubectl get pods -n "${TEST_NAMESPACE}" \
        -l control-plane=controller-manager \
        -o jsonpath='{.items[0].metadata.name}')

    if [[ -z "${pod_name}" ]]; then
        log_error "Controller pod not found"
        return 1
    fi

    log_info "Controller pod: ${pod_name}"

    # Check for common error patterns
    local errors
    errors=$(kubectl logs -n "${TEST_NAMESPACE}" "${pod_name}" --tail=100 | grep -i "error\|fatal\|panic" || true)

    if [[ -n "${errors}" ]]; then
        log_warn "Found potential errors in logs:"
        echo "${errors}"
    else
        log_info "[OK] No errors in recent logs"
    fi
}

# Test creating a minimal EvrocCluster
test_cluster_creation() {
    log_info "Testing EvrocCluster creation..."

    # Extract project and region from credentials.yaml
    local project region
    project=$(grep "project:" "${SCRIPT_DIR}/credentials.yaml" | sed 's/.*project: *"\?\([^"]*\)"\?.*/\1/')
    region=$(grep "region:" "${SCRIPT_DIR}/credentials.yaml" | sed 's/.*region: *"\?\([^"]*\)"\?.*/\1/')

    cat <<EOF | kubectl apply -f -
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocCluster
metadata:
  name: test-cluster-published
  namespace: ${TEST_NAMESPACE}
spec:
  project: "${project}"
  region: "${region}"
  failureDomains:
    - a
    - b
EOF

    log_info "Waiting for EvrocCluster to be created..."
    sleep 5

    # Check if cluster was created
    if kubectl get evroccluster test-cluster-published -n "${TEST_NAMESPACE}" &> /dev/null; then
        log_info "[OK] EvrocCluster created successfully"

        # Show status
        kubectl get evroccluster test-cluster-published -n "${TEST_NAMESPACE}" -o yaml | grep -A 10 "status:"
    else
        log_error "Failed to create EvrocCluster"
        return 1
    fi
}

# Cleanup
cleanup() {
    log_info "Cleaning up test resources..."

    # Delete test cluster if it exists
    kubectl delete evroccluster test-cluster-published -n "${TEST_NAMESPACE}" --ignore-not-found=true --timeout=2m 2>/dev/null || true

    # Uninstall Helm release
    helm uninstall "${RELEASE_NAME}" -n "${TEST_NAMESPACE}" --wait 2>/dev/null || true

    # Delete namespace
    kubectl delete namespace "${TEST_NAMESPACE}" --timeout=2m 2>/dev/null || true

    # Delete kind cluster
    log_info "Deleting kind cluster ${KIND_CLUSTER_NAME}..."
    kind delete cluster --name="${KIND_CLUSTER_NAME}" 2>/dev/null || true

    # Remove temporary kubeconfig
    if [[ -f "${KUBECONFIG_FILE}" ]]; then
        rm -f "${KUBECONFIG_FILE}"
    fi

    log_info "[OK] Cleanup complete"
}

# Main test flow
main() {
    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --local)
                USE_LOCAL_CHART=true
                shift
                ;;
            *)
                log_error "Unknown argument: $1"
                log_info "Usage: $0 [--local]"
                exit 1
                ;;
        esac
    done

    if [[ "${USE_LOCAL_CHART:-false}" == "true" ]]; then
        log_info "========================================="
        log_info "Testing Local Helm Chart"
        log_info "========================================="
    else
        log_info "========================================="
        log_info "Testing Published Helm Chart v${CHART_VERSION}"
        log_info "========================================="
    fi
    echo

    # Trap cleanup on exit
    trap cleanup EXIT

    check_prerequisites
    verify_credentials
    create_kind_cluster
    install_cert_manager

    # Build and load local image if using local chart
    if [[ "${USE_LOCAL_CHART:-false}" == "true" ]]; then
        build_local_image
    fi

    create_namespace
    create_config_secret
    install_chart
    wait_for_deployment
    verify_crds
    check_controller_logs
    test_cluster_creation

    echo
    log_info "========================================="
    log_info "[OK] All tests passed!"
    log_info "========================================="
    log_info ""
    if [[ "${USE_LOCAL_CHART:-false}" == "true" ]]; then
        log_info "Local chart is working correctly."
    else
        log_info "Published chart v${CHART_VERSION} is working correctly."
    fi
    log_info ""
    log_info "To manually inspect:"
    log_info "  kubectl get all -n ${TEST_NAMESPACE}"
    log_info "  kubectl logs -n ${TEST_NAMESPACE} -l control-plane=controller-manager --tail=50"
    log_info ""
}

# Run if executed directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
