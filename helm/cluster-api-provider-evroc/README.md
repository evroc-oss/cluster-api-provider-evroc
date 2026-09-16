# cluster-api-provider-evroc Helm Chart

Cluster API infrastructure provider for [evroc Cloud](https://evroc.com). Deploys the controller manager that reconciles `EvrocCluster`, `EvrocMachine`, and `EvrocMachineTemplate` resources.

## Prerequisites

- Kubernetes 1.31-1.36 on the management cluster
- Cluster API (CAPI) core and kubeadm components v1.12.8 or newer (tested with
  v1.12.11)
- cert-manager (if `webhook.certManager.enabled: true`)
- An evroc Cloud account with API credentials

## Installation

### 1. Install the chart

```bash
helm install capi-evroc oci://ghcr.io/evroc-oss/charts/cluster-api-provider-evroc \
  --namespace capi-evroc-system --create-namespace
```

### 2. Create per-cluster credentials

The controller holds **no global credentials**. Each `EvrocCluster` names a
Secret in **its own namespace** via `spec.credentialsRef`, containing
`serviceAccountID` and `serviceAccountSecret` keys (and optionally
`organization`). See the [README](../../README.md) "Create evroc credentials
secret" for details.

### Upgrading

Helm installs the `crds/` directory on first install but **never upgrades
CRDs**. Before `helm upgrade` across provider versions, apply them manually:

```bash
kubectl apply --server-side -f helm/cluster-api-provider-evroc/crds/
```

## Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `controller.replicas` | Number of controller replicas (leader election selects one active) | `1` |
| `controller.image.repository` | Controller container image | `ghcr.io/evroc-oss/cluster-api-provider-evroc` |
| `controller.image.tag` | Image tag | See `values.yaml` |
| `controller.resources.limits.cpu` | CPU limit | `500m` |
| `controller.resources.limits.memory` | Memory limit | `512Mi` |
| `controller.resources.requests.cpu` | CPU request | `100m` |
| `controller.resources.requests.memory` | Memory request | `128Mi` |
| `controller.leaderElection.enabled` | Enable leader election for HA | `true` |
| `controller.nodeSelector` | Node selector for controller pod | `{}` |
| `controller.tolerations` | Tolerations for controller pod | `[]` |
| `controller.affinity` | Affinity rules for controller pod | `{}` |
| `controller.extraEnv` | Additional environment variables | `[]` |
| `webhook.enabled` | Enable admission webhooks | `true` |
| `webhook.port` | Webhook server port | `9443` |
| `webhook.certManager.enabled` | Use cert-manager for webhook TLS | `true` |
| `rbac.create` | Create RBAC resources | `true` |
| `serviceAccount.create` | Create service account | `true` |
| `serviceAccount.name` | Service account name (auto-generated if empty) | `""` |
| `metrics.enabled` | Enable Prometheus metrics endpoint | `false` |
| `metrics.port` | Metrics port | `8080` |
| `metrics.service.annotations` | Annotations for the metrics Service | `{}` |
| `metrics.serviceMonitor.enabled` | Create Prometheus ServiceMonitor | `false` |
| `metrics.serviceMonitor.interval` | Prometheus scrape interval | `30s` |
| `metrics.serviceMonitor.scrapeTimeout` | Prometheus scrape timeout | `10s` |
| `metrics.serviceMonitor.additionalLabels` | Labels to match your Prometheus `serviceMonitorSelector` | `{}` |
| `logging.level` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `logging.format` | Log format (`json` or `text`) | `json` |
| `debug` | Enable debug mode | `false` |

## Uninstallation

```bash
helm uninstall capi-evroc --namespace capi-evroc-system
```

CRDs are not removed by `helm uninstall`. To remove them manually:

```bash
kubectl delete crd evrocclusters.infrastructure.cluster.x-k8s.io
kubectl delete crd evrocclustertemplates.infrastructure.cluster.x-k8s.io
kubectl delete crd evrocmachines.infrastructure.cluster.x-k8s.io
kubectl delete crd evrocmachinetemplates.infrastructure.cluster.x-k8s.io
```
