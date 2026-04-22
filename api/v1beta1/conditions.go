// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

// Conditions and condition Reasons for the EvrocCluster object.

const (
	// ClusterReadyCondition reports on the overall readiness of the cluster
	ClusterReadyCondition clusterv1.ConditionType = "Ready"

	// ClusterReconciliationFailedReason is used when cluster reconciliation fails
	ClusterReconciliationFailedReason = "ReconciliationFailed"

	// WaitingForControlPlaneEndpointReason is used when waiting for control plane endpoint
	WaitingForControlPlaneEndpointReason = "WaitingForControlPlaneEndpoint"
)
