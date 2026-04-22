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

3. **kubectl v1.30.0+**
   ```bash
   # Install kubectl
   curl -LO "https://dl.k8s.io/release/v1.30.0/bin/linux/amd64/kubectl"
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
curl -LO "https://dl.k8s.io/release/v1.30.0/bin/linux/amd64/kubectl"
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

Authentication in `variables` supports both valid platform methods:

- token-based: `EVROC_TOKEN` (+ optional `EVROC_REFRESH_TOKEN`)
- username/password: `EVROC_USERNAME` + `EVROC_PASSWORD`

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

## More Information

For Rancher integration, see the "Rancher with Turtles" section in the [README](../../README.md).
