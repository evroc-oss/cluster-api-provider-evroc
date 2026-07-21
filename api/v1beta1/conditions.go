// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

// Conditions and condition Reasons for the EvrocCluster object.

const (
	// ClusterReadyCondition reports on the overall readiness of the cluster
	ClusterReadyCondition clusterv1.ConditionType = "Ready"

	// PausedCondition is set when reconciliation is paused (e.g. during clusterctl move).
	PausedCondition clusterv1.ConditionType = "Paused"

	// ClusterReconciliationFailedReason is used when cluster reconciliation fails
	ClusterReconciliationFailedReason = "ReconciliationFailed"

	// CredentialsNotFoundReason is used when the secret named by credentialsRef
	// cannot be read. Surfaced as a condition so the cause is visible on the
	// object rather than only in controller logs.
	CredentialsNotFoundReason = "CredentialsNotFound"

	// WaitingForControlPlaneEndpointReason is used when waiting for control plane endpoint
	WaitingForControlPlaneEndpointReason = "WaitingForControlPlaneEndpoint"

	// PausedReason is used when the cluster or machine is paused.
	PausedReason = "Paused"

	// LoadBalancerReadyCondition reports on the readiness of the control plane load balancer.
	LoadBalancerReadyCondition clusterv1.ConditionType = "LoadBalancerReady"

	// LoadBalancerProvisioningReason is used when the LB is being created.
	LoadBalancerProvisioningReason = "Provisioning"

	// LoadBalancerNotReadyReason is used when the LB exists but is not yet active.
	LoadBalancerNotReadyReason = "NotReady"

	// LoadBalancerReadyReason is used when the LB is active and has an address.
	LoadBalancerReadyReason = "Ready"
)
