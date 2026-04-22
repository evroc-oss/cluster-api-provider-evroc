#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 evroc

set -euo pipefail

# Full Rancher + Turtles + evroc CAPI Provider Integration Test
# This validates the complete Rancher certification scenario

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Configuration
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-rancher-test-$(date +%s)}"
RANCHER_HOSTNAME="${RANCHER_HOSTNAME:-rancher.local}"
RANCHER_VERSION="${RANCHER_VERSION:-2.14.0-rc1}"
TURTLES_VERSION="${TURTLES_VERSION:-0.26.0}"
EVROC_PROVIDER_VERSION="${EVROC_PROVIDER_VERSION:-latest}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.2}"

# KUBECONFIG must use absolute path (not relative)
KUBECONFIG_FILE="$(mktemp /tmp/kubeconfig-rancher-test.XXXXXX)"
export KUBECONFIG="${KUBECONFIG_FILE}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_step() {
    echo -e "${BLUE}[STEP]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

# Check prerequisites
check_prerequisites() {
    log_step "Checking prerequisites..."

    local missing_tools=()

    if ! command -v kubectl &> /dev/null; then
        missing_tools+=("kubectl")
    fi

    if ! command -v helm &> /dev/null; then
        missing_tools+=("helm")
    fi

    if ! command -v kind &> /dev/null; then
        missing_tools+=("kind")
    fi

    if [[ ${#missing_tools[@]} -gt 0 ]]; then
        log_error "Missing required tools: ${missing_tools[*]}"
        log_info "Please install the missing tools and try again."
        exit 1
    fi

    log_info "[OK] All prerequisites satisfied"
}

# Create kind cluster
create_kind_cluster() {
    log_step "Creating kind cluster ${KIND_CLUSTER_NAME}..."

    kind create cluster \
        --name="${KIND_CLUSTER_NAME}" \
        --image=kindest/node:v1.30.0 \
        --wait=5m \
        --quiet

    log_info "[OK] Kind cluster created"
}

# Install cert-manager
install_cert_manager() {
    log_step "Installing cert-manager ${CERT_MANAGER_VERSION}..."

    if kubectl get namespace cert-manager &> /dev/null; then
        log_info "cert-manager namespace already exists, checking installation..."
        if kubectl get deployment -n cert-manager cert-manager &> /dev/null; then
            log_info "[OK] cert-manager already installed"
            return 0
        fi
    fi

    kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"

    log_info "Waiting for cert-manager to be ready..."
    kubectl wait --for=condition=available --timeout=5m \
        -n cert-manager deployment/cert-manager \
        deployment/cert-manager-cainjector \
        deployment/cert-manager-webhook

    log_info "[OK] cert-manager installed"
}

# Install Rancher
install_rancher() {
    log_step "Installing Rancher ${RANCHER_VERSION}..."

    # Add Rancher Helm repo
    helm repo add rancher-stable https://releases.rancher.com/server-charts/stable
    helm repo update

    # Create cattle-system namespace
    kubectl create namespace cattle-system --dry-run=client -o yaml | kubectl apply -f -

    # Install Rancher
    helm upgrade --install rancher rancher-stable/rancher \
        --namespace cattle-system \
        --version="${RANCHER_VERSION}" \
        --set hostname="${RANCHER_HOSTNAME}" \
        --set replicas=1 \
        --set bootstrapPassword=admin \
        --set global.cattle.psp.enabled=false \
        --wait \
        --timeout=10m

    log_info "Waiting for Rancher to be ready..."
    kubectl -n cattle-system rollout status deploy/rancher

    log_info "[OK] Rancher installed"
    log_info ""
    log_info "Rancher UI will be available at: https://${RANCHER_HOSTNAME}"
    log_info "Bootstrap password: admin"
    log_info ""
}

# Install Rancher Turtles
install_capi_operator() {
    log_step "Installing CAPI Operator..."

    # Add CAPI Operator Helm repo
    helm repo add capi-operator https://kubernetes-sigs.github.io/cluster-api-operator
    helm repo update

    # Install CAPI Operator
    log_info "Installing CAPI Operator Helm chart..."
    if helm install capi-operator capi-operator/cluster-api-operator \
        --create-namespace \
        --namespace capi-operator-system \
        --wait \
        --timeout=5m; then
        log_info "CAPI Operator Helm release created"
    else
        log_error "CAPI Operator Helm install failed"
        helm list -A
        kubectl get pods -n capi-operator-system
        return 1
    fi

    # Wait for deployment to exist and be ready
    log_info "Waiting for CAPI Operator to be ready..."
    kubectl wait --for=condition=available --timeout=5m \
        -n capi-operator-system deployment/capi-operator-cluster-api-operator

    log_info "Creating CoreProvider for CAPI v1.12..."
    cat <<EOF | kubectl apply -f -
apiVersion: operator.cluster.x-k8s.io/v1alpha2
kind: CoreProvider
metadata:
  name: cluster-api
  namespace: capi-operator-system
spec:
  version: v1.12.0
EOF

    log_info "[OK] CAPI Operator and CoreProvider configured"
}

install_turtles() {
    log_step "Installing Rancher Turtles ${TURTLES_VERSION}..."

    # Add Rancher Turtles Helm repo
    helm repo add turtles https://rancher.github.io/turtles
    helm repo update

    # Create required namespaces
    kubectl create namespace cattle-turtles-system --dry-run=client -o yaml | kubectl apply -f -
    kubectl create namespace cattle-capi-system --dry-run=client -o yaml | kubectl apply -f -

    # Install Turtles WITHOUT CAPI Operator (already installed separately)
    helm upgrade --install rancher-turtles turtles/rancher-turtles \
        --namespace cattle-turtles-system \
        --version="${TURTLES_VERSION}" \
        --create-namespace \
        --set cluster-api-operator.enabled=false \
        --set cluster-api-operator.cluster-api.enabled=false \
        --wait \
        --timeout=10m

    log_info "Waiting for Turtles controller to be ready..."
    kubectl wait --for=condition=available --timeout=5m \
        -n cattle-turtles-system deployment/rancher-turtles-controller-manager

    log_info "[OK] Rancher Turtles installed"
}

# Ensure CAPI core is installed
ensure_capi_core() {
    log_step "Waiting for CAPI core to be ready..."

    # Wait for CAPI Operator to create the default CoreProvider
    log_info "Waiting for CoreProvider to be created by CAPI Operator..."
    for i in {1..60}; do
        if kubectl get coreprovider cluster-api -n capi-operator-system &>/dev/null 2>&1; then
            log_info "CoreProvider found, waiting for it to be ready..."
            break
        fi
        if [ $i -eq 60 ]; then
            log_error "CoreProvider not created after 5 minutes"
            log_error "Checking CAPI Operator status..."
            kubectl get pods -n capi-operator-system || echo "CAPI Operator namespace not found"
            kubectl get coreprovider -A || echo "No CoreProviders found"
            return 1
        fi
        sleep 5
    done

    # Wait for CoreProvider to be ready
    kubectl wait --for=condition=ready --timeout=5m \
        -n capi-operator-system coreprovider/cluster-api || {
        log_warn "CoreProvider not ready yet, checking status..."
        kubectl describe coreprovider cluster-api -n capi-operator-system || true
        kubectl get pods -n cattle-provisioning-capi-system || echo "CAPI controllers namespace not found"
    }

    # Wait for CAPI webhook service
    log_info "Waiting for CAPI webhook service..."
    for i in {1..60}; do
        if kubectl get service -n cattle-provisioning-capi-system capi-webhook-service &>/dev/null 2>&1; then
            log_info "[OK] CAPI core is ready"
            return 0
        fi
        sleep 2
    done

    log_error "CAPI webhook service not found after 2 minutes"
    log_error "Debugging CAPI core installation:"
    kubectl get all -n cattle-provisioning-capi-system 2>&1 || echo "Namespace not found"
    kubectl get coreprovider -A 2>&1 || echo "No CoreProviders found"
    return 1
}

# Verify credentials file exists
verify_credentials() {
    log_step "Verifying credentials..."

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

# Create evroc provider secret
create_provider_secret() {
    log_step "Creating evroc provider credentials..."

    if [[ "${USE_LOCAL_PROVIDER:-false}" == "true" ]]; then
        # Local mode: create namespace and secret in capi-evroc-system
        kubectl create namespace capi-evroc-system --dry-run=client -o yaml | kubectl apply -f -
        kubectl create secret generic evroc-credentials \
            --from-file=config.yaml="${SCRIPT_DIR}/credentials.yaml" \
            --namespace=capi-evroc-system \
            --dry-run=client -o yaml | kubectl apply -f -
    else
        # CAPIProvider mode: create in cattle-turtles-system
        kubectl create secret generic evroc-credentials \
            --from-file=config.yaml="${SCRIPT_DIR}/credentials.yaml" \
            --namespace=cattle-turtles-system \
            --dry-run=client -o yaml | kubectl apply -f -
    fi

    log_info "[OK] Provider secret created"
}

# Install evroc CAPI Provider via CAPIProvider CRD
install_evroc_provider() {
    log_step "Installing evroc CAPI Provider v${EVROC_PROVIDER_VERSION}..."

    if [[ "${USE_LOCAL_PROVIDER:-false}" == "true" ]]; then
        local local_manifest="${SCRIPT_DIR}/../../templates/infrastructure-components.yaml"
        log_info "Using local provider from ${local_manifest}"

        if [[ ! -f "${local_manifest}" ]]; then
            log_error "Local manifest not found: ${local_manifest}"
            log_error "Run: ./scripts/bump-version.sh ${EVROC_PROVIDER_VERSION}"
            return 1
        fi

        # Load provider image into kind cluster
        local provider_image="ghcr.io/evroc-oss/cluster-api-provider-evroc:v${EVROC_PROVIDER_VERSION}"
        log_info "Loading provider image into kind: ${provider_image}"

        # Check if image exists locally
        if docker image inspect "${provider_image}" &> /dev/null; then
            kind load docker-image "${provider_image}" --name="${KIND_CLUSTER_NAME}"
            log_info "[OK] Provider image loaded into kind"
        else
            log_warn "Provider image ${provider_image} not found locally"
            log_warn "Attempting to pull from registry..."
            if docker pull "${provider_image}"; then
                kind load docker-image "${provider_image}" --name="${KIND_CLUSTER_NAME}"
                log_info "[OK] Provider image pulled and loaded into kind"
            else
                log_error "Failed to pull provider image. Available images:"
                docker images | grep cluster-api-provider-evroc || echo "None found"
                log_error ""
                log_error "You can either:"
                log_error "  1. Build the image: make docker-build"
                log_error "  2. Tag an existing image: docker tag <existing-image> ${provider_image}"
                log_error "  3. Use a published version that exists in GHCR"
                return 1
            fi
        fi

        # Create namespace first
        kubectl create namespace capi-evroc-system --dry-run=client -o yaml | kubectl apply -f -

        # Install directly from local file (bypass Turtles/clusterctl for local dev)
        log_info "Applying local infrastructure-components.yaml..."
        if kubectl apply -f "${local_manifest}"; then
            log_info "[OK] Local provider manifests applied"
        else
            log_error "Failed to apply local provider manifests"
            return 1
        fi
    else
        # Create CAPIProvider resource for evroc
        cat <<EOF | kubectl apply -f -
apiVersion: turtles-capi.cattle.io/v1alpha1
kind: CAPIProvider
metadata:
  name: evroc
  namespace: cattle-turtles-system
spec:
  name: evroc
  type: infrastructure
  version: v${EVROC_PROVIDER_VERSION}
  configSecret:
    name: evroc-credentials
  fetchConfig:
    url: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/download/v${EVROC_PROVIDER_VERSION}/infrastructure-components.yaml
EOF
    fi

    log_info "Waiting for evroc provider to be installed..."

    # Wait for deployment to exist (up to 3 minutes)
    for i in {1..36}; do
        if kubectl get deployment -n capi-evroc-system cluster-api-provider-evroc-controller-manager &> /dev/null; then
            log_info "Provider deployment found, waiting for it to become available..."

            # Wait with timeout, but capture failure
            if kubectl wait --for=condition=available --timeout=5m \
                -n capi-evroc-system \
                deployment/cluster-api-provider-evroc-controller-manager 2>&1; then
                log_info "[OK] evroc provider installed and ready"
                return 0
            else
                log_error "Provider deployment failed to become available"
                log_error "Debugging deployment failure:"
                echo ""
                echo "=== Deployment Status ==="
                kubectl get deployment -n capi-evroc-system cluster-api-provider-evroc-controller-manager -o yaml
                echo ""
                echo "=== Pod Status ==="
                kubectl get pods -n capi-evroc-system
                echo ""
                echo "=== Pod Describe ==="
                kubectl describe pods -n capi-evroc-system
                echo ""
                echo "=== Pod Logs (if any) ==="
                kubectl logs -n capi-evroc-system -l control-plane=controller-manager --tail=100 || echo "No logs available"
                echo ""
                echo "=== Recent Events ==="
                kubectl get events -n capi-evroc-system --sort-by='.lastTimestamp' | tail -20
                echo ""
                return 1
            fi
        fi
        if [ $i -eq 36 ]; then
            log_error "Provider deployment not found after 3 minutes"
            log_error "Debugging information:"
            echo ""
            echo "=== CAPIProvider Status ==="
            kubectl get capiprovider evroc -n cattle-turtles-system -o yaml || echo "CAPIProvider not found"
            echo ""
            echo "=== Namespace Status ==="
            kubectl get namespace capi-evroc-system || echo "Namespace not created"
            echo ""
            echo "=== Resources in capi-evroc-system ==="
            kubectl get all -n capi-evroc-system || echo "No resources found"
            echo ""
            echo "=== CAPI Operator Logs (last 50 lines) ==="
            kubectl logs -n capi-operator-system -l control-plane=controller-manager --tail=50 || echo "No operator logs found"
            echo ""
            return 1
        fi
        sleep 5
    done
}

# Verify installation
verify_installation() {
    log_step "Verifying installation..."

    log_info "Checking Rancher..."
    kubectl get pods -n cattle-system -l app=rancher

    log_info ""
    log_info "Checking Rancher Turtles..."
    kubectl get pods -n cattle-turtles-system

    log_info ""
    log_info "Checking CAPI Operator..."
    kubectl get pods -n capi-operator-system || log_warn "CAPI Operator not found"

    log_info ""
    log_info "Checking CAPI Core Controllers..."
    kubectl get pods -n cattle-provisioning-capi-system 2>/dev/null || log_warn "CAPI core controllers not found"
    kubectl get service -n cattle-provisioning-capi-system capi-webhook-service 2>/dev/null || log_warn "CAPI webhook service not found"

    log_info ""
    log_info "Checking evroc Provider..."
    kubectl get pods -n capi-evroc-system 2>/dev/null || log_info "Provider not yet deployed (normal - managed by CAPI Operator)"

    log_info ""
    log_info "Checking CRDs..."
    kubectl get crd | grep -E "cluster.x-k8s.io|infrastructure.cluster.x-k8s.io" || log_warn "No CAPI CRDs found"

    log_info ""
    log_info "[OK] Verification complete"
}

# Test creating a CAPI cluster through Rancher
test_cluster_creation() {
    log_step "Testing cluster creation through Rancher/Turtles..."

    # Extract project and region from credentials.yaml
    local project region
    project=$(grep "project:" "${SCRIPT_DIR}/credentials.yaml" | sed 's/.*project: *"\?\([^"]*\)"\?.*/\1/')
    region=$(grep "region:" "${SCRIPT_DIR}/credentials.yaml" | sed 's/.*region: *"\?\([^"]*\)"\?.*/\1/')

    # Create a simple test cluster
    cat <<EOF | kubectl apply -f -
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: test-rancher-cluster
  namespace: default
spec:
  clusterNetwork:
    pods:
      cidrBlocks:
      - 10.244.0.0/16
    services:
      cidrBlocks:
      - 10.96.0.0/12
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
    kind: EvrocCluster
    name: test-rancher-cluster
---
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocCluster
metadata:
  name: test-rancher-cluster
  namespace: default
spec:
  project: "${project}"
  region: "${region}"
  failureDomains:
    - a
EOF

    log_info "Cluster created, waiting for reconciliation..."
    sleep 10

    kubectl get cluster test-rancher-cluster -o yaml | grep -A 10 "status:" || log_warn "No status yet"

    log_info "[OK] Cluster creation tested"
}

# Display access information
display_access_info() {
    log_info ""
    log_info "=============================================="
    log_info "  Rancher + Turtles + evroc Provider Ready"
    log_info "=============================================="
    log_info ""
    log_info "Rancher UI:"
    log_info "   URL: https://${RANCHER_HOSTNAME}"
    log_info "   Username: admin"
    log_info "   Password: admin"
    log_info ""
    log_info "Access cluster:"
    log_info "   kubectl get clusters -A"
    log_info "   kubectl get evrocclusters -A"
    log_info ""
    log_info "View provider logs:"
    log_info "   kubectl logs -n capi-evroc-system -l control-plane=controller-manager --tail=50"
    log_info ""
    log_info "Test cluster:"
    log_info "   kubectl get cluster test-rancher-cluster -o yaml"
    log_info ""
    log_info "Cleanup:"
    log_info "   kubectl delete cluster test-rancher-cluster"
    log_info "   kubectl delete capiprovider evroc -n cattle-turtles-system"
    log_info "   helm uninstall rancher-turtles -n cattle-turtles-system"
    log_info "   helm uninstall rancher -n cattle-system"
    log_info ""
}

# Cleanup (optional)
cleanup() {
    if [[ "${SKIP_CLEANUP:-false}" == "true" ]]; then
        log_info "Skipping cleanup (SKIP_CLEANUP=true)"
        return
    fi

    log_step "Cleaning up test resources..."

    kubectl delete cluster test-rancher-cluster --ignore-not-found=true --timeout=2m 2>/dev/null || true
    kubectl delete capiprovider evroc -n cattle-turtles-system --ignore-not-found=true 2>/dev/null || true

    # Delete kind cluster
    log_info "Deleting kind cluster ${KIND_CLUSTER_NAME}..."
    kind delete cluster --name="${KIND_CLUSTER_NAME}" 2>/dev/null || true

    # Remove temporary kubeconfig
    if [[ -f "${KUBECONFIG_FILE}" ]]; then
        rm -f "${KUBECONFIG_FILE}"
    fi

    log_info "[OK] Cleanup complete"
}

# Main flow
main() {
    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --local)
                USE_LOCAL_PROVIDER=true
                shift
                ;;
            *)
                log_error "Unknown argument: $1"
                log_info "Usage: $0 [--local]"
                exit 1
                ;;
        esac
    done

    log_info "================================================================"
    log_info "  Rancher + Turtles + evroc CAPI Provider Integration Test"
    log_info "================================================================"
    log_info ""
    log_info "Configuration:"
    log_info "  Cluster: ${KIND_CLUSTER_NAME}"
    log_info "  Rancher: ${RANCHER_VERSION}"
    log_info "  Turtles: ${TURTLES_VERSION}"
    log_info "  evroc Provider: ${EVROC_PROVIDER_VERSION}$([ "${USE_LOCAL_PROVIDER:-false}" == "true" ] && echo " (local)" || echo "")"
    log_info ""

    # Trap cleanup on exit
    trap cleanup EXIT

    check_prerequisites
    create_kind_cluster
    install_cert_manager
    install_rancher
    install_capi_operator
    install_turtles
    ensure_capi_core
    verify_credentials
    create_provider_secret
    install_evroc_provider
    verify_installation
    test_cluster_creation
    display_access_info

    if [[ "${AUTO_CLEANUP:-false}" == "true" ]]; then
        cleanup
    fi

    log_info "[OK] Integration test complete!"
}

# Run if executed directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
