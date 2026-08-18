// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestConfigFromServiceAccountKeys(t *testing.T) {
	clusterCtx := ClusterContext{Project: "spec-project", Region: "se-sto"}

	tests := []struct {
		name      string
		data      map[string][]byte
		expectErr bool
	}{
		{
			name: "valid service account keys",
			data: map[string][]byte{
				"serviceAccountID":     []byte("my-sa"),
				"serviceAccountSecret": []byte("base64-jwk-data"),
			},
		},
		{
			name: "with optional organization",
			data: map[string][]byte{
				"serviceAccountID":     []byte("my-sa"),
				"serviceAccountSecret": []byte("base64-jwk-data"),
				"organization":         []byte("my-org"),
			},
		},
		{
			name: "missing service account ID",
			data: map[string][]byte{
				"serviceAccountSecret": []byte("base64-jwk-data"),
			},
			expectErr: true,
		},
		{
			name: "missing service account secret",
			data: map[string][]byte{
				"serviceAccountID": []byte("my-sa"),
			},
			expectErr: true,
		},
		{
			name:      "empty data",
			data:      map[string][]byte{},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := configFromServiceAccountKeys(tt.data, clusterCtx)
			if tt.expectErr {
				assert.Error(t, err)
				assert.Nil(t, cfg)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, cfg)
			}
		})
	}
}

func TestConfigFromServiceAccountKeys_Values(t *testing.T) {
	clusterCtx := ClusterContext{Project: "spec-project", Region: "se-sto"}
	data := map[string][]byte{
		"serviceAccountID":     []byte("my-sa"),
		"serviceAccountSecret": []byte("base64-jwk-data"),
		"organization":         []byte("my-org"),
	}

	cfg, err := configFromServiceAccountKeys(data, clusterCtx)
	assert.NoError(t, err)
	assert.Equal(t, "my-sa", cfg.Auth.ServiceAccountID)
	assert.Equal(t, "base64-jwk-data", cfg.Auth.ServiceAccountSecret)
	assert.Equal(t, "spec-project", cfg.Context.Project)
	assert.Equal(t, "se-sto", cfg.Context.Region)
	assert.Equal(t, "my-org", cfg.Context.Organization)
}

func TestConfigFromServiceAccountKeys_IgnoresLegacyProjectRegion(t *testing.T) {
	clusterCtx := ClusterContext{Project: "spec-project", Region: "se-sto"}
	data := map[string][]byte{
		"serviceAccountID":     []byte("my-sa"),
		"serviceAccountSecret": []byte("base64-jwk-data"),
		"project":              []byte("secret-project-ignored"),
		"region":               []byte("secret-region-ignored"),
	}

	cfg, err := configFromServiceAccountKeys(data, clusterCtx)
	assert.NoError(t, err)
	assert.Equal(t, "spec-project", cfg.Context.Project)
	assert.Equal(t, "se-sto", cfg.Context.Region)
}

// Endpoint overrides from the cluster spec must reach the SDK config.
func TestConfigFromServiceAccountKeys_EndpointOverrides(t *testing.T) {
	clusterCtx := ClusterContext{
		Project:      "spec-project",
		Region:       "se-sto",
		APIBaseURL:   "https://api.private.example.com",
		AuthTokenURL: "https://authn.private.example.com/realms/r/protocol/openid-connect/token",
		ClientID:     "custom-client",
	}
	data := map[string][]byte{
		"serviceAccountID":     []byte("my-sa"),
		"serviceAccountSecret": []byte("base64-jwk-data"),
	}

	cfg, err := configFromServiceAccountKeys(data, clusterCtx)
	assert.NoError(t, err)
	assert.Equal(t, "https://api.private.example.com", cfg.API.BaseURL)
	assert.Equal(t, "https://authn.private.example.com/realms/r/protocol/openid-connect/token", cfg.Auth.TokenURL)
	// An explicit clientID survives SetDefaults' <serviceAccountID>_<project> derivation.
	assert.Equal(t, "custom-client", cfg.Auth.ClientID)
}

// Without overrides, SetDefaults must still fill in the public evroc cloud
// endpoints and derive the client ID as before.
func TestConfigFromServiceAccountKeys_DefaultEndpoints(t *testing.T) {
	clusterCtx := ClusterContext{Project: "spec-project", Region: "se-sto"}
	data := map[string][]byte{
		"serviceAccountID":     []byte("my-sa"),
		"serviceAccountSecret": []byte("base64-jwk-data"),
	}

	cfg, err := configFromServiceAccountKeys(data, clusterCtx)
	assert.NoError(t, err)
	assert.NotEmpty(t, cfg.API.BaseURL)
	assert.Contains(t, cfg.Auth.TokenURL, "evroc.com")
	assert.Equal(t, "my-sa_spec-project", cfg.Auth.ClientID)
}

// credentialsRef is mandatory: an empty secret name has no credential source.
func TestClientForCluster_NoSecret(t *testing.T) {
	client, err := ClientForCluster(context.Background(), nil, "", "", ClusterContext{}, nil)
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "no credentialsRef")
}

func TestClientForCluster_LegacyConfigYAMLRejected(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "default"},
		Data:       map[string][]byte{"config.yaml": []byte("auth:\n  service_account_id: sa\n")},
	}).Build()

	client, err := ClientForCluster(context.Background(), k8sClient, "creds", "default", ClusterContext{Project: "p"}, nil)
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "removed config.yaml format")
	assert.Contains(t, err.Error(), "serviceAccountID")
}
