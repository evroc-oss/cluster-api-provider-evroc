# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.5] - 2026-09-04

### Added

- External cloud-controller-manager support. Set the
  `infrastructure.cluster.x-k8s.io/external-cloud-provider: "true"` annotation
  on an `EvrocCluster` to leave node metadata, topology labels, and removal of
  the cloud-provider initialization taint to the CCM. Unannotated clusters keep
  the existing CAPE-managed node initialization behavior.
- A validated standalone cluster example that installs Calico, the evroc CCM,
  and the evroc CSI driver with separate service-account credentials.

## [0.3.4] - 2026-08-25

### Fixed

- Set the EvrocCluster `Ready` condition to `True` once reconciliation succeeds, so a `CredentialsNotFound` condition no longer sticks after the secret is created.

## [0.3.3] - 2026-08-25

### Fixed

- Re-queue reconcilation of an EvrocCluster cluster if the secret it is referencing changes.
- Fix template `preKubeadmCommands` to pin to the right *patch* version of `kubelet`, `kubeadm` and `kubectl` within a minor version.
- A machine's availability zone is assigned once and persisted before any
  cloud resource is created. Re-picking the least-used zone on every reconcile
  could create the boot disk and the VM in different zones, which failed the
  VM start until the machine was recreated.

## [0.3.2] - 2026-08-14

### Added

- `spec.endpoints` on `EvrocCluster` selects which evroc deployment a cluster
  talks to (`apiBaseURL`, `issuerURL`, and an optional `clientID`). Omit it to
  use the public evroc cloud. `apiBaseURL` and `issuerURL` are the same values
  as `apiURL`/`issuerURL` in an evroc CLI config profile, so they can be copied
  across; the OAuth2 token endpoint is derived from `issuerURL`. The URLs are
  validated at apply time and are immutable once set, since repointing a running
  cluster would orphan its infrastructure. Cluster templates accept the values
  via `EVROC_API_BASE_URL` and `EVROC_ISSUER_URL`; when unset the `endpoints`
  block is omitted entirely, so existing manifests are unchanged.

### Fixed

- Machines no longer get stuck in `MachineOperationFailed` when the boot disk
  briefly reports the non-terminal `DiskImageImportCompleted` condition.
- Placement is now patched only when the placement group changes, instead of
  retrying the immutable `placement.zone` field on every reconcile.

### Documentation

- Dropped `/download` from the clusterctl provider URL in the README, which
  prevented `clusterctl init --infrastructure evroc` from finding its assets.

## [0.3.0] - 2026-07-31

### Added

- IPv6/dual-stack networking, custom VPC/subnet selection, and new `dualstack`
  and `calico` flavors. The default kubeadm flavor now uses Cilium.

### Changed

- Cloud resource ownership and asynchronous VM deletion now work reliably
  across `clusterctl move` and controller reconciliations.

### Fixed

- Fixed standalone release image builds and added the Namespace to standalone
  `infrastructure-components.yaml` manifests.
- Load balancer teardown now finds and removes related resources after moves or
  incomplete status updates.

### Security

- Documented that VM-readable cloud-init data contains kubeadm secrets, making
  VM-read access equivalent to cluster-admin access.

## [0.2.1] - 2026-07-21

### Removed

- **BREAKING:** The `config.yaml` credentials Secret format was removed.
  Credentials Secrets must use the `serviceAccountID` / `serviceAccountSecret`
  (and optional `organization`) keys; Secrets containing only a `config.yaml`
  key are rejected with a migration hint. This also removes the bug where the
  YAML path derived an OAuth client ID with an empty project suffix, causing
  `401 invalid_client` on every token request.
- The Helm chart no longer templates the release Namespace, fixing first
  installs: `helm install` previously failed with either "namespace already
  exists" (with `--create-namespace`) or "namespace not found" (without).
  Create the namespace up front or pass `--create-namespace`.
- The unused `installCRDs` and `namespace` chart values were removed. CRDs ship
  in the chart's `crds/` directory (installed on first install; apply manually
  on upgrades).

### Fixed

- kubeadm cloud-init now installs `conntrack`, which `kubeadm init` requires;
  without it every kubeadm-flavor cluster failed bootstrap with the API
  endpoint resetting connections.
- The default cluster template now installs Calico like the other kubeadm
  flavors; previously its nodes stayed `NotReady` (`cni plugin not
  initialized`).
- Cloud-resource deletion now requeues on a fixed 10s interval instead of
  feeding controller-runtime's exponential backoff, and logs which resources
  it is still waiting on.
- Removed the placeholder `0.0.0.0` API-server certSAN from kubeadm templates
  (kubeadm includes the control-plane endpoint automatically).
- `sync-to-github.sh` now requires an explicit `--branch`/`--tag` and pushes to
  the named ref, instead of deriving a `sync/snapshot-<version>` branch that
  could collide with existing branches.

### Added

- `AGENTS.md`: first-contact guide for AI agents and new contributors,
  including an evroc CLI cheat sheet and known pitfalls.
- Chart README documents the GHCR image pull secret requirement and the CRD
  upgrade procedure.
- README troubleshooting for `401 invalid_client` and API-endpoint
  connection-reset diagnosis.

## [0.2.0] - 2026-07-21

### Added

- Managed load balancers, Cluster API move support, custom VPC configuration,
  and a load-balancer-enabled HA template.
- ArtifactHub repository metadata for Helm chart discovery.

### Removed
- **BREAKING:** Global controller credentials were removed; the provider no longer reads `/etc/evroc/config.yaml`, and the Helm chart no longer accepts `evroc.existingConfigSecret`.

### Changed

- **BREAKING:** `EvrocCluster.spec.credentialsRef` is now required. Clusters that
  previously omitted it and inherited the controller's credentials are rejected at
  apply time and must name their credentials explicitly.
- Upgraded `evroc-go-sdk` to v0.7.1 and Go to 1.25.

### Fixed

- Hardened load balancer, credential, RKE2, and cluster-move reconciliation,
  with expanded manifest and end-to-end validation.
- Modernized GitHub Actions and added Sigstore bundles for all release assets.

## [0.1.2] - 2026-02-11

### Fixed
- Resolved SA4006 lint errors for unused variable assignments in disk and machine controllers
- Fixed webhook validation test failures by standardizing zone format to simple a/b/c notation
- Corrected error wrapping in status helper to use proper error chains

### Changed
- Standardized availability zone format across all webhooks and tests to use simple notation (a, b, c)
- Updated all test cases to use consistent se-sto region

## [0.1.1] - 2026-02-11

### Added
- Initial release of cluster-api-provider-evroc
- Complete SDK integration for all controllers
- SDK waiters and status helpers for async operations
- Support for EvrocCluster and EvrocMachine resources
- Webhook validation and defaulting for custom resources
- Integration with Cluster API v1beta2

### Features
- Automated cluster provisioning on evroc Cloud
- Multi-zone failure domain support
- Persistent disk management through inline machine configuration
- Security group and public IP management
- Placement group support for VM spread

[0.1.2]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.2
[0.1.1]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.1

[Unreleased]: https://github.com/evroc-oss/cluster-api-provider-evroc/compare/v0.3.5...HEAD
[0.1.6]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.6
[0.1.14]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.14
[0.1.15]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.15
[0.1.16]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.16
[0.1.17]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.17
[0.1.18]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.18
[0.1.19]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.19
[0.1.20]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.20
[0.1.21]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.21
[0.1.22]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.22
[0.1.23]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.23
[0.1.24]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.24
[0.1.25]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.25
[0.1.26]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.26
[0.1.27]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.27
[0.1.28]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.1.28
[0.2.0]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.2.0
[0.2.1]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.2.1
[0.3.0]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.3.0
[0.3.1]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.3.1
[0.3.2]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.3.2
[0.3.3]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.3.3
[0.3.4]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.3.4
[0.3.5]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v0.3.5
