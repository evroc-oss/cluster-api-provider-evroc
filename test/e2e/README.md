# E2E Certification Tests

This directory contains end-to-end tests for SUSE Rancher Cluster API provider certification.

## Prerequisites

Before running certification tests, ensure you have the following tools installed:

### Required Dependencies

1. **Go 1.24+**
   ```bash
   # Verify installation
   go version
   ```

2. **Docker**
   ```bash
   # Install Docker (Ubuntu/Debian)
   sudo apt-get update
   sudo apt-get install -y docker.io

   # Start Docker service
   sudo systemctl start docker
   sudo systemctl enable docker

   # Add user to docker group (avoid sudo)
   sudo usermod -aG docker $USER
   newgrp docker

   # Verify installation
   docker version
   ```

3. **kubectl v1.31.14+**
   ```bash
   # Install kubectl
   curl -LO "https://dl.k8s.io/release/v1.31.14/bin/linux/amd64/kubectl"
   chmod +x kubectl
   sudo mv kubectl /usr/local/bin/

   # Verify installation
   kubectl version --client
   ```

4. **kind v0.20.0+**
   ```bash
   # Install kind
   curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64
   chmod +x ./kind
   sudo mv ./kind /usr/local/bin/kind

   # Verify installation
   kind version
   ```

5. **Helm v3.16.0+**
   ```bash
   # Install Helm
   curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

   # Verify installation
   helm version
   ```

6. **Ginkgo v2** (installed via make cert-prepare)
   ```bash
   # Install ginkgo
   go install github.com/onsi/ginkgo/v2/ginkgo@latest

   # Add Go bin to PATH (IMPORTANT!)
   export PATH=$PATH:$(go env GOPATH)/bin

   # Make permanent (add to ~/.bashrc or ~/.zshrc)
   echo 'export PATH=$PATH:$(go env GOPATH)/bin' >> ~/.bashrc
   source ~/.bashrc

   # Verify installation
   ginkgo version
   ```

### Quick Install All Dependencies

```bash
# 1. Install system dependencies
sudo apt-get update
sudo apt-get install -y docker.io curl

# 2. Setup Docker
sudo systemctl start docker
sudo usermod -aG docker $USER
newgrp docker

# 3. Install kubectl
curl -LO "https://dl.k8s.io/release/v1.31.14/bin/linux/amd64/kubectl"
chmod +x kubectl && sudo mv kubectl /usr/local/bin/

# 4. Install kind
curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64
chmod +x kind && sudo mv kind /usr/local/bin/

# 5. Install Helm
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

# 6. Install ginkgo and update PATH
go install github.com/onsi/ginkgo/v2/ginkgo@latest
export PATH=$PATH:$(go env GOPATH)/bin
echo 'export PATH=$PATH:$(go env GOPATH)/bin' >> ~/.bashrc

# 7. Verify all installations
docker version
kubectl version --client
kind version
helm version
ginkgo version
```

## Structure

```
test/e2e/
├── config.yaml.sample                   # Copy and fill in EVROC_* values
├── config.yaml                          # Local test configuration (not for production secrets)
├── go.mod                               # Go module for tests
├── README.md                            # This file
└── suites/
    └── rancher-turtles/                 # Rancher Turtles integration tests
        └── rancher_turtles_test.go      # Main test suite
```

## Quick Start

```bash
# Install dependencies
make cert-prepare

# Create local test config from sample
cp test/e2e/config.yaml.sample test/e2e/config.yaml
# Edit test/e2e/config.yaml and set variables.EVROC_* values

# Run certification tests
make test-e2e-isolated

# View results
ls -la ../../_artifacts/
```

## Configuration

Edit `config.yaml` to customize test parameters and credentials:

- Kubernetes versions
- Rancher/Turtles versions
- Test environment type
- Cleanup behavior
- `variables.EVROC_*` credentials used by the e2e suites

Authentication in `variables` uses a service account, which is what the provider
requires:

- `EVROC_SERVICE_ACCOUNT_ID` — the service **account** name (not the credential
  name), from which the OAuth client ID is derived as
  `<serviceAccountID>_<project>`
- `EVROC_SERVICE_ACCOUNT_SECRET` — the base64 JWK, pasted verbatim
- `EVROC_ORGANIZATION` — optional

### Choosing an environment

Tests run against the public evroc cloud unless told otherwise. To target
another deployment, set both endpoint variables to the `apiURL`/`issuerURL` of
the matching profile in `~/.evroc/config.yaml`:

```yaml
  EVROC_API_BASE_URL: "https://api.<env>.example.com"
  EVROC_ISSUER_URL: "https://authn.<env>.example.com/realms/evroc-customer"
```

`EVROC_ISSUER_URL` is the issuer (realm) URL — the token endpoint is derived
from it. Leave both empty for the public cloud.

Every value in a config file must belong to the **same** environment: project,
organization, service account, and endpoints together. A mismatch is not caught
at apply time — it surfaces as an authentication failure during reconcile.

Both suites select their config by env var, so per-environment copies can live
side by side (all are git-ignored, since they hold live credentials):

```bash
E2E_CONFIG_PATH=$PWD/test/e2e/config.prod.yaml make test-e2e-rancher-turtles
CAPI_E2E_CONFIG_PATH=$PWD/test/e2e/capi-e2e-config.prod.yaml make test-e2e-capi
```

> Internal: see `docs/internal/staging-setup.md` for the staging environment
> values, service-account creation, and how to read each auth failure mode.

## Running Tests

### All Tests

```bash
make test-e2e-isolated
```

### Specific Suite

```bash
cd suites/rancher-turtles
ginkgo -v .
```

### With Custom Config

```bash
E2E_CONFIG_PATH=/path/to/config.yaml make test-e2e
```

## Debugging

Keep environment after tests:

```bash
SKIP_RESOURCE_CLEANUP=1 make test-e2e-isolated
```

Access the cluster:

```bash
export KUBECONFIG=../../_artifacts/kubeconfig
kubectl get pods -A
```

## CAPI QuickStart Suite

Upstream Cluster API QuickStart certification tests (`suites/capi`), run against
live evroc infrastructure. Run locally — these tests are not part of CI.

Variables are read from `test/e2e/capi-e2e-config.yaml`. This file is git-ignored
because it holds account-specific values (project ID, SSH key).

```bash
# Create your local config from the sample and fill in EVROC_* values
cp test/e2e/capi-e2e-config.yaml.sample test/e2e/capi-e2e-config.yaml
# Edit variables.EVROC_PROJECT and variables.EVROC_SSH_KEY

# Run the suite
make test-e2e-capi
```

The suite loads `test/e2e/capi-e2e-config.yaml` by default, or the path in
`CAPI_E2E_CONFIG_PATH`. Each variable also falls back to the OS environment.

### Custom VPC / dual-stack

`EVROC_VPC_REF` and `EVROC_SUBNET_A` are **optional**. When unset, the controller
uses the project's **default VPC** and the per-zone **default subnet**
(`default-{region}-{zone}`).

They are only needed to exercise the dual-stack path, and they reference
**pre-existing** resources — nothing in this repo creates them. Create the VPC
and subnet yourself first with the evroc CLI, then set the two variables to the
names you chose:

```bash
# Create a dual-stack VPC
evroc networking vpc create capi-e2e-dualstack \
  --stack-type=dual-stack \
  --ipv4-cidr-block=10.0.0.0/16

# Create a dual-stack subnet in zone a within that VPC
evroc networking subnet create capi-e2e-ds-a \
  --stack-type=dual-stack \
  --ipv4-cidr-block=10.0.5.0/24 \
  --vpc=capi-e2e-dualstack \
  --zone=a

# Then in capi-e2e-config.yaml:
#   EVROC_VPC_REF: "capi-e2e-dualstack"
#   EVROC_SUBNET_A: "capi-e2e-ds-a"
```

For an ipv6-only cluster instead, use `--stack-type=ipv6-only` (no
`--ipv4-cidr-block`) on both commands and set `EVROC_STACK_TYPE: "ipv6-only"`.
Clean up afterward with `evroc networking subnet delete` / `vpc delete`.

## More Information

For Rancher integration, see the "Rancher with Turtles" section in the [README](../../README.md).
