// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"fmt"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
)

// SubnetService provides read access to subnets.
type SubnetService struct {
	client *evroc.Client
}

// Subnets returns the subnet service.
func (c *Client) Subnets() SubnetServiceInterface {
	return &SubnetService{client: c.client}
}

// Get retrieves a subnet by name.
func (ss *SubnetService) Get(ctx context.Context, name string) (*networkingtypes.Subnet, error) {
	subnet, err := ss.client.Networking().Subnets().Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get subnet %s: %w", name, err)
	}
	return subnet, nil
}
