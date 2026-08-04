// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package mocks

import (
	"context"
	"time"

	"github.com/evroc-oss/evroc-go-sdk/networking"
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
	"github.com/stretchr/testify/mock"
)

// MockPublicIPService is a mock implementation of cloud.PublicIPServiceInterface.
type MockPublicIPService struct {
	mock.Mock
}

func (m *MockPublicIPService) Create(ctx context.Context, name string, labels map[string]string) (*networkingtypes.PublicIP, error) {
	args := m.Called(ctx, name, labels)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*networkingtypes.PublicIP), args.Error(1)
}

func (m *MockPublicIPService) Get(ctx context.Context, name string) (*networkingtypes.PublicIP, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*networkingtypes.PublicIP), args.Error(1)
}

func (m *MockPublicIPService) Delete(ctx context.Context, name string) error {
	args := m.Called(ctx, name)
	return args.Error(0)
}

func (m *MockPublicIPService) List(ctx context.Context) ([]networkingtypes.PublicIP, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]networkingtypes.PublicIP), args.Error(1)
}

func (m *MockPublicIPService) ListByOwner(ctx context.Context, machineID string) ([]string, error) {
	args := m.Called(ctx, machineID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockPublicIPService) Exists(ctx context.Context, name string) (bool, error) {
	args := m.Called(ctx, name)
	return args.Bool(0), args.Error(1)
}

func (m *MockPublicIPService) WaitForReady(ctx context.Context, name string, timeout time.Duration, opts ...networking.WaiterOption) (*networkingtypes.PublicIP, error) {
	args := m.Called(ctx, name, timeout, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*networkingtypes.PublicIP), args.Error(1)
}

func (m *MockPublicIPService) WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error {
	args := m.Called(ctx, name, timeout)
	return args.Error(0)
}
