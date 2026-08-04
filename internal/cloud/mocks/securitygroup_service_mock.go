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

// MockSecurityGroupService is a mock implementation of cloud.SecurityGroupServiceInterface.
type MockSecurityGroupService struct {
	mock.Mock
}

func (m *MockSecurityGroupService) Create(ctx context.Context, name string, rules []networkingtypes.SecurityGroupSpecRulesItem, labels map[string]string, vpcName string) (*networkingtypes.SecurityGroup, error) {
	args := m.Called(ctx, name, rules, labels, vpcName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*networkingtypes.SecurityGroup), args.Error(1)
}

func (m *MockSecurityGroupService) Get(ctx context.Context, name string) (*networkingtypes.SecurityGroup, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*networkingtypes.SecurityGroup), args.Error(1)
}

func (m *MockSecurityGroupService) Delete(ctx context.Context, name string) error {
	args := m.Called(ctx, name)
	return args.Error(0)
}

func (m *MockSecurityGroupService) List(ctx context.Context) ([]networkingtypes.SecurityGroup, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]networkingtypes.SecurityGroup), args.Error(1)
}

func (m *MockSecurityGroupService) ListByOwner(ctx context.Context, clusterID string) ([]string, error) {
	args := m.Called(ctx, clusterID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockSecurityGroupService) ListByMachineOwner(ctx context.Context, machineID string) ([]string, error) {
	args := m.Called(ctx, machineID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockSecurityGroupService) Update(ctx context.Context, name string, group *networkingtypes.SecurityGroup) (*networkingtypes.SecurityGroup, error) {
	args := m.Called(ctx, name, group)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*networkingtypes.SecurityGroup), args.Error(1)
}

func (m *MockSecurityGroupService) Exists(ctx context.Context, name string) (bool, error) {
	args := m.Called(ctx, name)
	return args.Bool(0), args.Error(1)
}

func (m *MockSecurityGroupService) WaitForReady(ctx context.Context, name string, timeout time.Duration, opts ...networking.WaiterOption) (*networkingtypes.SecurityGroup, error) {
	args := m.Called(ctx, name, timeout, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*networkingtypes.SecurityGroup), args.Error(1)
}

func (m *MockSecurityGroupService) WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error {
	args := m.Called(ctx, name, timeout)
	return args.Error(0)
}
