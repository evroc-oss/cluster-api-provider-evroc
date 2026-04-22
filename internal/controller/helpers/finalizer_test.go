// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
)

func TestEnsureFinalizer(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)

	tests := []struct {
		name            string
		initialObject   *infrav1.EvrocMachine
		finalizer       string
		expectRequeue   bool
		expectFinalizer bool
	}{
		{
			name: "adds finalizer when not present",
			initialObject: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  "default",
					Finalizers: []string{},
				},
			},
			finalizer:       "test-finalizer",
			expectRequeue:   true,
			expectFinalizer: true,
		},
		{
			name: "skips when finalizer already present",
			initialObject: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  "default",
					Finalizers: []string{"test-finalizer"},
				},
			},
			finalizer:       "test-finalizer",
			expectRequeue:   false,
			expectFinalizer: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.initialObject).Build()
			ctx := context.Background()

			requeue, err := EnsureFinalizer(ctx, fakeClient, tt.initialObject, tt.finalizer)

			require.NoError(t, err)
			assert.Equal(t, tt.expectRequeue, requeue)
			assert.Equal(t, tt.expectFinalizer, controllerutil.ContainsFinalizer(tt.initialObject, tt.finalizer))
		})
	}
}

func TestRemoveFinalizer(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)

	tests := []struct {
		name            string
		initialObject   *infrav1.EvrocMachine
		finalizer       string
		expectFinalizer bool
	}{
		{
			name: "removes finalizer when present",
			initialObject: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  "default",
					Finalizers: []string{"test-finalizer"},
				},
			},
			finalizer:       "test-finalizer",
			expectFinalizer: false,
		},
		{
			name: "no-op when finalizer not present",
			initialObject: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  "default",
					Finalizers: []string{},
				},
			},
			finalizer:       "test-finalizer",
			expectFinalizer: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.initialObject).Build()
			ctx := context.Background()

			err := RemoveFinalizer(ctx, fakeClient, tt.initialObject, tt.finalizer)

			require.NoError(t, err)
			assert.Equal(t, tt.expectFinalizer, controllerutil.ContainsFinalizer(tt.initialObject, tt.finalizer))
		})
	}
}

func TestHandleDeletion(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)

	tests := []struct {
		name              string
		initialObject     *infrav1.EvrocMachine
		finalizer         string
		cleanupShouldFail bool
		expectDeleting    bool
		expectError       bool
	}{
		{
			name: "not being deleted",
			initialObject: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  "default",
					Finalizers: []string{"test-finalizer"},
				},
			},
			finalizer:      "test-finalizer",
			expectDeleting: false,
			expectError:    false,
		},
		{
			name: "being deleted with finalizer and successful cleanup",
			initialObject: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  "default",
					Finalizers: []string{"test-finalizer"},
					DeletionTimestamp: &metav1.Time{
						Time: metav1.Now().Time,
					},
				},
			},
			finalizer:         "test-finalizer",
			cleanupShouldFail: false,
			expectDeleting:    true,
			expectError:       false,
		},
		{
			name: "being deleted with finalizer and failed cleanup",
			initialObject: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  "default",
					Finalizers: []string{"test-finalizer"},
					DeletionTimestamp: &metav1.Time{
						Time: metav1.Now().Time,
					},
				},
			},
			finalizer:         "test-finalizer",
			cleanupShouldFail: true,
			expectDeleting:    true,
			expectError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.initialObject).Build()
			ctx := context.Background()

			cleanup := func(ctx context.Context) error {
				if tt.cleanupShouldFail {
					return assert.AnError
				}
				return nil
			}

			deleting, err := HandleDeletion(ctx, fakeClient, tt.initialObject, tt.finalizer, cleanup)

			assert.Equal(t, tt.expectDeleting, deleting)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
