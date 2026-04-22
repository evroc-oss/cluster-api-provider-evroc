// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"context"
	"errors"

	evroc "github.com/evroc-oss/evroc-go-sdk"
)

// GetOrCreate attempts to get a cloud resource, and creates it if not found.
// This eliminates the Exists() + Get() double API call pattern.
//
// The get function should return (resource, error).
// The create function should return (resource, error).
//
// If get returns a "not found" error, create is called.
// Otherwise, the resource from get is returned.
func GetOrCreate[T any](
	ctx context.Context,
	get func(context.Context) (*T, error),
	create func(context.Context) (*T, error),
) (*T, bool, error) {
	// Try to get the resource first
	resource, err := get(ctx)
	if err != nil {
		// Check if it's a not found error
		if IsNotFoundError(err) {
			// Resource doesn't exist, create it
			created, createErr := create(ctx)
			if createErr != nil {
				return nil, false, createErr
			}
			return created, true, nil
		}
		// Real error (network, auth, etc.)
		return nil, false, err
	}

	// Resource exists
	return resource, false, nil
}

// IsNotFoundError checks if an error indicates a resource was not found.
// Uses the SDK's typed ErrNotFound error for reliable detection.
func IsNotFoundError(err error) bool {
	return errors.Is(err, evroc.ErrNotFound)
}

// IsTransientError checks if an error is likely transient and should be retried.
// Uses SDK's typed errors where available, falls back to context errors.
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Check for context timeout/cancellation (not worth retrying)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}

	// SDK doesn't currently export specific transient error types,
	// but the SDK client already handles retries internally with exponential backoff.
	// If we get here, the SDK already retried and gave up, so we should respect that.
	return false
}
