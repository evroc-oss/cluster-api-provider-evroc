// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"testing"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/stretchr/testify/assert"
)

func TestConfigFromFlatKeys(t *testing.T) {
	tests := []struct {
		name      string
		data      map[string][]byte
		expectOK  bool
		expectCfg bool
	}{
		{
			name: "token auth",
			data: map[string][]byte{
				"evroccredentialConfig-token":   []byte("my-token"),
				"evroccredentialConfig-project": []byte("my-project"),
				"evroccredentialConfig-region":  []byte("se-sto"),
			},
			expectOK:  true,
			expectCfg: true,
		},
		{
			name: "refresh token auth",
			data: map[string][]byte{
				"evroccredentialConfig-refreshToken": []byte("my-refresh"),
				"evroccredentialConfig-project":      []byte("my-project"),
			},
			expectOK:  true,
			expectCfg: true,
		},
		{
			name: "password auth",
			data: map[string][]byte{
				"evroccredentialConfig-username": []byte("user"),
				"evroccredentialConfig-password": []byte("pass"),
				"evroccredentialConfig-project":  []byte("my-project"),
			},
			expectOK:  true,
			expectCfg: true,
		},
		{
			name: "case insensitive keys",
			data: map[string][]byte{
				"evroccredentialconfig-token":   []byte("my-token"),
				"evroccredentialconfig-project": []byte("my-project"),
			},
			expectOK:  true,
			expectCfg: true,
		},
		{
			name: "no auth keys returns false",
			data: map[string][]byte{
				"evroccredentialConfig-project": []byte("my-project"),
				"evroccredentialConfig-region":  []byte("se-sto"),
			},
			expectOK: false,
		},
		{
			name:     "empty data returns false",
			data:     map[string][]byte{},
			expectOK: false,
		},
		{
			name: "password without username returns false",
			data: map[string][]byte{
				"evroccredentialConfig-password": []byte("pass"),
			},
			expectOK: false,
		},
		{
			name: "includes organization",
			data: map[string][]byte{
				"evroccredentialConfig-token":        []byte("my-token"),
				"evroccredentialConfig-organization": []byte("my-org"),
			},
			expectOK:  true,
			expectCfg: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, ok := configFromFlatKeys(tt.data)
			assert.Equal(t, tt.expectOK, ok)
			if tt.expectCfg {
				assert.NotNil(t, cfg)
			}
		})
	}
}

func TestConfigFromFlatKeys_Values(t *testing.T) {
	data := map[string][]byte{
		"evroccredentialConfig-token":        []byte("my-token"),
		"evroccredentialConfig-refreshToken": []byte("my-refresh"),
		"evroccredentialConfig-project":      []byte("my-project"),
		"evroccredentialConfig-region":       []byte("se-sto"),
		"evroccredentialConfig-organization": []byte("my-org"),
	}

	cfg, ok := configFromFlatKeys(data)
	assert.True(t, ok)
	assert.Equal(t, "my-token", cfg.Auth.Token)
	assert.Equal(t, "my-refresh", cfg.Auth.RefreshToken)
	assert.Equal(t, "my-project", cfg.Context.Project)
	assert.Equal(t, "se-sto", cfg.Context.Region)
	assert.Equal(t, "my-org", cfg.Context.Organization)
}

func TestClientForCluster_NoSecret_WithFallback(t *testing.T) {
	// When no secret name is provided and a fallback exists, return fallback
	fallback := &mockClientInterface{}
	client, err := ClientForCluster(context.Background(), nil, fallback, "", "", nil)
	assert.NoError(t, err)
	assert.Equal(t, fallback, client)
}

func TestClientForCluster_NoSecret_NoFallback(t *testing.T) {
	// When no secret name and no fallback, return error
	client, err := ClientForCluster(context.Background(), nil, nil, "", "", nil)
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "no credentialsRef")
}

// mockClientInterface is a minimal mock for testing ClientForCluster fallback logic
type mockClientInterface struct{}

func (m *mockClientInterface) Disks() DiskServiceInterface                     { return nil }
func (m *mockClientInterface) PublicIPs() PublicIPServiceInterface             { return nil }
func (m *mockClientInterface) SecurityGroups() SecurityGroupServiceInterface   { return nil }
func (m *mockClientInterface) PlacementGroups() PlacementGroupServiceInterface { return nil }
func (m *mockClientInterface) VirtualMachines() VirtualMachineServiceInterface { return nil }
func (m *mockClientInterface) SDKClient() *evroc.Client                        { return nil }
