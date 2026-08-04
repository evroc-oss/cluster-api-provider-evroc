// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"strings"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
)

// MergeLabels merges cluster-level and machine-level labels.
// Machine labels take precedence over cluster labels when there are conflicts.
// Automatically adds CAPI standard labels for cluster name and role identification.
func MergeLabels(cluster *infrav1.EvrocCluster, machine *infrav1.EvrocMachine, role string) map[string]string {
	labels := make(map[string]string)

	// Start with cluster-level labels (if any)
	if cluster != nil && cluster.Spec.AdditionalLabels != nil {
		for k, v := range cluster.Spec.AdditionalLabels {
			labels[k] = v
		}
	}

	// Merge machine-level labels (overrides cluster labels if conflict)
	if machine != nil && machine.Spec.AdditionalLabels != nil {
		for k, v := range machine.Spec.AdditionalLabels {
			labels[k] = v
		}
	}

	// Add automatic CAPI labels for resource tracking.
	// evroc does not allow "/" in label keys, so we use "_" as separator.
	if cluster != nil {
		labels["capi_cluster-name"] = cluster.Name
	}

	if role != "" {
		labels["capi_role"] = role
	}

	// Add provider label for identification
	labels["capi_provider"] = "evroc"

	return labels
}

// ResourceLabels returns labels for cloud resources (public IPs, security groups, disks,
// placement groups). It merges user-provided additionalLabels with automatic ownership
// labels. Ownership labels (capi_*) always take precedence and cannot be overridden.
//
// The additionalLabels maps are applied in order — later maps override earlier ones,
// following the same precedence as Terraform's merge: cluster-level first, then
// machine-level. This lets users set labels like "department: analytics" at the cluster
// level and have them trickle down to every cloud resource.
//
// evroc does not allow "/" in label keys, so we use "_" as separator.
// clusterID is the immutable annotation-backed ownership ID that survives
// clusterctl move.
func ResourceLabels(clusterName, clusterID string, additionalLabels ...map[string]string) map[string]string {
	labels := make(map[string]string)

	// Apply user-provided labels in order (cluster first, machine second)
	for _, extra := range additionalLabels {
		for k, v := range extra {
			labels[k] = v
		}
	}

	// Ownership labels always win — users cannot override these
	labels[cloud.LabelClusterName] = clusterName
	labels[cloud.LabelClusterID] = clusterID
	labels[cloud.LabelManagedBy] = cloud.ManagedByValue

	return labels
}

// ValidateLabelKey checks if a label key is valid for evroc cloud resources.
// Returns true if valid, false otherwise.
// evroc does not allow "/" in label keys, so keys must use "_" as separator.
func ValidateLabelKey(key string) bool {
	if len(key) == 0 || len(key) > 253 {
		return false
	}
	if strings.Contains(key, "/") {
		return false
	}
	return true
}

// ValidateLabelValue checks if a label value is valid.
// Returns true if valid, false otherwise.
func ValidateLabelValue(value string) bool {
	// Label values must be 63 characters or less
	// Can be empty
	// Must begin and end with alphanumeric (if not empty)
	if len(value) > 63 {
		return false
	}
	return true
}
