// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package mocks

import (
	"context"

	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
	"github.com/stretchr/testify/mock"
)

// MockVPCService is a mock implementation of VPCServiceInterface.
type MockVPCService struct {
	mock.Mock
}

func (m *MockVPCService) Create(ctx context.Context, name string, cidr string) (*networkingtypes.VirtualPrivateCloud, error) {
	args := m.Called(ctx, name, cidr)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*networkingtypes.VirtualPrivateCloud), args.Error(1)
}

func (m *MockVPCService) Get(ctx context.Context, name string) (*networkingtypes.VirtualPrivateCloud, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*networkingtypes.VirtualPrivateCloud), args.Error(1)
}

func (m *MockVPCService) Delete(ctx context.Context, name string) error {
	args := m.Called(ctx, name)
	return args.Error(0)
}

func (m *MockVPCService) List(ctx context.Context) ([]networkingtypes.VirtualPrivateCloud, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]networkingtypes.VirtualPrivateCloud), args.Error(1)
}

func (m *MockVPCService) Exists(ctx context.Context, name string) (bool, error) {
	args := m.Called(ctx, name)
	return args.Bool(0), args.Error(1)
}
