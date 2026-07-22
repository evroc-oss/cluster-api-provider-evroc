# AGENTS.md — fast path for AI agents (and impatient humans)

Cluster API infrastructure provider for evroc Cloud. Go 1.25, controller-runtime,
CAPI v1beta2 (needs clusterctl v1.12+). Reconciles `EvrocCluster` / `EvrocMachine`
into VMs, disks, LBs, security groups via the evroc SDK.

## Repo map

| Path | What |
|---|---|
| `internal/controller/` | EvrocCluster / EvrocMachine reconcilers |
| `internal/cloud/` | SDK client wrapper, credential resolution (`credentials.go`) |
| `templates/` | clusterctl flavors: default, minimal, ha-lb, rke2 |
| `helm/cluster-api-provider-evroc/` | Helm chart (CRDs in `crds/`, installed on first install only) |
| `test/e2e/` | Ginkgo e2e suites; config in `test/e2e/config.yaml` |

## Build / test

```bash
make build          # compile
make test           # unit tests
make manifests      # regenerate CRDs after api/ changes
make test-e2e-capi  # real cloud e2e (needs credentials, creates real VMs)
```

## Install order (management cluster)

1. cert-manager (webhooks need it)
2. CAPI core: `clusterctl init` (add `--bootstrap rke2 --control-plane rke2` for RKE2 flavors)
3. This provider via Helm (see `helm/cluster-api-provider-evroc/README.md`)

## Credentials — the gotchas that cost hours

- Per-cluster only. Secret lives in the **EvrocCluster's namespace**, referenced by `spec.credentialsRef`. No controller-global fallback exists.
- Single Secret format: keys `serviceAccountID`, `serviceAccountSecret`, optional `organization`. (The old `config.yaml` format was removed in v0.2.1.)
- `serviceAccountID` is the **plain SA name**. The SDK builds the OAuth client ID as `<name>_<project>` (project from EvrocCluster spec). If you see `401 invalid_client`: wrong name form, doubled suffix, or expired credential.
- The `serviceAccountSecret` is a base64 JWK printed once by `evroc iam serviceaccount credential create` — store verbatim, never re-encode, beware trailing newlines.
- SA needs roles: `computeOperator`, `networkingOperator`, `loadBalancerOperator` (NOT `storageOperator` — that is S3 only).

## Templates

All template variables must be **exported** even when a default exists —
clusterctl errors on unset vars (`EVROC_SSH_KEY=""` counts as set). Required:
`CLUSTER_NAME`, `EVROC_PROJECT`, `EVROC_CREDENTIALS_SECRET`, `KUBERNETES_VERSION`
(+ `CONTROL_PLANE_MACHINE_COUNT`/`WORKER_MACHINE_COUNT` for default and ha-lb).
kubeadm flavors install Calico automatically; rke2 bundles its own CNI.

## Behaviors to know (not bugs)

- **All cloud APIs are asynchronous.** Delete the CAPI `Cluster` object and move on — the full resource graph releases eventually. Do not block on `--wait`; use `kubectl delete cluster <name> --wait=false` in automation.
- Deleting a standalone `EvrocCluster` (no owning `Cluster`) does not cascade the same way — always delete the top-level `Cluster` object.
- Control-plane nodes get **no public IP**; only the LB does (port 6443). For node access set `EVROC_SSH_KEY` and go through a jump host in the same VPC.
- `connection reset by peer` on 6443 = bootstrap failed on the node, not networking. Check `/var/log/cloud-init-output.log` on the node and the marker file `/run/cluster-api/bootstrap-success.complete`.
- `clusterctl move` requires `--to-kubeconfig` as a file, not a context name.

## evroc CLI cheat sheet

Full reference: https://docs.evroc.com/cli-help/evroc.html — and `--help` works offline at every level. Naming notes: disks are `evroc compute disk` (`storage` = S3 buckets); LB resources are doubled (`evroc loadbalancer loadbalancer list`). Config/profiles live in `~/.evroc/config.yaml` (`--config` to override).

```bash
evroc iam serviceaccount create <name>
evroc iam serviceaccount credential create <key> --service-account <name> --expires-in 48h  # min 24h; JWK printed ONCE
evroc iam serviceaccount get <name>            # status.oauthClientId shows <name>_<project>
evroc iam rolebinding assign --principal "/iam/projects/<proj>/serviceAccounts/<name>" --role /iam/roles/computeOperator
evroc compute vm list|get|delete <id> --force  # --force skips confirmation
evroc loadbalancer loadbalancer list           # + l4route / backendpool / backendservice subgroups
evroc networking publicip|securitygroup|subnet list
```

## Security notes

- VM `cloudInitUserData` (readable via `evroc compute vm get` by anyone with `computeOperator`) contains kubeadm cluster CA private keys. Treat project membership as cluster-admin until platform-side redaction lands.
- Release assets are Cosign-signed (`.bundle` files); images are keyless-signed.
