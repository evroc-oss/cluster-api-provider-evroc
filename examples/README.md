# evroc Provider Examples

This directory contains runnable manifests for scenarios beyond the default cluster templates.

Use these examples when you need to compose advanced infrastructure behavior directly in manifests.

## Before You Apply Examples

- Install CAPI core and the evroc infrastructure provider.
- Confirm credentials and project access.
- Export common variables:

```bash
export CLUSTER_NAME="example-cluster"
export EVROC_PROJECT="your-project-id"
export EVROC_REGION="se-sto"
export KUBERNETES_VERSION="v1.31.14"
```

## Example Catalog

| File / Directory | What it demonstrates |
|---|---|
| `cluster-autoscaler.yaml` | machine deployment autoscaling configuration |
| `cluster-with-addons.yaml` | post-provision addon bootstrap patterns; use `envsubst` for variable substitution (see file header) |
| `clusterclass-example.yaml` | topology-driven multi-cluster pattern; requires ClusterClass CRD (CAPI topology feature) |
| `rke2-simple-cluster.yaml` | minimal RKE2 cluster with 1 CP + 3 workers on SL Micro |
| `rke2-sl-micro-cluster.yaml` | production-ready RKE2 cluster on SL Micro with Canal CNI config |
| `terraform-byoi-integration.yaml` | externally managed networking/public resources |

## Usage Pattern

Apply examples with environment substitution when placeholders are present:

```bash
envsubst < cluster-autoscaler.yaml | kubectl apply -f -
```

## Suggested Learning Order

1. Start with `cluster-autoscaler.yaml`
2. Move to `cluster-with-addons.yaml`
3. Apply `terraform-byoi-integration.yaml` if you manage external infra

## Operational Notes

- Examples are intentionally explicit so they can be copied into your own platform manifests.
- Referenced external resources must exist before apply when an example expects existing IDs/names.
- Not all examples are intended for first-time cluster bring-up.

## Related Documentation

- `README.md`
- `templates/` — Cluster template flavors
