// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package mocks

import (
	"context"

	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
	"github.com/stretchr/testify/mock"
)

// MockSubnetService is a mock implementation of SubnetServiceInterface.
type MockSubnetService struct {
	mock.Mock
}

func (m *MockSubnetService) Get(ctx context.Context, name string) (*networkingtypes.Subnet, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*networkingtypes.Subnet), args.Error(1)
}
