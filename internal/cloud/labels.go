// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
)

// Ownership label keys applied to every managed cloud resource. evroc does not
// allow "/" in label keys, so "_" is the separator.
//
// LabelClusterID and LabelMachineID hold immutable ownership IDs persisted in
// Kubernetes annotations. Unlike live Kubernetes UIDs, those annotations
// survive clusterctl move. Names are included only for human-readable tracking.
const (
	LabelClusterName = "capi_cluster-name"
	LabelClusterID   = infrav1.ClusterIDLabel
	LabelManagedBy   = "capi_managed-by"

	// Machine-level resources carry both the immutable ID and readable name.
	LabelMachineID   = "capi_machine-id"
	LabelMachineName = "capi_machine-name"

	// ManagedByValue is the value of LabelManagedBy.
	ManagedByValue = "cluster-api-provider-evroc"
)
