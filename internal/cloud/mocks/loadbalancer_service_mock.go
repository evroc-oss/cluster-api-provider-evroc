// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package mocks

import (
	"context"
	"time"

	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
	"github.com/stretchr/testify/mock"
)

// MockLoadBalancerService is a mock implementation of cloud.LoadBalancerServiceInterface.
type MockLoadBalancerService struct {
	mock.Mock
}

func (m *MockLoadBalancerService) Create(ctx context.Context, request *cloud.LoadBalancerCreateRequest) (*cloud.LoadBalancer, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cloud.LoadBalancer), args.Error(1)
}

func (m *MockLoadBalancerService) Get(ctx context.Context, name string) (*cloud.LoadBalancer, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cloud.LoadBalancer), args.Error(1)
}

func (m *MockLoadBalancerService) Delete(ctx context.Context, name string) error {
	args := m.Called(ctx, name)
	return args.Error(0)
}

func (m *MockLoadBalancerService) List(ctx context.Context) ([]cloud.LoadBalancer, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]cloud.LoadBalancer), args.Error(1)
}

func (m *MockLoadBalancerService) Exists(ctx context.Context, name string) (bool, error) {
	args := m.Called(ctx, name)
	return args.Bool(0), args.Error(1)
}

func (m *MockLoadBalancerService) AddBackend(ctx context.Context, lbName string, backend cloud.Backend) error {
	args := m.Called(ctx, lbName, backend)
	return args.Error(0)
}

func (m *MockLoadBalancerService) RemoveBackend(ctx context.Context, lbName string, backendName string) error {
	args := m.Called(ctx, lbName, backendName)
	return args.Error(0)
}

func (m *MockLoadBalancerService) ListBackends(ctx context.Context, lbName string) ([]cloud.Backend, error) {
	args := m.Called(ctx, lbName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]cloud.Backend), args.Error(1)
}

func (m *MockLoadBalancerService) WaitForReady(ctx context.Context, name string, timeout time.Duration) (*cloud.LoadBalancer, error) {
	args := m.Called(ctx, name, timeout)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cloud.LoadBalancer), args.Error(1)
}

func (m *MockLoadBalancerService) WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error {
	args := m.Called(ctx, name, timeout)
	return args.Error(0)
}
