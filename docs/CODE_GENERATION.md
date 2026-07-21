# Code Generation

This repository makes heavy use of code generation. Running a single command should keep everything in sync, but understanding the pipeline helps when things go wrong.

## Quick Start

```bash
make generate
```

This is the one command that regenerates all derived artifacts from the Go source code.

## Pipeline

The generation flow looks like this:

```
Go structs (api/...)
    │
    ▼
controller-gen ──┬──► Helm CRDs   (helm/cluster-api-provider-evroc/crds/*.yaml)
    │            ├──► DeepCopy code (zz_generated.deepcopy.go)
    │            └──► Webhook code  (generated webhook stubs)
    │
    ▼
helm template --include-crds
    │
    ▼
templates/infrastructure-components.yaml
```

## What Is and Is NOT Automated

### Automated
- **CRD schemas** — every `// +kubebuilder:validation:...` or `// +kubebuilder:resource:...` marker in `api/v1beta1/*.go` is translated into the OpenAPI schema inside the CRD YAML.
- **DeepCopy boilerplate** — `controller-gen object` produces `zz_generated.deepcopy.go`.
- **`templates/infrastructure-components.yaml`** — produced by Helm templating, so it always matches the current chart + CRDs.

### Manual (for now)
- **RBAC** (`helm/cluster-api-provider-evroc/templates/rbac.yaml`) is hand-written. The repository does *not* sync `config/rbac/role.yaml` (produced by `controller-gen rbac`) into the Helm chart.
  - If you add a new `// +kubebuilder:rbac:` marker for controller-runtime permissions, remember to mirror the new rule into `helm/cluster-api-provider-evroc/templates/rbac.yaml` so it is included in `infrastructure-components.yaml`.

## CI Enforcement

The GitHub Actions workflow runs `make generate` and then verifies that **no tracked file changed**:

If you see a CI failure telling you to run `make generate`, commit the resulting changes and push again.
