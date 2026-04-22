#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 evroc
#
# Automated version bump script for cluster-api-provider-evroc
# Usage: ./scripts/bump-version.sh <version>
#        ./scripts/bump-version.sh v1.2.3
#        ./scripts/bump-version.sh 1.2.3

set -euo pipefail

if [ -z "$1" ]; then
    echo "Error: Version number required"
    echo "Usage: ./scripts/bump-version.sh <version>"
    echo "Example: ./scripts/bump-version.sh v1.2.3"
    exit 1
fi

INPUT_VERSION="$1"
CHART_FILE="helm/cluster-api-provider-evroc/Chart.yaml"
VALUES_FILE="helm/cluster-api-provider-evroc/values.yaml"
CHANGELOG_FILE="CHANGELOG.md"
SYNC_SCRIPT_FILE="scripts/sync-to-github.sh"

# Strip 'v' prefix if present
NEW_VERSION="${INPUT_VERSION#v}"

# Validate version format (semantic versioning)
if ! [[ "$NEW_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: Invalid version format '$INPUT_VERSION'"
    echo "Version must be in format: [v]major.minor.patch (e.g., v1.2.3 or 1.2.3)"
    exit 1
fi

# Get current version from Chart.yaml
CURRENT_VERSION=$(grep "^version:" "$CHART_FILE" | awk '{print $2}')

if [ -z "$CURRENT_VERSION" ]; then
    echo "Error: Could not find current version in $CHART_FILE"
    exit 1
fi

echo "Current version: $CURRENT_VERSION"
echo "New version: $NEW_VERSION"
OLD_TAG="v$CURRENT_VERSION"
NEW_TAG="v$NEW_VERSION"

# Update Chart.yaml
echo "Updating $CHART_FILE..."
sed -i "s/^version: .*/version: $NEW_VERSION/" "$CHART_FILE"
sed -i "s/^appVersion: .*/appVersion: \"$NEW_VERSION\"/" "$CHART_FILE"

# Update values.yaml image tag
echo "Updating $VALUES_FILE..."
sed -i "s/tag: v[0-9]*\.[0-9]*\.[0-9]*/tag: $NEW_TAG/" "$VALUES_FILE"

# Generate infrastructure-components.yaml into templates/
echo "Generating templates/infrastructure-components.yaml for $NEW_TAG..."
if command -v helm >/dev/null 2>&1; then
    helm template cluster-api-provider-evroc helm/cluster-api-provider-evroc \
        --namespace capi-evroc-system \
        --set controller.image.tag="$NEW_TAG" \
        --set evroc.existingConfigSecret=evroc-credentials \
        --set fullnameOverride=cluster-api-provider-evroc \
        --include-crds \
        > "templates/infrastructure-components.yaml"
else
    echo "ERROR: helm is required to generate infrastructure-components.yaml"
    exit 1
fi

# Update metadata.yaml version
echo "Updating metadata.yaml for $NEW_TAG..."
MAJOR=$(echo "$NEW_VERSION" | cut -d. -f1)
MINOR=$(echo "$NEW_VERSION" | cut -d. -f2)
sed -i "s/cluster.x-k8s.io\/version: .*/cluster.x-k8s.io\/version: $NEW_TAG/" metadata.yaml
sed -i "s/^latest: .*/latest: $NEW_TAG/" metadata.yaml
sed -i "s/major: [0-9]*/major: $MAJOR/" metadata.yaml
sed -i "s/minor: [0-9]*/minor: $MINOR/" metadata.yaml

# Update VERSION file
echo "Updating VERSION file..."
echo "$NEW_TAG" > VERSION

# Keep sync script examples aligned with current release version.
if [ -f "$SYNC_SCRIPT_FILE" ]; then
    echo "Updating $SYNC_SCRIPT_FILE examples..."
    sed -i "s/${OLD_TAG//./\\.}/${NEW_TAG//./\\.}/g" "$SYNC_SCRIPT_FILE"
fi

# Update CHANGELOG.md - replace [Unreleased] with the new version
if [ -f "$CHANGELOG_FILE" ]; then
    echo "Updating $CHANGELOG_FILE..."
    TODAY=$(date +%Y-%m-%d)
    sed -i "s/## \[Unreleased\]/## [$NEW_VERSION] - $TODAY/" "$CHANGELOG_FILE"

    # Add [Unreleased] link at the bottom if it doesn't exist
    if ! grep -q "\[Unreleased\]:" "$CHANGELOG_FILE"; then
        echo "" >> "$CHANGELOG_FILE"
        echo "[Unreleased]: https://github.com/evroc-oss/cluster-api-provider-evroc/compare/v$NEW_VERSION...HEAD" >> "$CHANGELOG_FILE"
    fi

    # Add version link at the bottom if it doesn't exist
    if ! grep -q "\[$NEW_VERSION\]:" "$CHANGELOG_FILE"; then
        echo "[$NEW_VERSION]: https://github.com/evroc-oss/cluster-api-provider-evroc/releases/tag/v$NEW_VERSION" >> "$CHANGELOG_FILE"
    fi
fi

# Stage and commit
echo "Staging changes..."
git add "$CHART_FILE"
git add "$VALUES_FILE"
git add VERSION
git add metadata.yaml
git add templates/infrastructure-components.yaml
[ -f "$CHANGELOG_FILE" ] && git add "$CHANGELOG_FILE"
[ -f "$SYNC_SCRIPT_FILE" ] && git add "$SYNC_SCRIPT_FILE"

echo "Creating commit..."
git commit -m "chore: bump version to v$NEW_VERSION"

echo "Creating tag v$NEW_VERSION..."
git tag "v$NEW_VERSION"

echo ""
echo "[OK] Version bumped successfully!"
echo "   Old: v$CURRENT_VERSION"
echo "   New: v$NEW_VERSION"
echo ""
echo "Files updated:"
echo "   - $CHART_FILE"
echo "   - $VALUES_FILE"
echo "   - VERSION"
echo "   - metadata.yaml"
echo "   - templates/infrastructure-components.yaml"
[ -f "$CHANGELOG_FILE" ] && echo "   - $CHANGELOG_FILE"
[ -f "$SYNC_SCRIPT_FILE" ] && echo "   - $SYNC_SCRIPT_FILE"
echo ""
echo "To push to remote, run:"
echo "   git push origin \$(git branch --show-current)"
echo "   git push origin v$NEW_VERSION"
