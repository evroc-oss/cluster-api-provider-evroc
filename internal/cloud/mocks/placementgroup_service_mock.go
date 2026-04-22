// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package mocks

import (
	"context"
	"time"

	"github.com/evroc-oss/evroc-go-sdk/compute"
	computetypes "github.com/evroc-oss/evroc-go-sdk/types/compute"
	"github.com/stretchr/testify/mock"
)

// MockPlacementGroupService is a mock implementation of PlacementGroupServiceInterface.
type MockPlacementGroupService struct {
	mock.Mock
}

func (m *MockPlacementGroupService) Create(ctx context.Context, name string, strategy string, zone string, labels map[string]string) (*computetypes.PlacementGroup, error) {
	args := m.Called(ctx, name, strategy, zone, labels)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*computetypes.PlacementGroup), args.Error(1)
}

func (m *MockPlacementGroupService) Get(ctx context.Context, name string) (*computetypes.PlacementGroup, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*computetypes.PlacementGroup), args.Error(1)
}

func (m *MockPlacementGroupService) Delete(ctx context.Context, name string) error {
	args := m.Called(ctx, name)
	return args.Error(0)
}

func (m *MockPlacementGroupService) List(ctx context.Context) ([]computetypes.PlacementGroup, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]computetypes.PlacementGroup), args.Error(1)
}

func (m *MockPlacementGroupService) Exists(ctx context.Context, name string) (bool, error) {
	args := m.Called(ctx, name)
	return args.Bool(0), args.Error(1)
}

func (m *MockPlacementGroupService) WaitForReady(ctx context.Context, name string, timeout time.Duration, opts ...compute.WaiterOption) (*computetypes.PlacementGroup, error) {
	args := m.Called(ctx, name, timeout, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*computetypes.PlacementGroup), args.Error(1)
}
