#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 evroc

set -euo pipefail

EXPECTED_TAG="${1:-$(tr -d '[:space:]' < VERSION)}"
EXPECTED_TAG="v${EXPECTED_TAG#v}"
EXPECTED_VERSION="${EXPECTED_TAG#v}"

fail() {
    echo "release version check failed: $*" >&2
    exit 1
}

[[ "$EXPECTED_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || \
    fail "'$EXPECTED_TAG' is not a stable semantic version tag"

VERSION_FILE=$(tr -d '[:space:]' < VERSION)
CHART_VERSION=$(awk '$1 == "version:" { print $2; exit }' helm/cluster-api-provider-evroc/Chart.yaml)
APP_VERSION=$(awk '$1 == "appVersion:" { gsub(/"/, "", $2); print $2; exit }' helm/cluster-api-provider-evroc/Chart.yaml)
IMAGE_TAG=$(awk '$1 == "tag:" { print $2; exit }' helm/cluster-api-provider-evroc/values.yaml)
METADATA_VERSION=$(awk '$1 == "cluster.x-k8s.io/version:" { print $2; exit }' metadata.yaml)
METADATA_LATEST=$(awk '$1 == "latest:" { print $2; exit }' metadata.yaml)
METADATA_SERIES=$(awk '$1 == "-" && $2 == "major:" { maj=$3 } $1 == "minor:" { printf "%s.%s ", maj, $2 }' metadata.yaml)
EXPECTED_MAJOR=${EXPECTED_VERSION%%.*}
VERSION_REMAINDER=${EXPECTED_VERSION#*.}
EXPECTED_MINOR=${VERSION_REMAINDER%%.*}

[[ "$VERSION_FILE" == "$EXPECTED_TAG" ]] || fail "VERSION contains '$VERSION_FILE', expected '$EXPECTED_TAG'"
[[ "$CHART_VERSION" == "$EXPECTED_VERSION" ]] || fail "Chart version is '$CHART_VERSION', expected '$EXPECTED_VERSION'"
[[ "$APP_VERSION" == "$EXPECTED_VERSION" ]] || fail "Chart appVersion is '$APP_VERSION', expected '$EXPECTED_VERSION'"
[[ "$IMAGE_TAG" == "$EXPECTED_TAG" ]] || fail "Helm image tag is '$IMAGE_TAG', expected '$EXPECTED_TAG'"
[[ "$METADATA_VERSION" == "$EXPECTED_TAG" ]] || fail "metadata label is '$METADATA_VERSION', expected '$EXPECTED_TAG'"
[[ "$METADATA_LATEST" == "$EXPECTED_TAG" ]] || fail "metadata latest is '$METADATA_LATEST', expected '$EXPECTED_TAG'"
[[ " $METADATA_SERIES" == *" $EXPECTED_MAJOR.$EXPECTED_MINOR "* ]] || fail "metadata releaseSeries '$METADATA_SERIES' lacks $EXPECTED_MAJOR.$EXPECTED_MINOR"

MANIFEST_VERSION_LINES=$(grep -E 'helm.sh/chart: cluster-api-provider-evroc-|app.kubernetes.io/version:|image: "ghcr.io/evroc-oss/cluster-api-provider-evroc:' templates/infrastructure-components.yaml)
if grep -Ev "cluster-api-provider-evroc-${EXPECTED_VERSION}$|app.kubernetes.io/version: \"${EXPECTED_VERSION}\"$|cluster-api-provider-evroc:${EXPECTED_TAG}\"$" <<< "$MANIFEST_VERSION_LINES"; then
    fail "templates/infrastructure-components.yaml contains inconsistent release versions"
fi

CHANGELOG_SECTION=$(awk -v heading="## [${EXPECTED_VERSION}]" '
    index($0, heading) == 1 { found = 1; next }
    found && /^## \[/ { exit }
    found { print }
' CHANGELOG.md)
[[ -n "$CHANGELOG_SECTION" ]] || fail "CHANGELOG.md has no entry for $EXPECTED_VERSION"
grep -q '^### ' <<< "$CHANGELOG_SECTION" || fail "CHANGELOG.md entry for $EXPECTED_VERSION has no sections"
grep -q '^- ' <<< "$CHANGELOG_SECTION" || fail "CHANGELOG.md entry for $EXPECTED_VERSION has no release notes"

echo "Release metadata, manifests, and changelog consistently use $EXPECTED_TAG"
