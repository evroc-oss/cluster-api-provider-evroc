// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"fmt"

	"github.com/evroc-oss/evroc-go-sdk/config"
	"github.com/evroc-oss/evroc-go-sdk/metrics"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Secret keys for service account credentials.
const (
	// configKey is the secret key for the full evroc SDK config YAML.
	configKey = "config.yaml"

	// Individual secret keys for service account auth.
	keyServiceAccountID     = "serviceAccountID"
	keyServiceAccountSecret = "serviceAccountSecret"
	keyOrganization         = "organization"
)

// ClusterContext carries the project and region from the EvrocCluster spec.
// These are the authoritative source — any project/region in the credentials
// secret is ignored.
type ClusterContext struct {
	Project string
	Region  string
}

// ClientForCluster returns a cloud client for a specific cluster.
// Credentials are read from the Secret referenced by the cluster's credentialsRef,
// which is mandatory. Project and region come from the EvrocCluster spec (not the secret).
//
// The secret must use one of two formats:
//  1. A "config.yaml" key containing the full evroc SDK config YAML with service account auth.
//  2. Individual keys: serviceAccountID, serviceAccountSecret (and optionally organization).
//
// Only service account authentication is supported.
func ClientForCluster(
	ctx context.Context,
	k8sClient client.Reader,
	secretName, secretNamespace string,
	clusterCtx ClusterContext,
	m *metrics.Manager,
) (ClientInterface, error) {
	if secretName == "" {
		return nil, fmt.Errorf("cluster has no credentialsRef")
	}

	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: secretName, Namespace: secretNamespace}
	if err := k8sClient.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("failed to get credentials secret %s/%s: %w", secretNamespace, secretName, err)
	}

	// Format 1: full YAML config (must use service account auth)
	if data, ok := secret.Data[configKey]; ok {
		return newClientFromYAMLWithContext(ctx, data, clusterCtx, m)
	}

	// Format 2: individual keys for service account auth
	cfg, err := configFromServiceAccountKeys(secret.Data, clusterCtx)
	if err != nil {
		return nil, fmt.Errorf("credentials secret %s/%s: %w", secretNamespace, secretName, err)
	}
	return NewClientFromConfig(ctx, cfg, m)
}

// newClientFromYAMLWithContext parses YAML credentials and overrides
// project/region from the EvrocCluster spec.
func newClientFromYAMLWithContext(ctx context.Context, data []byte, clusterCtx ClusterContext, m *metrics.Manager) (ClientInterface, error) {
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse credentials YAML: %w", err)
	}
	cfg.SetDefaults()
	cfg.Context.Project = clusterCtx.Project
	if clusterCtx.Region != "" {
		cfg.Context.Region = clusterCtx.Region
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid credentials: %w", err)
	}
	return NewClientFromConfig(ctx, &cfg, m)
}

// configFromServiceAccountKeys builds an SDK Config from individual secret keys.
// Project and region come from the EvrocCluster spec, not from the secret.
func configFromServiceAccountKeys(data map[string][]byte, clusterCtx ClusterContext) (*config.Config, error) {
	saID := string(data[keyServiceAccountID])
	saSecret := string(data[keyServiceAccountSecret])
	organization := string(data[keyOrganization])

	if saID == "" || saSecret == "" {
		return nil, fmt.Errorf("missing required keys: %s and %s", keyServiceAccountID, keyServiceAccountSecret)
	}

	cfg := &config.Config{
		Auth: config.AuthConfig{
			ServiceAccountID:     saID,
			ServiceAccountSecret: saSecret,
		},
		Context: config.ContextConfig{
			Project:      clusterCtx.Project,
			Region:       clusterCtx.Region,
			Organization: organization,
		},
	}
	cfg.SetDefaults()
	return cfg, nil
}
