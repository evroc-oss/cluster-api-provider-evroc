// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"context"
	"errors"
	"testing"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOrCreate(t *testing.T) {
	type testResource struct {
		ID string
	}

	tests := []struct {
		name          string
		getError      error
		createError   error
		expectCreated bool
		expectError   bool
	}{
		{
			name:          "resource exists - returns existing",
			getError:      nil,
			expectCreated: false,
			expectError:   false,
		},
		{
			name:          "resource not found - creates new",
			getError:      evroc.ErrNotFound,
			expectCreated: true,
			expectError:   false,
		},
		{
			name:          "resource not found - create fails",
			getError:      evroc.ErrNotFound,
			createError:   errors.New("create failed"),
			expectCreated: false,
			expectError:   true,
		},
		{
			name:          "get returns real error - returns error",
			getError:      errors.New("connection refused"),
			expectCreated: false,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getCallCount := 0
			createCallCount := 0

			get := func(ctx context.Context) (*testResource, error) {
				getCallCount++
				if tt.getError != nil {
					return nil, tt.getError
				}
				return &testResource{ID: "existing"}, nil
			}

			create := func(ctx context.Context) (*testResource, error) {
				createCallCount++
				if tt.createError != nil {
					return nil, tt.createError
				}
				return &testResource{ID: "created"}, nil
			}

			ctx := context.Background()
			resource, created, err := GetOrCreate(ctx, get, create)

			assert.Equal(t, 1, getCallCount, "get should be called once")

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, resource)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resource)
				assert.Equal(t, tt.expectCreated, created)

				if tt.expectCreated {
					assert.Equal(t, 1, createCallCount, "create should be called once")
					assert.Equal(t, "created", resource.ID)
				} else {
					assert.Equal(t, 0, createCallCount, "create should not be called")
					assert.Equal(t, "existing", resource.ID)
				}
			}
		})
	}
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect bool
	}{
		{
			name:   "nil error",
			err:    nil,
			expect: false,
		},
		{
			name:   "SDK ErrNotFound",
			err:    evroc.ErrNotFound,
			expect: true,
		},
		{
			name:   "wrapped ErrNotFound",
			err:    errors.Join(evroc.ErrNotFound, errors.New("additional context")),
			expect: true,
		},
		{
			name:   "other error",
			err:    errors.New("connection refused"),
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNotFoundError(tt.err)
			assert.Equal(t, tt.expect, result)
		})
	}
}

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect bool
	}{
		{
			name:   "nil error",
			err:    nil,
			expect: false,
		},
		{
			name:   "context deadline exceeded",
			err:    context.DeadlineExceeded,
			expect: false,
		},
		{
			name:   "context canceled",
			err:    context.Canceled,
			expect: false,
		},
		{
			name:   "permanent error",
			err:    errors.New("invalid credentials"),
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTransientError(tt.err)
			assert.Equal(t, tt.expect, result)
		})
	}
}
