#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 evroc
#
# Validates cluster templates are well-formed YAML after variable substitution.
# Run via: make validate-templates

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TEMPLATES_DIR="$REPO_ROOT/templates"

# Check for python3 (needed for YAML validation)
if ! command -v python3 &>/dev/null; then
  echo "ERROR: python3 is required for YAML validation"
  exit 1
fi

ERRORS=0

# Dummy values for variable substitution
export CLUSTER_NAME="validate-test"
export EVROC_PROJECT="00000000-0000-0000-0000-000000000000"
export EVROC_CREDENTIALS_SECRET="evroc-credentials"
export EVROC_REGION="se-sto"
export KUBERNETES_VERSION="v1.31.14"
export EVROC_SSH_KEY="ssh-ed25519 AAAA test@test"
export NAMESPACE="default"
export EVROC_AVAILABILITY_ZONE="a"
export EVROC_IMAGE="ubuntu.24-04.1"
export EVROC_CONTROL_PLANE_FLAVOR="a1a.m"
export EVROC_WORKER_FLAVOR="a1a.m"
export EVROC_CONTROL_PLANE_DISK_SIZE="100"
export EVROC_WORKER_DISK_SIZE="100"
export EVROC_PLACEMENT_STRATEGY="spread"
export POD_CIDR="10.244.0.0/16"
export SERVICE_CIDR="10.96.0.0/12"
export CONTROL_PLANE_MACHINE_COUNT="3"
export WORKER_MACHINE_COUNT="3"
export CONTROL_PLANE_MACHINE_COMPUTE_PROFILE="a1a.m"
export WORKER_MACHINE_COMPUTE_PROFILE="a1a.s"
export EVROC_ALLOWED_CIDR="0.0.0.0/0"
export EVROC_ALLOWED_CIDR_V6="::/0"
export EVROC_VPC_CIDR="10.0.0.0/8"
export EVROC_ORGANIZATION="00000000-0000-0000-0000-000000000000"
export EVROC_API_BASE_URL="https://api.evroc.com"
export EVROC_ISSUER_URL="https://authn.iam.evroc.com/realms/evroc-customer"
export EVROC_TOKEN_URL="https://authn.iam.evroc.com/realms/evroc-customer/protocol/openid-connect/token"
export EVROC_CCM_SA_ID="ccm-agent"
export EVROC_CCM_SA_SECRET="test-ccm-secret"
export EVROC_CSI_SA_ID="csi-agent"
export EVROC_CSI_SA_SECRET="test-csi-secret"
export EVROC_CCM_CHART_VERSION="0.1.2"
export EVROC_CSI_CHART_VERSION="0.2.3"
export GITHUB_USER="evroc"
export GITHUB_TOKEN="test-package-token"

templates=(
  "$TEMPLATES_DIR"/cluster-template*.yaml
  "$REPO_ROOT/examples/standalone-cluster-ccm-csi.yaml"
)

echo "=== Validating cluster templates ==="
echo ""

for template in "${templates[@]}"; do
  name="$(basename "$template")"
  echo -n "  $name ... "

  # Step 1: Strip ${VAR:=default} syntax to ${VAR}, then envsubst
  substituted=$(sed -E 's/\$\{([A-Z_]+):=[^}]*\}/${\1}/g' "$template" | envsubst)

  # Step 2: Check for unsubstituted variables (${...} remaining)
  remaining=$(echo "$substituted" | grep -oP '\$\{[A-Z_]+\}' | sort -u || true)
  if [ -n "$remaining" ]; then
    echo "FAIL (unsubstituted variables: $remaining)"
    ERRORS=$((ERRORS + 1))
    continue
  fi

  # Step 3: Validate as multi-document YAML
  result=$(echo "$substituted" | python3 -c "
import sys, yaml
try:
    docs = list(yaml.safe_load_all(sys.stdin))
    # Filter out None docs (from trailing ---)
    docs = [d for d in docs if d is not None]
    kinds = [d.get('kind', 'UNKNOWN') for d in docs]
    embedded = 0
    for doc in docs:
        if doc.get('kind') != 'ConfigMap':
            continue
        for key, value in (doc.get('data') or {}).items():
            if key.endswith(('.yaml', '.yml')):
                embedded_docs = [d for d in yaml.safe_load_all(value) if d is not None]
                embedded += len(embedded_docs)
    suffix = ', %d embedded documents' % embedded if embedded else ''
    print('OK (%d documents%s: %s)' % (len(docs), suffix, ', '.join(kinds)))
except yaml.YAMLError as e:
    # Extract line/col info if available
    if hasattr(e, 'problem_mark') and e.problem_mark:
        mark = e.problem_mark
        print('FAIL (YAML error at line %d, col %d: %s)' % (mark.line + 1, mark.column + 1, e.problem))
    else:
        print('FAIL (YAML error: %s)' % str(e))
    sys.exit(1)
" 2>&1)

  status=$?
  echo "$result"
  if [ $status -ne 0 ]; then
    ERRORS=$((ERRORS + 1))
  fi
done

# Step 4: Validate block scalar indentation in raw templates (before substitution).
# A common mistake is writing:
#   - |
#   script body here     <-- WRONG: must be indented under the "- |"
# instead of:
#   - |
#     script body here   <-- CORRECT
echo ""
echo "=== Checking block scalar indentation ==="

for template in "${templates[@]}"; do
  name="$(basename "$template")"
  # Find lines matching "- |" and check the NEXT non-empty line is indented further
  line_num=0
  while IFS= read -r line; do
    line_num=$((line_num + 1))
    # Match a YAML list item that starts a block scalar: "- |" or "- |+"  etc.
    if echo "$line" | grep -qP '^\s+-\s+\|[+-]?\s*$'; then
      block_indent=$(echo "$line" | grep -oP '^\s+' | wc -c)
      # Read next non-empty line
      next_line_num=$((line_num + 1))
      next_line=$(sed -n "${next_line_num}p" "$template")
      # Skip comment lines
      while echo "$next_line" | grep -qP '^\s*#|^\s*$'; do
        next_line_num=$((next_line_num + 1))
        next_line=$(sed -n "${next_line_num}p" "$template")
      done
      if [ -n "$next_line" ]; then
        next_indent=$(echo "$next_line" | grep -oP '^\s+' | wc -c)
        if [ "$next_indent" -le "$block_indent" ]; then
          echo "  $name:$line_num FAIL: block scalar body not indented under '- |'"
          echo "    line $line_num: $(echo "$line" | sed 's/^/  /')"
          echo "    line $next_line_num: $(echo "$next_line" | sed 's/^/  /')"
          ERRORS=$((ERRORS + 1))
        fi
      fi
    fi
  done < "$template"
done

echo ""
if [ "$ERRORS" -gt 0 ]; then
  echo "FAILED: $ERRORS error(s) found"
  exit 1
else
  echo "All templates are valid!"
fi
