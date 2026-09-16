# Integration Tests

Integration tests validate controller behavior for the active infrastructure model:

- `EvrocCluster` reconciliation
- `EvrocMachine` reconciliation
- inline networking/security/public IP/placement/disk semantics

## Prerequisites

- envtest (Kubernetes API server + etcd) — installed via `make envtest`
- evroc API credentials

## Credentials Setup

Provide credentials using one of these methods:

### Option A: Environment variables

```bash
export EVROC_PROJECT="your-project-id"
export EVROC_REGION="se-sto"

# Authentication (choose one):
# Token-based:
export EVROC_TOKEN="your-access-token"
export EVROC_REFRESH_TOKEN="your-refresh-token"

# Or username/password:
export EVROC_USERNAME="your-username"
export EVROC_PASSWORD="your-password"
```

### Option B: E2E config file

Copy the sample config and fill in the `variables.EVROC_*` section:

```bash
cp test/e2e/config.yaml.sample test/e2e/config.yaml
# Edit test/e2e/config.yaml and set:
#   variables.EVROC_PROJECT
#   variables.EVROC_REGION
#   variables.EVROC_TOKEN (or EVROC_USERNAME + EVROC_PASSWORD)
```

Environment variables take precedence over the config file.

## Run

From the repository root:

```bash
make test-integration
```

Or directly:

```bash
KUBEBUILDER_ASSETS="$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use 1.35.0 -p path)" \
  INTEGRATION_TEST=1 go test -v -timeout 30m ./test/integration/...
```
