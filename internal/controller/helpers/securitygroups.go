// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
)

// ConvertSecurityGroupRulesToSDK converts API security group rules to SDK format.
// This conversion is used by both cluster and machine controllers when creating
// or updating inline security groups.
func ConvertSecurityGroupRulesToSDK(rules []infrav1.SecurityGroupRule) []networkingtypes.SecurityGroupSpecRulesItem {
	sdkRules := make([]networkingtypes.SecurityGroupSpecRulesItem, len(rules))

	for i, rule := range rules {
		// Direction is required enum
		direction := networkingtypes.SecurityGroupSpecRulesItemDirection(rule.Direction)

		sdkRule := networkingtypes.SecurityGroupSpecRulesItem{
			Name:      &rule.Name,
			Direction: direction,
		}

		// Protocol is optional enum
		if rule.Protocol != "" {
			protocol := networkingtypes.SecurityGroupSpecRulesItemProtocol(rule.Protocol)
			sdkRule.Protocol = &protocol
		}

		// Port range
		if rule.Port != nil {
			sdkRule.Port = rule.Port
		}
		if rule.EndPort != nil {
			sdkRule.EndPort = rule.EndPort
		}

		// Remote CIDR or SecurityGroup
		if rule.RemoteCIDR != "" {
			sdkRule.Remote.Address = &networkingtypes.SecurityGroupSpecRulesItemAddress{
				IpAddressOrCIDR: rule.RemoteCIDR,
			}
		}
		if rule.RemoteSecurityGroup != "" {
			sdkRule.Remote.SecurityGroupRef = &rule.RemoteSecurityGroup
		}

		sdkRules[i] = sdkRule
	}

	return sdkRules
}
