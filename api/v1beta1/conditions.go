// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

// Conditions and condition Reasons for the EvrocCluster and EvrocMachine objects.

const (
	// ClusterReadyCondition reports on the overall readiness of the cluster
	ClusterReadyCondition clusterv1.ConditionType = "Ready"

	// ClusterReadyReason is used when the cluster infrastructure is provisioned.
	ClusterReadyReason = "ClusterReady"

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

	// QuotaExceededReason is used when a cloud API call is rejected because
	// an organization quota has been reached.
	QuotaExceededReason = "QuotaExceeded"

	// InsufficientPermissionsReason is used when a cloud API call is denied.
	InsufficientPermissionsReason = "InsufficientPermissions"

	// InvalidConfigurationReason is used when the cloud API rejects the
	// resource spec.
	InvalidConfigurationReason = "InvalidConfiguration"

	// MachineOperationFailedReason is the default reason for unclassified
	// machine reconciliation errors.
	MachineOperationFailedReason = "MachineOperationFailed"

	// MachineReadyCondition reports on the overall readiness of the machine.
	MachineReadyCondition clusterv1.ConditionType = "Ready"

	// MachineReadyReason is used when the machine's VM is running.
	MachineReadyReason = "MachineReady"

	// MachineNotReadyReason is used when the machine's VM is not yet ready.
	MachineNotReadyReason = "MachineNotReady"
)
