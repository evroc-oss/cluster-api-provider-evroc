// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package mocks

import (
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/stretchr/testify/mock"
)

// MockClient is a mock implementation of cloud.ClientInterface.
type MockClient struct {
	mock.Mock
}

func (m *MockClient) Disks() cloud.DiskServiceInterface {
	args := m.Called()
	return args.Get(0).(cloud.DiskServiceInterface)
}

func (m *MockClient) PublicIPs() cloud.PublicIPServiceInterface {
	args := m.Called()
	return args.Get(0).(cloud.PublicIPServiceInterface)
}

func (m *MockClient) SecurityGroups() cloud.SecurityGroupServiceInterface {
	args := m.Called()
	return args.Get(0).(cloud.SecurityGroupServiceInterface)
}

func (m *MockClient) PlacementGroups() cloud.PlacementGroupServiceInterface {
	args := m.Called()
	return args.Get(0).(cloud.PlacementGroupServiceInterface)
}

func (m *MockClient) VirtualMachines() cloud.VirtualMachineServiceInterface {
	args := m.Called()
	return args.Get(0).(cloud.VirtualMachineServiceInterface)
}

func (m *MockClient) SDKClient() *evroc.Client {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*evroc.Client)
}
