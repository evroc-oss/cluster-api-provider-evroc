# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 evroc

# Multi-stage build for minimal image size

# Stage 1: Build the controller manager binary
FROM golang:1.25-alpine AS builder

# Build arguments for version information
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown

# Install build dependencies
RUN apk add --no-cache git make

# Set working directory
WORKDIR /workspace

# Copy go mod files
COPY go.mod go.sum ./

# Configure Git to use the provided token for private repos
# The github_token secret is provided by the build workflow
RUN --mount=type=secret,id=github_token \
    if [ -f /run/secrets/github_token ]; then \
        export GITHUB_TOKEN=$(cat /run/secrets/github_token) && \
        git config --global url."https://${GITHUB_TOKEN}@github.com/".insteadOf "https://github.com/"; \
    fi && \
    GOPRIVATE=github.com/evroc-oss GOSUMDB=off go mod download

# Copy source code (only what's needed for the binary)
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s \
    -X github.com/evroc-oss/cluster-api-provider-evroc/pkg/version.Version=${VERSION} \
    -X github.com/evroc-oss/cluster-api-provider-evroc/pkg/version.GitCommit=${GIT_COMMIT} \
    -X github.com/evroc-oss/cluster-api-provider-evroc/pkg/version.BuildDate=${BUILD_DATE}" \
    -o /manager \
    ./cmd/manager

# Stage 2: Create minimal runtime image
FROM gcr.io/distroless/static:nonroot

# Copy the binary from builder
COPY --from=builder /manager /manager

# Use nonroot user
USER 65532:65532

# Set the entrypoint
ENTRYPOINT ["/manager"]
