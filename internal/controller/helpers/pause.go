// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"context"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IsPaused returns true if the object has the cluster.x-k8s.io/paused annotation
// or the owning CAPI Cluster has Spec.Paused set to true.
//
// Per the CAPI provider contract, infrastructure providers SHOULD check both:
//   - the paused annotation on the infra object itself
//   - Spec.Paused on the owning Cluster object
//
// During clusterctl move, CAPI sets Cluster.Spec.Paused=true and propagates the
// paused annotation to all owned objects. Respecting this prevents controllers
// from deleting real infrastructure when objects are removed from the source
// management cluster.
func IsPaused(ctx context.Context, c client.Client, obj client.Object) bool {
	// Check the paused annotation on the object itself.
	if HasPausedAnnotation(obj) {
		return true
	}

	// Check the owning CAPI Cluster's Spec.Paused field.
	clusterName, ok := obj.GetLabels()[clusterv1.ClusterNameLabel]
	if !ok || clusterName == "" {
		return false
	}

	cluster := &clusterv1.Cluster{}
	if err := c.Get(ctx, client.ObjectKey{
		Namespace: obj.GetNamespace(),
		Name:      clusterName,
	}, cluster); err != nil {
		// If the cluster is not found (e.g., already deleted), not paused.
		return false
	}

	return cluster.Spec.Paused != nil && *cluster.Spec.Paused
}

// HasPausedAnnotation returns true if the object has the cluster.x-k8s.io/paused
// annotation set (any non-empty value).
func HasPausedAnnotation(obj client.Object) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	_, exists := annotations[clusterv1.PausedAnnotation]
	return exists
}
