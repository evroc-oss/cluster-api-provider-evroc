// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"fmt"
	"time"

	"github.com/evroc-oss/evroc-go-sdk/compute"
	computetypes "github.com/evroc-oss/evroc-go-sdk/types/compute"
)

// Create creates a new placement group.
func (pgs *PlacementGroupService) Create(
	ctx context.Context,
	name string,
	strategy string,
	zone string,
	labels map[string]string,
) (*computetypes.PlacementGroup, error) {
	builder := compute.NewPlacementGroupBuilder(name, strategy).
		WithZone(zone)
	if len(labels) > 0 {
		builder = builder.WithLabels(labels)
	}
	request := builder.Build()

	created, err := pgs.client.client.Compute().PlacementGroups().Create(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to create placement group: %w", err)
	}

	return created, nil
}

// Get retrieves a placement group by name.
func (pgs *PlacementGroupService) Get(ctx context.Context, name string) (*computetypes.PlacementGroup, error) {
	pg, err := pgs.client.client.Compute().PlacementGroups().Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get placement group: %w", err)
	}

	return pg, nil
}

// Delete deletes a placement group.
func (pgs *PlacementGroupService) Delete(ctx context.Context, name string) error {
	if err := pgs.client.client.Compute().PlacementGroups().Delete(ctx, name); err != nil {
		return fmt.Errorf("failed to delete placement group: %w", err)
	}

	return nil
}

// List lists all placement groups.
func (pgs *PlacementGroupService) List(ctx context.Context) ([]computetypes.PlacementGroup, error) {
	response, err := pgs.client.client.Compute().PlacementGroups().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list placement groups: %w", err)
	}

	return response.Items, nil
}

// Exists checks if a placement group exists.
func (pgs *PlacementGroupService) Exists(ctx context.Context, name string) (bool, error) {
	_, err := pgs.Get(ctx, name)
	if err != nil {
		// Check if it's a not found error.
		if isNotFoundError(err) {
			return false, nil
		}
		// Return other errors (network, auth, etc.) to caller.
		return false, fmt.Errorf("failed to check placement group existence: %w", err)
	}

	return true, nil
}

// WaitForReady waits for a placement group to reach ready state.
func (pgs *PlacementGroupService) WaitForReady(
	ctx context.Context,
	name string,
	timeout time.Duration,
	opts ...compute.WaiterOption,
) (*computetypes.PlacementGroup, error) {
	return pgs.client.client.Compute().PlacementGroups().WaitForReady(ctx, name, timeout, opts...)
}
