// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"fmt"

	"github.com/evroc-oss/evroc-go-sdk/config"
	"github.com/evroc-oss/evroc-go-sdk/metrics"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Secret keys for service account credentials.
const (
	keyServiceAccountID     = "serviceAccountID"
	keyServiceAccountSecret = "serviceAccountSecret"
	keyOrganization         = "organization"

	// legacyConfigKey is the removed pre-v0.2.1 YAML format, detected only to
	// give migrating users an actionable error.
	legacyConfigKey = "config.yaml"
)

// ClusterContext carries the project, region and endpoint overrides from the
// EvrocCluster spec. These are the authoritative source — any project/region in
// the credentials secret is ignored.
type ClusterContext struct {
	Project string
	Region  string

	// APIBaseURL, AuthTokenURL and ClientID override the SDK's public evroc
	// cloud defaults for private cloud deployments. Empty means "use the
	// default", which SetDefaults fills in.
	APIBaseURL   string
	AuthTokenURL string
	ClientID     string
}

// ClientForCluster returns a cloud client for a specific cluster.
// Credentials are read from the Secret referenced by the cluster's credentialsRef,
// which is mandatory. Project and region come from the EvrocCluster spec (not the secret).
//
// The secret must contain the keys serviceAccountID and serviceAccountSecret
// (and optionally organization). Only service account authentication is
// supported.
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

	cfg, err := configFromServiceAccountKeys(secret.Data, clusterCtx)
	if err != nil {
		if _, hasLegacy := secret.Data[legacyConfigKey]; hasLegacy {
			return nil, fmt.Errorf("credentials secret %s/%s uses the removed config.yaml format; recreate it with the %s and %s keys (see README step 7)",
				secretNamespace, secretName, keyServiceAccountID, keyServiceAccountSecret)
		}
		return nil, fmt.Errorf("credentials secret %s/%s: %w", secretNamespace, secretName, err)
	}
	return NewClientFromConfig(ctx, cfg, m)
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

	// Endpoint overrides are set before SetDefaults, which only fills in fields
	// left empty — so an unset override keeps the public evroc cloud default.
	cfg := &config.Config{
		Auth: config.AuthConfig{
			ServiceAccountID:     saID,
			ServiceAccountSecret: saSecret,
			TokenURL:             clusterCtx.AuthTokenURL,
			ClientID:             clusterCtx.ClientID,
		},
		API: config.APIConfig{
			BaseURL: clusterCtx.APIBaseURL,
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
