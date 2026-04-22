// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"fmt"
	"strings"

	"github.com/evroc-oss/evroc-go-sdk/config"
	"github.com/evroc-oss/evroc-go-sdk/metrics"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// configKey is the secret key for the full evroc SDK config YAML.
// This is the same key used by the global credentials mount and the Helm chart.
const configKey = "config.yaml"

// Flat-key prefix used by external credential stores (e.g. Rancher cloud credentials).
// Keys look like "evroccredentialConfig-token", "evroccredentialConfig-project", etc.
const flatKeyPrefix = "evroccredentialConfig-"

// ClientForCluster returns a cloud client for a specific cluster. If secretName
// and secretNamespace are provided, the credentials are read from that Secret.
// The secret can use either format:
//
//  1. A "config.yaml" key containing the full evroc SDK config YAML.
//  2. Flat keys prefixed with "evroccredentialConfig-" (token, refreshToken, project, region).
//
// Otherwise the fallback global client is returned.
func ClientForCluster(
	ctx context.Context,
	k8sClient client.Reader,
	fallback ClientInterface,
	secretName, secretNamespace string,
	m *metrics.Manager,
) (ClientInterface, error) {
	if secretName == "" {
		if fallback == nil {
			return nil, fmt.Errorf("no credentialsRef on cluster and no global credentials configured")
		}
		return fallback, nil
	}

	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: secretName, Namespace: secretNamespace}
	if err := k8sClient.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("failed to get credentials secret %s/%s: %w", secretNamespace, secretName, err)
	}

	// Format 1: full YAML config
	if data, ok := secret.Data[configKey]; ok {
		return NewClientFromYAML(ctx, data, m)
	}

	// Format 2: flat key-value pairs (e.g. from Rancher cloud credentials)
	if cfg, ok := configFromFlatKeys(secret.Data); ok {
		return NewClientFromConfig(ctx, cfg, m)
	}

	return nil, fmt.Errorf("credentials secret %s/%s: expected either a %q key or %s* keys",
		secretNamespace, secretName, configKey, flatKeyPrefix)
}

// configFromFlatKeys builds an SDK Config from flat secret keys like
// "evroccredentialConfig-token". Returns false if the required keys are missing.
func configFromFlatKeys(data map[string][]byte) (*config.Config, bool) {
	get := func(field string) string {
		for k, v := range data {
			// Case-insensitive prefix match to tolerate minor casing variations.
			if strings.EqualFold(k, flatKeyPrefix+field) {
				return string(v)
			}
		}
		return ""
	}

	token := get("token")
	refreshToken := get("refreshToken")
	username := get("username")
	password := get("password")
	project := get("project")
	region := get("region")

	hasTokenAuth := token != "" || refreshToken != ""
	hasPasswordAuth := username != "" && password != ""
	if !hasTokenAuth && !hasPasswordAuth {
		return nil, false
	}

	cfg := &config.Config{
		Auth: config.AuthConfig{
			Token:        token,
			RefreshToken: refreshToken,
			Username:     username,
			Password:     password,
		},
		Context: config.ContextConfig{
			Project:      project,
			Region:       region,
			Organization: get("organization"),
		},
	}
	cfg.SetDefaults()
	return cfg, true
}
