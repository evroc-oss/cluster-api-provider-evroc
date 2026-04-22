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

// MockVirtualMachineService is a mock implementation of VirtualMachineServiceInterface.
type MockVirtualMachineService struct {
	mock.Mock
}

func (m *MockVirtualMachineService) Create(ctx context.Context, request *computetypes.VirtualMachineRequest) (*computetypes.VirtualMachine, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*computetypes.VirtualMachine), args.Error(1)
}

func (m *MockVirtualMachineService) Get(ctx context.Context, name string) (*computetypes.VirtualMachine, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*computetypes.VirtualMachine), args.Error(1)
}

func (m *MockVirtualMachineService) Delete(ctx context.Context, name string) error {
	args := m.Called(ctx, name)
	return args.Error(0)
}

func (m *MockVirtualMachineService) List(ctx context.Context) ([]computetypes.VirtualMachine, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]computetypes.VirtualMachine), args.Error(1)
}

func (m *MockVirtualMachineService) Exists(ctx context.Context, name string) (bool, error) {
	args := m.Called(ctx, name)
	return args.Bool(0), args.Error(1)
}

func (m *MockVirtualMachineService) WaitForReady(ctx context.Context, name string, timeout time.Duration, opts ...compute.WaiterOption) (*computetypes.VirtualMachine, error) {
	args := m.Called(ctx, name, timeout, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*computetypes.VirtualMachine), args.Error(1)
}

func (m *MockVirtualMachineService) WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error {
	args := m.Called(ctx, name, timeout)
	return args.Error(0)
}

func (m *MockVirtualMachineService) UpdateSecurityGroups(ctx context.Context, vmName string, securityGroupNames []string) error {
	args := m.Called(ctx, vmName, securityGroupNames)
	return args.Error(0)
}

func (m *MockVirtualMachineService) UpdatePublicIP(ctx context.Context, vmName string, publicIPName string) error {
	args := m.Called(ctx, vmName, publicIPName)
	return args.Error(0)
}

func (m *MockVirtualMachineService) UpdateDisks(ctx context.Context, vmName string, diskNames []string) error {
	args := m.Called(ctx, vmName, diskNames)
	return args.Error(0)
}

func (m *MockVirtualMachineService) UpdatePlacement(ctx context.Context, vmName string, placement computetypes.VirtualMachineSpecPlacement) error {
	args := m.Called(ctx, vmName, placement)
	return args.Error(0)
}
