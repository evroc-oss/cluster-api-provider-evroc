// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// EnsureFinalizer adds a finalizer to an object if not already present.
// Returns true if the object was updated and needs to requeue.
func EnsureFinalizer(ctx context.Context, c client.Client, obj client.Object, finalizer string) (bool, error) {
	if !controllerutil.ContainsFinalizer(obj, finalizer) {
		controllerutil.AddFinalizer(obj, finalizer)
		if err := c.Update(ctx, obj); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// RemoveFinalizer removes a finalizer from an object if present.
func RemoveFinalizer(ctx context.Context, c client.Client, obj client.Object, finalizer string) error {
	if controllerutil.ContainsFinalizer(obj, finalizer) {
		controllerutil.RemoveFinalizer(obj, finalizer)
		if err := c.Update(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}

// HandleDeletion checks if an object is being deleted and returns true if deletion is in progress.
// If the object is being deleted but still has the finalizer, it calls the cleanup function
// and removes the finalizer if cleanup succeeds.
func HandleDeletion(
	ctx context.Context,
	c client.Client,
	obj client.Object,
	finalizer string,
	cleanup func(ctx context.Context) error,
) (bool, error) {
	if obj.GetDeletionTimestamp().IsZero() {
		return false, nil
	}

	if !controllerutil.ContainsFinalizer(obj, finalizer) {
		return true, nil
	}

	// Run cleanup function
	if err := cleanup(ctx); err != nil {
		return true, err
	}

	// Remove finalizer
	if err := RemoveFinalizer(ctx, c, obj, finalizer); err != nil {
		return true, err
	}

	return true, nil
}
