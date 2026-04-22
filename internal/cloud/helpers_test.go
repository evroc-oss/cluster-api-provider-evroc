// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"testing"

	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
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

func TestBuildSecurityGroupCreateRequest(t *testing.T) {
	tcp := networkingtypes.SecurityGroupSpecRulesItemProtocolTCP
	tests := []struct {
		name   string
		sgName string
		rules  []networkingtypes.SecurityGroupSpecRulesItem
	}{
		{
			name:   "security group with no rules",
			sgName: "test-sg",
			rules:  []networkingtypes.SecurityGroupSpecRulesItem{},
		},
		{
			name:   "security group with SSH rule",
			sgName: "test-sg-ssh",
			rules: []networkingtypes.SecurityGroupSpecRulesItem{
				{
					Direction: networkingtypes.SecurityGroupSpecRulesItemDirectionIngress,
					Protocol:  &tcp,
					Port:      int32Ptr(22),
					Remote: struct {
						Address          *networkingtypes.SecurityGroupSpecRulesItemAddress `json:"address,omitempty"`
						SecurityGroupRef *string                                            `json:"securityGroupRef,omitempty"`
						SubnetRef        *string                                            `json:"subnetRef,omitempty"`
					}{
						Address: &networkingtypes.SecurityGroupSpecRulesItemAddress{
							IpAddressOrCIDR: "0.0.0.0/0",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildSecurityGroupCreateRequest(tt.sgName, tt.rules)

			// Verify SDK builder sets correct API version (networking/v1beta1)
			assert.Contains(t, result.ApiVersion, "networking/")
			assert.Equal(t, "SecurityGroup", result.Kind)
			assert.Equal(t, tt.sgName, result.Metadata.Id)

			// Verify rules are set correctly
			if len(tt.rules) > 0 {
				assert.NotNil(t, result.Spec.Rules)
				assert.Equal(t, len(tt.rules), len(*result.Spec.Rules))
			} else {
				// Empty rules should still be set as empty slice
				assert.NotNil(t, result.Spec.Rules)
				assert.Equal(t, 0, len(*result.Spec.Rules))
			}
		})
	}
}

// Helper functions for test.
func int32Ptr(i int32) *int32 {
	return &i
}
