# cluster-api-provider-evroc Helm Chart

Cluster API infrastructure provider for [evroc Cloud](https://evroc.com). Deploys the controller manager that reconciles `EvrocCluster`, `EvrocMachine`, and `EvrocMachineTemplate` resources.

## Prerequisites

- Kubernetes >= 1.27
- Cluster API (CAPI) core components installed (v1.11+)
- cert-manager (if `webhook.certManager.enabled: true`)
- An evroc Cloud account with API credentials

## Installation

### 1. Create the credentials secret

The controller needs an evroc API configuration file. Create a Kubernetes secret with the credentials before installing the chart:

```bash
kubectl create namespace capi-evroc-system

kubectl create secret generic evroc-credentials \
  --namespace capi-evroc-system \
  --from-file=config.yaml=/path/to/your/evroc-config.yaml
```

The `config.yaml` file should contain evroc SDK credentials:

```yaml
auth:
  token: "your-access-token"
  refresh_token: "your-refresh-token"

context:
  project: "your-project-id"
  region: "se-sto"
  organization: "your-organization-id"
```

See the [README](../../README.md) for all credential options (username/password, per-cluster credentials).

### 2. Install the chart

```bash
helm install capi-evroc ./helm/cluster-api-provider-evroc \
  --namespace capi-evroc-system \
  --set evroc.existingConfigSecret=evroc-credentials
```

## Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `evroc.existingConfigSecret` | **Required.** Name of the Secret containing `config.yaml` | `""` |
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
| `metrics.enabled` | Enable Prometheus metrics endpoint | `true` |
| `metrics.port` | Metrics port | `8080` |
| `metrics.service.annotations` | Annotations for the metrics Service | `{}` |
| `metrics.serviceMonitor.enabled` | Create Prometheus ServiceMonitor | `false` |
| `metrics.serviceMonitor.interval` | Prometheus scrape interval | `30s` |
| `metrics.serviceMonitor.scrapeTimeout` | Prometheus scrape timeout | `10s` |
| `metrics.serviceMonitor.additionalLabels` | Labels to match your Prometheus `serviceMonitorSelector` | `{}` |
| `logging.level` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `logging.format` | Log format (`json` or `text`) | `json` |
| `installCRDs` | Install CRDs with the chart | `true` |
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
