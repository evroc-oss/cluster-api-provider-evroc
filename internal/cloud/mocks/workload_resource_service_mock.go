// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockWorkloadResourceService is a mock implementation of cloud.WorkloadResourceServiceInterface.
type MockWorkloadResourceService struct {
	mock.Mock
}

func (m *MockWorkloadResourceService) Cleanup(ctx context.Context, clusterID string, deleteDisks bool) (bool, error) {
	args := m.Called(ctx, clusterID, deleteDisks)
	return args.Bool(0), args.Error(1)
}
