# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 evroc

# Include .env file if it exists
-include .env

# Image URL to use all building/pushing image targets
IMG ?= controller:latest

# Kubernetes version used by controller-runtime envtest.
ENVTEST_K8S_VERSION ?= 1.31.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate CRD manifests.
	$(CONTROLLER_GEN) crd paths="./api/...;./cmd/...;./internal/..." output:crd:artifacts:config=./helm/cluster-api-provider-evroc/crds/
	# The upstream clusterv1.APIEndpoint type carries +kubebuilder:validation:MinProperties=1,
	# which blocks status updates when controlPlaneEndpoint is not yet set (zero value = {}).
	# Strip it post-generation so the CRD allows an absent/zero endpoint.
	@sed -i '/minProperties: 1/d' helm/cluster-api-provider-evroc/crds/infrastructure.cluster.x-k8s.io_evrocclusters.yaml

.PHONY: generate-templates
generate-templates: ## Generate templates/infrastructure-components.yaml from Helm chart.
	helm template cluster-api-provider-evroc helm/cluster-api-provider-evroc \
		--namespace capi-evroc-system \
		--set controller.image.tag="$(shell cat VERSION)" \
		--set fullnameOverride=cluster-api-provider-evroc \
		--set namespace.create=true \
		--include-crds \
		> templates/infrastructure-components.yaml

.PHONY: generate
generate: manifests controller-gen generate-templates ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations and update templates.
	$(CONTROLLER_GEN) object:headerFile="scripts/boilerplate.go.txt" paths="./api/...;./cmd/...;./internal/..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet ## Run unit tests.
	go test ./... -coverprofile cover.out

.PHONY: coverage
coverage: manifests generate fmt vet ## Run unit tests and display coverage report.
	go test ./... -coverprofile=cover.out
	@# Filter out auto-generated code, mocks, and thin SDK wrappers for accurate reporting
	@head -1 cover.out > cover.filtered.out
	@tail -n +2 cover.out | grep -v 'zz_generated\|mocks/\|/client\.go:\|cmd/\|test/integration' >> cover.filtered.out
	@echo ""
	@echo "=== Filtered coverage (excludes generated code, mocks, SDK wrappers) ==="
	@go tool cover -func=cover.filtered.out | tail -1
	@echo ""
	@echo "HTML report: go tool cover -html=cover.filtered.out"

.PHONY: test-controller-integration
test-controller-integration: manifests generate fmt vet envtest ## Run controller integration tests with envtest.
	KUBEBUILDER_ASSETS="$(shell $(LOCALBIN)/setup-envtest use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test ./internal/controller -tags=integration -v

.PHONY: envtest
envtest: ## Download envtest binaries locally if necessary.
	@test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
	@$(LOCALBIN)/setup-envtest use -p path $(ENVTEST_K8S_VERSION)

.PHONY: test-integration
test-integration: manifests generate envtest ## Run integration tests (requires EVROC credentials).
	KUBEBUILDER_ASSETS="$(shell $(LOCALBIN)/setup-envtest use $(ENVTEST_K8S_VERSION) -p path)" \
	INTEGRATION_TEST=1 go test -v -timeout 30m ./test/integration/...

.PHONY: test-published-chart
test-published-chart: ## Test the published Helm chart from GHCR (requires cluster + credentials).
	@./test/e2e/test-published-chart.sh

.PHONY: check-compliance
check-compliance: ## Check CAPI 1.12 compliance.
	@./scripts/check-capi-compliance.sh

.PHONY: validate-templates
validate-templates: ## Validate cluster templates are well-formed YAML.
	@./scripts/validate-templates.sh

.PHONY: validate-manifests
validate-manifests: manifests ## Validate manifests against CRD schemas and the Helm chart renders to valid YAML.
	go test ./test/manifests/...

.PHONY: verify
verify: test check-compliance validate-templates validate-manifests verify-release-version ## Run all verification checks (tests + compliance + templates + manifest schemas + chart render).
	@echo "All verification checks passed!"

.PHONY: verify-release-version
verify-release-version: ## Verify all release metadata matches VERSION and the changelog is populated.
	@./scripts/verify-release-version.sh

##@ Build

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -w -s \
	-X github.com/evroc-oss/cluster-api-provider-evroc/pkg/version.Version=$(VERSION) \
	-X github.com/evroc-oss/cluster-api-provider-evroc/pkg/version.GitCommit=$(GIT_COMMIT) \
	-X github.com/evroc-oss/cluster-api-provider-evroc/pkg/version.BuildDate=$(BUILD_DATE)

.PHONY: build
build: generate fmt vet ## Build manager binary.
	go build -ldflags="$(LDFLAGS)" -o bin/manager ./cmd/manager

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/manager/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build -f Dockerfile -t ${IMG} \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.19.0

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

##@ Certification

.PHONY: test-e2e-rancher-turtles
test-e2e-rancher-turtles: e2e-image ## Run Rancher Turtles E2E certification tests.
	@echo "Running Rancher Turtles E2E certification tests..."
	@echo "Logging to: $(E2E_LOG_FILE)"
	cd test/e2e && go mod download && \
	E2E_CONFIG=$(PWD)/test/e2e/config.yaml \
	E2E_CONFIG_PATH=$(PWD)/test/e2e/config.yaml \
	ARTIFACTS_FOLDER=$(PWD)/_artifacts \
	HELM_BINARY_PATH=$(shell which helm) \
	HELM_EXTRA_VALUES_FOLDER=$(PWD)/_artifacts \
	XDG_CONFIG_HOME=$(PWD)/_artifacts/xdg \
	CAPI_KUBECTL_PATH=$(shell which kubectl) \
	CLUSTERCTL_BINARY_PATH=$(CLUSTERCTL_V112) \
	REPO_ROOT=$(PWD) \
	EVROC_CREDENTIALS_FILE=$(EVROC_CREDENTIALS_FILE) \
	E2E_LOCAL_IMAGE=$(E2E_LOCAL_IMAGE) \
	go run github.com/onsi/ginkgo/v2/ginkgo -v -trace --tags e2e --timeout=2h -output-dir=$(PWD)/_artifacts \
	./suites/rancher-turtles 2>&1 | tee "$(E2E_LOG_FILE)"

.PHONY: test-e2e-capi
test-e2e-capi: ## Run upstream CAPI QuickStart E2E tests (requires live evroc credentials + kind).
	@echo "Running CAPI QuickStart E2E tests..."
	cd test/e2e && go mod download && \
	CAPI_E2E_CONFIG_PATH=$(PWD)/test/e2e/capi-e2e-config.yaml \
	ARTIFACTS_FOLDER=$(PWD)/_artifacts \
	CAPI_KUBECTL_PATH=$(shell which kubectl) \
	CLUSTERCTL_BINARY_PATH=$(CLUSTERCTL_V112) \
	REPO_ROOT=$(PWD) \
	EVROC_CREDENTIALS_FILE=$(EVROC_CREDENTIALS_FILE) \
	E2E_LOCAL_IMAGE=$(E2E_LOCAL_IMAGE) \
	go run github.com/onsi/ginkgo/v2/ginkgo -v -trace --tags e2e --timeout=$(GINKGO_TIMEOUT) -output-dir=$(PWD)/_artifacts \
	./suites/capi

.PHONY: test-e2e-conformance
test-e2e-conformance: manifests ## Run CAPI conformance tests (validates CRD contract compliance, no cluster needed).
	cd test/e2e && go mod download && \
	REPO_ROOT=$(PWD) \
	go run github.com/onsi/ginkgo/v2/ginkgo -v -trace -output-dir=$(PWD)/_artifacts \
	./suites/conformance

E2E_LOCAL_IMAGE ?= ghcr.io/evroc-oss/cluster-api-provider-evroc:latest
E2E_LOG_FILE ?= $(PWD)/_artifacts/rancher-turtles-e2e-$(shell date +%Y%m%d-%H%M%S).log

# Ginkgo Suite Timeout for the CAPI e2e run. Each spec provisions a real VM+LB,
# so the whole suite needs above ginkgo's 1h default to run all 11 specs.
GINKGO_TIMEOUT ?= 2h30m

# Pin clusterctl to v1.12 for v1beta2 compatibility.
# The run-e2e-tests.sh downloads this to /tmp if not already present.
CLUSTERCTL_V112 ?= $(HOME)/.local/bin/clusterctl-v1.12

.PHONY: e2e-image
e2e-image: ## Build and tag the provider image for local E2E use (uses local SDK copy).
	@if [ -n "$$GITHUB_TOKEN" ]; then \
		echo "$$GITHUB_TOKEN" > /tmp/.github-token-build && \
		DOCKER_BUILDKIT=1 docker build \
		  --secret id=github_token,src=/tmp/.github-token-build \
		  --file $(PWD)/Dockerfile \
		  -t $(E2E_LOCAL_IMAGE) \
		  $(PWD); \
		rm -f /tmp/.github-token-build; \
	else \
		DOCKER_BUILDKIT=1 docker build \
		  --file $(PWD)/Dockerfile \
		  -t $(E2E_LOCAL_IMAGE) \
		  $(PWD); \
	fi

.PHONY: test-e2e-isolated
test-e2e-isolated: e2e-image ## Build image and run E2E tests in isolated-kind (local only).
	MANAGEMENT_CLUSTER_ENVIRONMENT=isolated-kind \
	E2E_LOCAL_IMAGE=$(E2E_LOCAL_IMAGE) \
	$(MAKE) test-e2e-rancher-turtles

.PHONY: test-e2e-kind
test-e2e-kind: ## Run E2E tests in kind with ngrok (requires ngrok credentials).
	MANAGEMENT_CLUSTER_ENVIRONMENT=kind $(MAKE) test-e2e-rancher-turtles

.PHONY: cert-prepare
cert-prepare: ## Prepare for certification (install dependencies).
	@echo "Installing certification dependencies..."
	@command -v ginkgo >/dev/null 2>&1 || \
		(echo "Installing ginkgo..." && go install github.com/onsi/ginkgo/v2/ginkgo@latest)
	@command -v kind >/dev/null 2>&1 || \
		(echo "kind not found. Please install kind: https://kind.sigs.k8s.io/docs/user/quick-start/#installation" && exit 1)
	@command -v helm >/dev/null 2>&1 || \
		(echo "helm not found. Please install helm: https://helm.sh/docs/intro/install/" && exit 1)
	@command -v kubectl >/dev/null 2>&1 || \
		(echo "kubectl not found. Please install kubectl" && exit 1)
	@echo "All dependencies are installed!"

.PHONY: cert-clean
cert-clean: ## Clean up certification test artifacts.
	rm -rf _artifacts test/e2e/_artifacts
	kind delete cluster --name capi-test 2>/dev/null || true

.PHONY: cert-info
cert-info: ## Display certification information and requirements.
	@echo "==================================================================="
	@echo "  SUSE Rancher Cluster API Provider Certification"
	@echo "==================================================================="
	@echo ""
	@echo "This provider implements the Rancher Turtles certification suite"
	@echo "for validating integration with SUSE Rancher Prime Cluster API."
	@echo ""
	@echo "Documentation:"
	@echo "  https://documentation.suse.com/cloudnative/cluster-api/v0.17/en/operator/certificationsuite.html"
	@echo ""
	@echo "Prerequisites:"
	@echo "  - Docker"
	@echo "  - kind"
	@echo "  - kubectl"
	@echo "  - helm"
	@echo "  - ginkgo (installed via 'make cert-prepare')"
	@echo ""
	@echo "Quick start:"
	@echo "  1. Prepare dependencies: make cert-prepare"
	@echo "  2. Run certification:    make test-e2e-isolated"
	@echo ""
	@echo "For cloud provider testing with ngrok:"
	@echo "  export NGROK_API_KEY=your-api-key"
	@echo "  export NGROK_AUTHTOKEN=your-auth-token"
	@echo "  make test-e2e-kind"
	@echo ""
	@echo "Configuration template: test/e2e/config.yaml.sample"
	@echo "Create local config: cp test/e2e/config.yaml.sample test/e2e/config.yaml"
	@echo "==================================================================="
