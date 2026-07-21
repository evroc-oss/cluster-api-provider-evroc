// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildPublicIPCreateRequest(t *testing.T) {
	tests := []struct {
		name   string
		ipName string
	}{
		{
			name:   "basic public IP request",
			ipName: "test-public-ip",
		},
		{
			name:   "public IP with UUID name",
			ipName: "550e8400-e29b-41d4-a716-446655440000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildPublicIPCreateRequest(tt.ipName)

			// Verify SDK builder sets correct API version (networking/v1beta1)
			assert.Contains(t, result.ApiVersion, "networking/")
			assert.Equal(t, "PublicIP", result.Kind)
			assert.Equal(t, tt.ipName, result.Metadata.Id)
		})
	}
}
