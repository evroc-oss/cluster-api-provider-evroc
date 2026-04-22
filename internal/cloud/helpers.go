// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"github.com/evroc-oss/evroc-go-sdk/networking"
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
)

// BuildPublicIPCreateRequest creates a properly formatted PublicIP request for the evroc API.
func BuildPublicIPCreateRequest(name string) *networkingtypes.PublicIPRequest {
	// Use SDK builder to create request with correct API version
	return networking.NewPublicIPBuilder(name).Build()
}

// BuildSecurityGroupCreateRequest creates a SecurityGroupRequest for creating a security group.
func BuildSecurityGroupCreateRequest(
	name string,
	rules []networkingtypes.SecurityGroupSpecRulesItem,
) *networkingtypes.SecurityGroupRequest {
	// Use SDK builder to create request with correct API version
	builder := networking.NewSecurityGroupBuilder(name)

	// Build the request and set rules directly
	// The builder's Build() method handles rules passed through its rule methods,
	// but we need to set them directly when passing pre-constructed rules
	request := builder.Build()
	request.Spec.Rules = &rules

	return request
}
