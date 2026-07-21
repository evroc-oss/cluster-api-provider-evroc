// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"github.com/evroc-oss/evroc-go-sdk/networking"
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
)

// BuildPublicIPCreateRequest creates a properly formatted PublicIP request for the evroc API.
func BuildPublicIPCreateRequest(name string) *networkingtypes.PublicIPRequest {
	return networking.NewPublicIPBuilder(name).Build()
}
