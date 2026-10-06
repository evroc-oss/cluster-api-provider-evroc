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

## Running against an air-gapped or private deployment

The `TestCloudClient_*` tests in `cloud_integration_test.go` only call the evroc
API. They don't use envtest and read their credentials from
`test/e2e/credentials.yaml`, so they can run from any host that can reach the
API, without Go or the repository.

Build a static binary:

```bash
make test-integration-binary   # writes bin/cape-cloud-integration.test
```

Copy it to the target host with a `test/e2e/credentials.yaml` next to it.
`api.base_url` and `auth.token_url` point the client at the deployment instead
of the public evroc cloud:

```yaml
api:
  base_url: https://api.<domain>
auth:
  token_url: https://authn.<domain>/realms/evroc-customer/protocol/openid-connect/token
  # a user...
  username: <user>
  password: <password>
  # ...or a service account
  # service_account_id: <name>
  # service_account_secret: <base64 JWK>
context:
  project: <project>
  region: <region>
  organization: <organization-id>
```

Run it from the directory that contains `test/`. If the deployment uses a
private CA, point `SSL_CERT_FILE` at a bundle that includes it:

```bash
SSL_CERT_FILE=/path/to/ca-bundle.crt INTEGRATION_TEST=1 \
  ./cape-cloud-integration.test -test.v -test.run 'TestCloudClient_' -test.timeout 15m
```

`TestCloudClient_SecurityGroupLifecycle` creates and deletes a security group in
the project; the other tests are read-only.
