# Cluster API Provider for evroc

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![CI](https://github.com/evroc-oss/cluster-api-provider-evroc/actions/workflows/ci.yml/badge.svg)](https://github.com/evroc-oss/cluster-api-provider-evroc/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/evroc-oss/cluster-api-provider-evroc)](https://goreportcard.com/report/github.com/evroc-oss/cluster-api-provider-evroc)
[![Go Version](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](./go.mod)

This repository provides the evroc infrastructure provider for Kubernetes Cluster API (CAPI).

The provider reconciles:

- `EvrocCluster` - infrastructure cluster resources
- `EvrocMachine` - compute machine resources
- `EvrocMachineTemplate` - machine templates for MachineDeployments
- `EvrocClusterTemplate` - cluster templates for ClusterClass (advanced)

It manages evroc cloud primitives such as virtual machines, disks, public IPs, security groups, and placement groups.

## Quick Start

### Prerequisites

- Docker running locally
- `kubectl` and `kind` installed
- [cert-manager](https://cert-manager.io/) installed on the management cluster (required for admission webhooks)
- evroc service account (either existing, or created in step 6)

### 1) Create a management cluster

```bash
kind create cluster --name capi-mgmt
kubectl cluster-info
```

### 2) Install cert-manager

The provider uses admission webhooks for validation and defaulting, which require TLS certificates managed by cert-manager:

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml

# Wait for cert-manager to be ready
kubectl wait --for=condition=Available deployment/cert-manager -n cert-manager --timeout=120s
kubectl wait --for=condition=Available deployment/cert-manager-webhook -n cert-manager --timeout=120s
```

### 3) Install clusterctl

This provider targets Cluster API **v1beta2**, which requires clusterctl **v1.12+**.

```bash
# Download clusterctl v1.12.2
curl -L https://github.com/kubernetes-sigs/cluster-api/releases/download/v1.12.2/clusterctl-linux-amd64 \
  -o ~/.local/bin/clusterctl
chmod +x ~/.local/bin/clusterctl

clusterctl version
```

> **macOS:** Replace `linux-amd64` with `darwin-amd64` (or `darwin-arm64` for Apple Silicon).

### 4) Configure clusterctl for evroc provider

```bash
mkdir -p ~/.cluster-api

cat > ~/.cluster-api/clusterctl.yaml <<EOF
providers:
  - name: evroc
    type: InfrastructureProvider
    url: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/latest/infrastructure-components.yaml
EOF
```

### 5) Initialize Cluster API with evroc provider

```bash
# Installs CAPI core, kubeadm bootstrap/control-plane, and evroc infrastructure provider
clusterctl init --infrastructure evroc
```

Verify:

```bash
kubectl get deployments -n capi-evroc-system
# Expected: cluster-api-provider-evroc-controller-manager

kubectl get crd | grep evroc
# Expected CRDs:
#   evrocclusters.infrastructure.cluster.x-k8s.io
#   evrocmachines.infrastructure.cluster.x-k8s.io
#   evrocclustertemplates.infrastructure.cluster.x-k8s.io
#   evrocmachinetemplates.infrastructure.cluster.x-k8s.io
```

### 6) Create a service account

The provider uses service account authentication. Create a service account and credential using the evroc CLI:

```bash
# Create the service account
evroc iam serviceaccount create my-capi-sa

# Create a credential (save the private key — it is only shown once)
evroc iam serviceaccount credential create my-key --service-account my-capi-sa
```

Assign the required roles:

```bash
SA_PRINCIPAL="/iam/projects/<your-project-id>/serviceAccounts/my-capi-sa"

evroc iam rolebinding assign --principal "$SA_PRINCIPAL" --role /iam/roles/computeOperator
evroc iam rolebinding assign --principal "$SA_PRINCIPAL" --role /iam/roles/networkingOperator
evroc iam rolebinding assign --principal "$SA_PRINCIPAL" --role /iam/roles/loadBalancerOperator
```

### 7) Create evroc credentials secret

Create a secret with the service account credentials. Each `EvrocCluster` references its credentials via `spec.credentialsRef`:

```bash
kubectl create secret generic evroc-credentials -n default \
  --from-literal=serviceAccountID=my-capi-sa \
  --from-literal=serviceAccountSecret="<base64-encoded-jwk-private-key>" \
  --from-literal=organization="<your-organization-id>"   # optional
```

> **Important:** `serviceAccountID` is the plain service-account name
> (e.g. `my-capi-sa`) — do **not** append the project ID. The SDK derives the
> OAuth client ID as `<serviceAccountID>_<project>` automatically, using the
> project from the `EvrocCluster` spec. The `serviceAccountSecret` value is the
> base64 JWK exactly as printed by
> `evroc iam serviceaccount credential create` — paste it verbatim; do not
> decode or re-encode it, and make sure no trailing newline is introduced.
>
> The pre-v0.2.1 `config.yaml` Secret format has been **removed**. Secrets
> containing only a `config.yaml` key are rejected with a migration hint.

> **Note:** The secret contains only authentication material. Project and region
> come from the `EvrocCluster` spec (`spec.project` / `spec.region`), which is
> the single source of truth for where resources are created.
>
> For `clusterctl move`, the controller creates an owned copy in the
> `EvrocCluster` namespace. The user-managed source Secret is never owned or
> deleted by the controller. The source Secret must be in the same namespace as
> the `EvrocCluster`.

For multi-tenant environments, create separate secrets per tenant and reference them in each `EvrocCluster`:

```yaml
spec:
  credentialsRef:
    name: tenant-a-evroc-creds
```

Every `EvrocCluster` must name its credentials — there is no controller-level
fallback. All cluster templates include `credentialsRef`; `EVROC_CREDENTIALS_SECRET`
is required and `clusterctl` errors if it is unset:

```bash
export EVROC_CREDENTIALS_SECRET="evroc-credentials"
```

### 7b) Target a private cloud deployment (optional)

By default clusters talk to the public evroc cloud. To target a private
deployment — or a non-production environment such as staging — set
`spec.endpoints` on the `EvrocCluster`:

```yaml
spec:
  endpoints:
    apiBaseURL: https://api.example.com
    issuerURL: https://authn.example.com/realms/evroc-customer
```

These are the same `apiURL` and `issuerURL` values as the profile for that
deployment in the evroc CLI config (`~/.evroc/config.yaml`), so they can be
copied across directly. The OAuth2 token endpoint is derived from `issuerURL`
by appending `/protocol/openid-connect/token`, matching what the evroc SDK does
when it loads that config.

Each field is independently optional; omitting one keeps the public evroc cloud
default for that field. Omit the whole block for the public cloud.

- `apiBaseURL` — base URL for the evroc APIs (`apiURL` in the CLI config).
- `issuerURL` — the OIDC issuer (Keycloak realm) URL, **not** the token
  endpoint. A value ending in `/protocol/openid-connect/token` is rejected.
- `clientID` — only needed if the deployment's identity provider does not
  register clients as `<serviceAccountID>_<project>` (see step 7). Leave unset
  otherwise.

Both URLs must use `https` and are validated at apply time. All three fields are
**immutable once set**: repointing a running cluster at a different deployment
would make the controller look for infrastructure that does not exist there.
Changing endpoints means recreating the cluster.

> **Important:** the credentials in `credentialsRef` must be valid against
> whichever endpoints are configured here. Nothing validates that pairing — a
> mismatch surfaces as an authentication failure when the controller first
> contacts the API, not at apply time. Keep per-environment service accounts in
> separate secrets.

### 8) Create your first workload cluster

**Option A: Using `clusterctl generate cluster` (recommended)**

`clusterctl` handles variable substitution natively. It applies `${VAR:=default}`
defaults **only for variables it has no value for**; any variable it knows about
(including ones you export as empty) is substituted as-is, so the `:=default` is
skipped. Replica counts in particular have no working default — see the note
under **Available flavors** below and always pass `--control-plane-machine-count`
/ `--worker-machine-count`.

```bash
export CLUSTER_NAME="my-cluster"
export EVROC_PROJECT="your-project-id"
export EVROC_REGION="se-sto"
export KUBERNETES_VERSION="v1.28.0"
export EVROC_SSH_KEY="your-ssh-public-key"  # may be empty (""), but MUST be exported — clusterctl errors on unset variables even when the template declares a default

clusterctl generate cluster "${CLUSTER_NAME}" \
  --infrastructure evroc \
  --flavor minimal \
  --kubernetes-version "${KUBERNETES_VERSION}" \
  | kubectl apply -f -
```

**Option B: Using `envsubst` directly**

> **Important:** `envsubst` does **not** support the `${VAR:=default}` bash syntax used in templates. You must either export all variables or strip the defaults first:

```bash
# Export required variables (defaults won't be substituted by envsubst)
export CLUSTER_NAME="my-cluster"
export EVROC_PROJECT="your-project-id"
export EVROC_REGION="se-sto"
export KUBERNETES_VERSION="v1.28.0"
export NAMESPACE="default"
export EVROC_AVAILABILITY_ZONE="a"
export EVROC_IMAGE="ubuntu.22-04.1"
export EVROC_SSH_KEY=""  # set to your SSH public key, or leave empty

# Strip :=default syntax then substitute
sed -E 's/\$\{([A-Z_]+):=[^}]*\}/${\1}/g' templates/cluster-template-minimal.yaml \
  | envsubst | kubectl apply -f -
```

**Available flavors:**
- `minimal` - 1 control plane, 1 worker, Calico CNI pre-installed (dev/test)
- `default` - multi-zone, Cilium CNI (eBPF) via `postKubeadmCommands` (production)
- `calico` - same topology as `default`, with Calico CNI instead of Cilium (opt-in)
- `ha-lb` - multi-zone control plane behind an L4 load balancer, Calico CNI (high availability)
- `rke2` - RKE2 (SUSE enterprise-hardened Kubernetes) with Canal CNI

> **Replica counts:** the `default`, `ha-lb`, and `rke2` templates leave
> `spec.replicas` bound to `${CONTROL_PLANE_MACHINE_COUNT}` /
> `${WORKER_MACHINE_COUNT}` **without a substituted default** — `clusterctl`
> renders an unset count as empty, which yields 1 control plane / 0 workers, not
> 3 / 3. Always pass the counts explicitly (they must be odd for the control
> plane):
>
> ```bash
> clusterctl generate cluster "${CLUSTER_NAME}" \
>   --infrastructure evroc --flavor default \
>   --kubernetes-version "${KUBERNETES_VERSION}" \
>   --control-plane-machine-count 3 \
>   --worker-machine-count 3 \
>   | kubectl apply -f -
> ```

See [templates/](./templates/) for all flavor variables and defaults.

### 9) Monitor cluster creation

```bash
# Watch cluster and machine status
kubectl get cluster,kubeadmcontrolplane,machinedeployment -w

# Watch evroc-specific resources
kubectl get evroccluster,evrocmachine -A

# Detailed cluster description
clusterctl describe cluster "${CLUSTER_NAME}"
```

Typical provisioning takes 3-5 minutes. Machines progress through phases:
`Pending` → `Provisioning` → `Running`

The control plane machine provisions first. Workers start provisioning once the control plane API endpoint is available.

### 10) Access the workload cluster

```bash
clusterctl get kubeconfig "${CLUSTER_NAME}" > "${CLUSTER_NAME}.kubeconfig"
kubectl --kubeconfig="${CLUSTER_NAME}.kubeconfig" get nodes
```

**SSH access** (if `EVROC_SSH_KEY` was set):

```bash
# Get the public IP of a node
kubectl get evrocmachine -o wide

# SSH to a node (kubeadm/Ubuntu images use 'evroc-user')
ssh evroc-user@<PUBLIC_IP>
```

**CNI note:** The `minimal`, `calico`, and `ha-lb` flavors install Calico automatically via `postKubeadmCommands`. The `default` flavor installs Cilium the same way. The `rke2` flavor includes Canal CNI. You only need to install a CNI manually if you are using a custom template without CNI.

```bash
# Only needed for custom templates without built-in CNI:
kubectl --kubeconfig="${CLUSTER_NAME}.kubeconfig" apply -f \
  https://raw.githubusercontent.com/projectcalico/calico/v3.28.0/manifests/calico.yaml

# Wait for nodes Ready
kubectl --kubeconfig="${CLUSTER_NAME}.kubeconfig" wait \
  --for=condition=Ready nodes --all --timeout=5m
```

### Topology Labels

The **single-zone** template (`minimal`) sets both `topology.kubernetes.io/region` and `topology.kubernetes.io/zone` labels on nodes at bootstrap time from `EVROC_REGION` and `EVROC_AVAILABILITY_ZONE`.

The **multi-zone** templates (`default`, `ha-lb`, `rke2`) set only the `topology.kubernetes.io/region` label. Zone labels require either per-zone MachineDeployments (each with its own bootstrap config specifying the zone) or a Cloud Controller Manager (CCM). These templates include a note that nodes will **not** have zone labels until a CCM is available.

These labels are required by the [evroc CSI driver](https://github.com/evroc-oss/evroc-csi-driver) for topology-aware volume placement. If you need per-zone volume placement with a multi-zone template, create separate MachineDeployments per zone:

```yaml
# Example: per-zone worker MachineDeployment for zone "a"
apiVersion: bootstrap.cluster.x-k8s.io/v1beta2
kind: KubeadmConfigTemplate
metadata:
  name: ${CLUSTER_NAME}-workers-zone-a
spec:
  template:
    spec:
      joinConfiguration:
        nodeRegistration:
          kubeletExtraArgs:
          - name: node-labels
            value: "topology.kubernetes.io/region=${EVROC_REGION},topology.kubernetes.io/zone=a"
```

For **RKE2**, topology labels are set via `agentConfig.nodeLabels` in `RKE2ControlPlane` and `RKE2ConfigTemplate`.

Verify labels are set on workload cluster nodes:

```bash
kubectl --kubeconfig="${CLUSTER_NAME}.kubeconfig" get nodes \
  -o custom-columns='NAME:.metadata.name,REGION:.metadata.labels.topology\.kubernetes\.io/region,ZONE:.metadata.labels.topology\.kubernetes\.io/zone'
```

---

## Alternative: Helm-Based Installation

If you prefer Helm over `clusterctl init`, you can install the provider directly:

```bash
helm install evroc-provider \
  oci://ghcr.io/evroc-oss/charts/cluster-api-provider-evroc \
  -n capi-evroc-system \
  --wait
```

This requires that CAPI core components are already installed (e.g., via `clusterctl init` without `--infrastructure`, or via Rancher Turtles).

### Rancher with Turtles

Rancher Turtles installs CAPI core components automatically:

```bash
# Install Rancher
helm repo add rancher-prime https://charts.rancher.com/server-charts/prime
helm upgrade --install rancher rancher-prime/rancher \
  --namespace cattle-system --create-namespace \
  --set hostname="rancher.$(curl -s ifconfig.me).nip.io" \
  --set replicas=1 --set bootstrapPassword=admin \
  --set ingress.tls.source=rancher --wait --timeout=10m

# Install Rancher Turtles (deploys CAPI + RKE2 providers)
helm repo add turtles https://rancher.github.io/turtles
helm upgrade --install rancher-turtles turtles/rancher-turtles \
  --namespace rancher-turtles-system --create-namespace \
  --set cluster-api-operator.enabled=true \
  --set cluster-api-operator.cluster-api.enabled=true \
  --wait --timeout=10m

# Then install the evroc provider via Helm (step above)
```

### RKE2 Flavor

The `rke2` flavor requires the [CAPRKE2](https://github.com/rancher/cluster-api-provider-rke2) bootstrap and control plane providers:

```bash
clusterctl init --infrastructure evroc \
  --bootstrap rke2 \
  --control-plane rke2

clusterctl generate cluster rke2-prod \
  --infrastructure evroc \
  --flavor rke2 \
  --kubernetes-version v1.30.0+rke2r1 \
  | kubectl apply -f -
```

---

## Advanced: ClusterClass

**ClusterClass** is an advanced CAPI feature that provides reusable cluster blueprints. Instead of creating full cluster manifests each time, you define a ClusterClass once and reference it from lightweight Cluster resources.

### When to Use ClusterClass

**Use ClusterClass if:**
- Managing 10+ clusters with similar configurations
- Multi-tenancy scenarios (same blueprint, different clusters)
- Centralized version upgrade management
- Platform teams automating cluster deployments

**Use regular flavors if:**
- Single cluster deployments
- Highly customized unique clusters
- Getting started with CAPI

### Example

See [`examples/clusterclass-example.yaml`](./examples/clusterclass-example.yaml) for a complete working example.

```bash
# 1. Apply the ClusterClass definition
envsubst < examples/clusterclass-example.yaml | kubectl apply -f -

# 2. Create clusters using just 15 lines
cat <<EOF | kubectl apply -f -
apiVersion: cluster.x-k8s.io/v1beta2
kind: Cluster
metadata:
  name: my-cluster
  namespace: default
spec:
  topology:
    class: evroc-basic
    version: v1.28.0
    controlPlane:
      replicas: 3
    workers:
      machineDeployments:
      - class: default-worker
        name: workers
        replicas: 3
EOF
```

The ClusterClass handles all the infrastructure templates, bootstrap configurations, and machine definitions automatically. This approach is far more concise than the 200+ line traditional cluster manifests.

**Prerequisites:**
- CAPI core v1.2+ (topology feature; satisfied by v1.12+ requirement)
- Bootstrap/control-plane providers:
  - Kubeadm (for standard Kubernetes) - installed via `clusterctl init`
  - RKE2 (for SUSE enterprise Kubernetes) - only if using RKE2 flavor

---

## Common Operations

### Scale Workers

```bash
kubectl edit machinedeployment ${CLUSTER_NAME}-md-0
# Change spec.replicas
```

### Scale Control Plane

```bash
kubectl edit kubeadmcontrolplane ${CLUSTER_NAME}-control-plane
# Change spec.replicas (must be odd: 3, 5, 7)
```

### Update Security Groups

```bash
kubectl edit evroccluster ${CLUSTER_NAME}
# Edit spec.controlPlaneConfig.securityGroups.inlineRules
# Changes apply automatically to all existing VMs with inheritFromCluster enabled
```

### Upgrade Kubernetes Version

```bash
kubectl edit kubeadmcontrolplane ${CLUSTER_NAME}-control-plane
# Change spec.version (e.g., v1.29.0)
# Control plane upgrades first, then workers
```

### Delete a Workload Cluster

Deleting the CAPI `Cluster` resource triggers the controller to clean up all associated cloud resources (VMs, disks, public IPs, security groups):

```bash
kubectl delete cluster ${CLUSTER_NAME}
```

Monitor the teardown until all resources are gone:

```bash
kubectl get evrocmachines,evrocclusters -A -w
```

The controller deletes resources in order: workers first, then control plane, then the EvrocCluster (which cleans up shared resources like security groups). This typically takes 1-2 minutes.

> **Important:** Always delete clusters via `kubectl delete cluster` rather than deleting individual machines. Deleting the Cluster resource ensures proper ordering and finalizer-based cleanup. Deleting machines directly can leave orphaned cloud resources.

### Uninstall the Provider

```bash
# Remove the evroc infrastructure provider
helm uninstall evroc-provider -n capi-evroc-system

# Remove CAPI core (optional — only if no other providers need it)
kubectl delete namespace capi-evroc-system
```

---

## Troubleshooting

### Check Provider Logs

```bash
kubectl logs -n capi-evroc-system \
  deployment/cluster-api-provider-evroc-controller-manager -f
```

### Check Machine Status

```bash
kubectl get evrocmachine -A -o yaml
# Look at status.conditions for errors
```

### Common Issues

**Credentials errors (`secret not found`, `credentialsRef must be specified`):**
- `spec.credentialsRef` is required on every EvrocCluster. A cluster without it is rejected at apply time.
- Ensure the referenced secret exists in the **same namespace as the EvrocCluster**, not in `capi-evroc-system`. If `clusterctl generate cluster ... | kubectl apply -f -` creates the cluster in `default`, the secret must also be in `default`.
- Verify the secret contains the `serviceAccountID` and `serviceAccountSecret` keys (step 7). The old `config.yaml` format is rejected.

**`401 invalid_client` in controller logs:**
- `serviceAccountID` must be the plain SA name — the SDK appends `_<project>` itself. A doubled or missing project suffix produces exactly this error.
- Verify the credential is not expired and the JWK was pasted verbatim (no re-encoding, no trailing newline).

**API endpoint refuses connections (`connection reset by peer` on 6443):**
- A *reset* (not timeout) means the LB is up but nothing is listening on the node — bootstrap failed. It is not a firewall problem.
- SSH to the node (via a jump host in the same VPC if nodes have no public IP) and check:
  - `/run/cluster-api/bootstrap-success.complete` — absent means bootstrap did not finish
  - `/var/log/cloud-init-output.log` — the failing command's output is here

**Machines stuck in Provisioning:**
- Check evroc credentials are valid and not expired (token refresh)
- Verify project ID and region are correct
- Check quota limits in evroc console
- Check controller logs for `secrets is forbidden` errors — this indicates RBAC issues with the controller reading bootstrap data

**Machines stuck in Pending:**
- Worker machines remain `Pending` until the control plane API endpoint is available
- If the control plane machine is `Running` but workers are `Pending`, check that the control plane API is reachable (port 6443 open in security groups)

**Nodes NotReady:**
- Ensure CNI is installed — the `minimal` and `ha-lb` flavors install Calico automatically; for custom templates you must install a CNI manually
- Check kubelet logs on workload nodes: `ssh evroc-user@<PUBLIC_IP> sudo journalctl -u kubelet`

**Control plane timeout:**
- Verify security groups allow port 6443 (Kubernetes API) and port 10250 (kubelet)
- Check bootstrap data secret exists: `kubectl get secrets | grep bootstrap`
- Check controller logs: `kubectl logs -n capi-evroc-system deployment/cluster-api-provider-evroc-controller-manager -f`

**Webhook errors (`failed calling webhook`, `connection refused`):**

The provider uses admission webhooks that require cert-manager for TLS certificates. If you see errors like:

```
Internal error occurred: failed calling webhook "mevroccluster.kb.io": connection refused
```

1. Verify cert-manager is installed and running:
   ```bash
   kubectl get pods -n cert-manager
   ```
2. If cert-manager is not installed, install it:
   ```bash
   kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
   kubectl wait --for=condition=Available deployment/cert-manager-webhook -n cert-manager --timeout=120s
   ```
3. Restart the provider controller to pick up the new certificates:
   ```bash
   kubectl rollout restart deployment -n capi-evroc-system
   ```
4. To disable webhooks entirely (not recommended for production):
   ```bash
   kubectl delete mutatingwebhookconfiguration -l cluster.x-k8s.io/provider=infrastructure-evroc
   kubectl delete validatingwebhookconfiguration -l cluster.x-k8s.io/provider=infrastructure-evroc
   ```

---

## Metrics and Observability

The provider exposes Prometheus metrics on port `8080` (enabled by default). These include standard controller-runtime metrics (reconciliation counts, queue depth, work duration) and evroc SDK metrics for authentication, retries, and cloud resource readiness.

### Exposed evroc SDK Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `evroc_sdk_auth_token_refreshes_total` | Counter | Token refresh attempts |
| `evroc_sdk_auth_token_refresh_errors_total` | Counter | Token refresh failures |
| `evroc_sdk_auth_token_refresh_duration_seconds` | Histogram | Token refresh latency |
| `evroc_sdk_auth_initial_auth_total` | Counter | Initial authentication attempts |
| `evroc_sdk_auth_initial_auth_errors_total` | Counter | Initial authentication failures |
| `evroc_sdk_auth_initial_auth_duration_seconds` | Histogram | Initial authentication latency |
| `evroc_sdk_retries_total` | Counter | API call retries (labels: `method`, `status_code`) |
| `evroc_sdk_retry_backoff_duration_seconds` | Histogram | Retry backoff wait time |
| `evroc_sdk_waiter_operations_total` | Counter | Wait-for-ready operations (labels: `resource_type`, `result`) |
| `evroc_sdk_waiter_duration_seconds` | Histogram | Time waiting for resources (labels: `resource_type`) |
| `evroc_sdk_waiter_attempts` | Histogram | Poll attempts per wait operation (labels: `resource_type`) |

The `waiter` metrics are particularly useful for understanding how long cloud resources (VMs, disks, public IPs) take to become ready.

### Configuration

Metrics are controlled via Helm values:

```yaml
metrics:
  enabled: true       # Enable/disable metrics endpoint (default: true)
  port: 8080          # Metrics port (default: 8080)
  serviceMonitor:
    enabled: false     # Create Prometheus Operator ServiceMonitor (default: false)
    interval: 30s
    scrapeTimeout: 10s
    additionalLabels: {}
```

**Disable metrics entirely:**

```bash
helm install evroc-provider \
  oci://ghcr.io/evroc-oss/charts/cluster-api-provider-evroc \
  --set metrics.enabled=false \
  -n capi-evroc-system
```

**Enable Prometheus Operator ServiceMonitor:**

```bash
helm install evroc-provider \
  oci://ghcr.io/evroc-oss/charts/cluster-api-provider-evroc \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.serviceMonitor.additionalLabels.prometheus=kube-prometheus \
  -n capi-evroc-system
```

**Manual scrape (without Prometheus Operator):**

```bash
kubectl port-forward -n capi-evroc-system \
  deployment/cluster-api-provider-evroc-controller-manager 8080:8080
curl http://localhost:8080/metrics
```

---

## Repository Layout

```text
.
├── api/v1beta1/                    # CRD types and webhooks
├── cmd/manager/                    # Controller manager entrypoint
├── internal/controller/            # Reconciliation logic
├── internal/cloud/                 # evroc SDK client wrappers
├── config/                         # CRDs, RBAC, webhook manifests (kustomize)
├── templates/                       # cluster templates & infrastructure-components.yaml
├── examples/                       # ready-to-run example manifests
├── helm/cluster-api-provider-evroc # Helm chart for provider install
└── test/                           # unit, integration, e2e suites
```

## Core Capabilities

- Cluster reconciliation via `EvrocCluster`
- Machine lifecycle management via `EvrocMachine`
- Inline public IP and security group configuration
- Cloud resource ownership tracking in status
- Finalizer-based cleanup for managed cloud resources
- Admission defaulting and validation webhooks
- Cluster API compliance checks and e2e suites

## Custom Labels

You can attach custom labels to all cloud resources (VMs, disks, public IPs, security groups, placement groups) for cost tracking, billing allocation, and resource organization.

### Cluster-Level Labels

Labels defined in `EvrocCluster.spec.additionalLabels` are applied to **every** cloud resource in the cluster, similar to Terraform's project-level `default_labels`:

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocCluster
metadata:
  name: my-cluster
spec:
  project: "your-project-id"
  region: "se-sto"
  additionalLabels:
    department: analytics
    cost-center: "42"
    environment: production
```

### Machine-Level Labels

Labels defined in `EvrocMachine.spec.additionalLabels` are merged with cluster-level labels. When both levels define the same key, **machine labels take precedence**:

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: EvrocMachine
metadata:
  name: gpu-worker-0
spec:
  additionalLabels:
    workload: gpu-training
    environment: staging   # overrides cluster-level "production"
```

### Automatic Ownership Labels

The provider automatically adds ownership labels to every cloud resource. These cannot be overridden by user labels:

| Label | Description |
|-------|-------------|
| `capi_cluster-name` | Name of the owning CAPI cluster |
| `capi_cluster-uid` | UID of the owning cluster (prevents stale resource collisions) |
| `capi_managed-by` | Always `cluster-api-provider-evroc` |

> **Note:** evroc does not allow `/` in label keys, so the provider uses `_` as a separator (e.g., `capi_cluster-name` instead of `capi/cluster-name`).

## Build and Test

```bash
make build
make manifests
make test
make check-compliance
```

Extended testing:

```bash
make test-integration
make test-e2e-capi
make test-e2e-isolated
```

### Service Account Setup for E2E Tests

Follow [step 6](#6-create-a-service-account) to create a service account, then configure the E2E test credentials in `test/e2e/config.yaml`:

```yaml
variables:
  EVROC_PROJECT: "your-project-id"
  EVROC_REGION: "se-sto"
  EVROC_ORGANIZATION: "your-organization-id"
  EVROC_SERVICE_ACCOUNT_ID: "my-capi-sa"
  EVROC_SERVICE_ACCOUNT_SECRET: "<base64-encoded-jwk-private-key>"
```

Then run:

```bash
make e2e-image
make test-e2e-capi
```

## Documentation

- [Helm chart README](./helm/cluster-api-provider-evroc/README.md) - Helm values reference
- [templates/](./templates/) - Available cluster template flavors

## Security

### Bootstrap Data Exposure

The VM `cloudInitUserData` stored by evroc contains kubeadm bootstrap data,
including cluster CA private keys. Anyone with permission to read VM details
(including the `computeOperator` role) can retrieve that data. Until the cloud
API redacts it, treat project membership with VM-read access as equivalent to
cluster-admin access and limit it accordingly.

### Image Signing and Verification

All container images are signed with [Sigstore Cosign](https://github.com/sigstore/cosign) using keyless signing. This ensures that images have not been tampered with and originate from our trusted CI pipeline.

**Verify image signature** (replace `$VERSION` with your release tag):

```bash
cosign verify ghcr.io/evroc-oss/cluster-api-provider-evroc:$VERSION \
  --certificate-identity=https://github.com/evroc-oss/cluster-api-provider-evroc/.github/workflows/release.yml@refs/tags/$VERSION \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com
```

### Software Bill of Materials (SBOM)

Each release includes a Software Bill of Materials generated with [Syft](https://github.com/anchore/syft) in SPDX format and attested to the container image. This enables vulnerability tracking and license compliance.

**Verify and inspect SBOM:**

```bash
cosign verify-attestation ghcr.io/evroc-oss/cluster-api-provider-evroc:$VERSION \
  --type spdxjson \
  --certificate-identity=https://github.com/evroc-oss/cluster-api-provider-evroc/.github/workflows/release.yml@refs/tags/$VERSION \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  | jq -r '.payload' | base64 -d | jq .
```

### SLSA Provenance

All images include [SLSA](https://slsa.dev/) provenance attestations documenting the build process, source materials, and builder identity. This provides supply chain transparency and helps prevent tampering.

**Verify SLSA provenance:**

```bash
cosign verify-attestation ghcr.io/evroc-oss/cluster-api-provider-evroc:$VERSION \
  --type slsaprovenance \
  --certificate-identity=https://github.com/evroc-oss/cluster-api-provider-evroc/.github/workflows/release.yml@refs/tags/$VERSION \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  | jq -r '.payload' | base64 -d | jq .
```

### Helm Chart Signing

Helm chart packages are signed with Cosign. Signatures (`.sig`) and certificates (`.pem`) are published alongside chart releases.

**Verify Helm chart:**

```bash
# Download the chart, signature, and certificate from GitHub release
wget https://github.com/evroc-oss/cluster-api-provider-evroc/releases/download/$VERSION/cluster-api-provider-evroc-${VERSION#v}.tgz
wget https://github.com/evroc-oss/cluster-api-provider-evroc/releases/download/$VERSION/cluster-api-provider-evroc-${VERSION#v}.tgz.bundle

# Verify the signature
cosign verify-blob cluster-api-provider-evroc-${VERSION#v}.tgz \
  --bundle cluster-api-provider-evroc-${VERSION#v}.tgz.bundle \
  --certificate-identity=https://github.com/evroc-oss/cluster-api-provider-evroc/.github/workflows/release.yml@refs/tags/$VERSION \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com
```

## Support and Contributions

This project does not accept external contributions or pull requests. GitHub
Issues are not used for support or bug reporting and are not monitored.

Support requests and bug reports must be submitted through the normal evroc
support channels.

- Cluster API documentation: https://cluster-api.sigs.k8s.io/
- evroc documentation: https://docs.evroc.com/

## License

Apache License 2.0. See `LICENSE`.
