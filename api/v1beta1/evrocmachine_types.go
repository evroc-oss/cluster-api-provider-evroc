// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// EvrocMachineSpec defines the desired state of EvrocMachine
type EvrocMachineSpec struct {
	// Project is the evroc project ID
	Project string `json:"project"`

	// Region is the evroc region (e.g., se-sto)
	Region string `json:"region"`

	// ComputeProfile specifies the VM compute profile (e.g., "a1a.m" for 4 vCPU, 16GB RAM)
	ComputeProfile string `json:"computeProfile"`

	// Image is the OS image to use for the VM (e.g., "ubuntu.24-04.1")
	Image string `json:"image"`

	// SSHKey is the SSH public key for access
	// +optional
	SSHKey string `json:"sshKey,omitempty"`

	// UserData is cloud-init user data for bootstrap
	// +optional
	UserData string `json:"userData,omitempty"`

	// RootDiskSize is the size of the root disk in GB
	RootDiskSize int `json:"rootDiskSize"`

	// ProviderID is the unique identifier for the machine
	// +optional
	ProviderID *string `json:"providerID,omitempty"`

	// AdditionalLabels is an optional set of labels to add to this machine's Evroc resources.
	// These labels are merged with cluster-level AdditionalLabels (machine labels take precedence).
	// Useful for differentiating control plane vs worker nodes, or specific workload types.
	// Example: {"role": "worker", "workload-type": "cpu-intensive"}
	// +optional
	AdditionalLabels map[string]string `json:"additionalLabels,omitempty"`

	// NetworkingConfig defines networking configuration for this machine.
	// This is the simplified v1.1 approach - inline specs instead of separate CRDs.
	// When specified, the controller will auto-create and manage the necessary resources.
	// This is mutually compatible with v1 fields (SecurityGroupRefs, PublicIPRef) - inline config takes priority.
	// +optional
	NetworkingConfig *MachineNetworkingConfig `json:"networkingConfig,omitempty"`

	// PlacementConfig defines placement group configuration for this machine.
	// This is the simplified v1.1 approach - specify strategy inline or reference existing resources.
	// +optional
	PlacementConfig *PlacementConfig `json:"placementConfig,omitempty"`

	// AdditionalDisks defines additional disks to attach to this machine.
	// These disks are auto-created, auto-attached, and auto-deleted with the machine.
	// +optional
	AdditionalDisks []AdditionalDiskSpec `json:"additionalDisks,omitempty"`
}

// MachineNetworkingConfig defines networking configuration for a machine.
// Provides a simplified interface compared to explicit CRD references.
type MachineNetworkingConfig struct {
	// PublicIP configuration for this machine.
	// +optional
	PublicIP *PublicIPConfig `json:"publicIP,omitempty"`

	// SecurityGroups configuration for this machine.
	// +optional
	SecurityGroups *MachineSecurityGroupsConfig `json:"securityGroups,omitempty"`
}

// MachineSecurityGroupsConfig extends SecurityGroupsConfig with machine-specific options.
type MachineSecurityGroupsConfig struct {
	// InheritFromCluster inherits security groups from the cluster's securityGroups config.
	// When true, the machine receives the "common" section plus its role-specific section
	// (controlPlane for CP nodes, worker for workers) based on the machine's labels.
	// Additional inline/existing groups can still be specified.
	// Defaults to true if no inline or existing security groups are specified.
	// +optional
	InheritFromCluster bool `json:"inheritFromCluster,omitempty"`

	// InlineSecurityGroups defines security groups to auto-create with specified rules.
	// Each group will be named "<machine-name>-<group-name>" and tracked in machine.status.
	// +optional
	InlineSecurityGroups []InlineSecurityGroup `json:"inlineSecurityGroups,omitempty"`

	// ExistingNames references pre-existing security groups by name.
	// +optional
	ExistingNames []string `json:"existingNames,omitempty"`
}

// PlacementConfig defines placement group configuration.
// Placement groups must be pre-created and shared across VMs — CAPI does not
// auto-create them because a per-VM placement group has no anti-affinity benefit.
type PlacementConfig struct {
	// ExistingGroupName references a pre-existing placement group by name.
	// The group will be used but NOT managed (created/deleted) by CAPI.
	// +optional
	ExistingGroupName *string `json:"existingGroupName,omitempty"`
}

// AdditionalDiskSpec defines an additional disk to attach to a machine.
// These disks are automatically created, attached, and deleted with the machine.
type AdditionalDiskSpec struct {
	// Name is the logical name for this disk.
	// The actual resource will be named "<machine-name>-<name>".
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// SizeGB is the size of the disk in gigabytes.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Required
	SizeGB int `json:"sizeGB"`

	// Image is the OS image for the disk (optional, for bootable disks).
	// If not specified, the disk will be empty.
	// +optional
	Image *string `json:"image,omitempty"`
}

// InfrastructureMachineInitialization tracks infrastructure provisioning status.
// This matches the CAPI v1.12 infrastructure provider contract which expects
// status.initialization.provisioned on infrastructure machines.
type InfrastructureMachineInitialization struct {
	// Provisioned is true when the machine infrastructure is fully provisioned.
	// This is the field that CAPI v1.12 infrastructure contract checks.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// EvrocMachineStatus defines the observed state of EvrocMachine
type EvrocMachineStatus struct {
	// Ready indicates if the machine is ready
	Ready bool `json:"ready"`

	// Initialization tracks the status of machine infrastructure provisioning.
	// This is part of the CAPI v1.12+ contract for orchestrating provisioning.
	// +optional
	Initialization InfrastructureMachineInitialization `json:"initialization,omitempty"`

	// MachineID is the unique identifier for the VM in evroc
	MachineID string `json:"machineID,omitempty"`

	// Addresses is the list of addresses assigned to the machine
	// +optional
	Addresses []corev1.NodeAddress `json:"addresses,omitempty"`

	// AvailabilityZone is the resolved availability zone for this machine.
	// This is computed from the CAPI Machine's failureDomain (assigned from
	// the EvrocCluster's failureDomains list) or a hash-based fallback.
	// +optional
	AvailabilityZone string `json:"availabilityZone,omitempty"`

	// Conditions defines current service state of the EvrocMachine.
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`

	// Resources tracks cloud resources created or referenced by this machine controller.
	// +optional
	Resources *MachineResources `json:"resources,omitempty"`
}

// MachineResources tracks cloud resources created or referenced by the machine controller.
type MachineResources struct {
	// BootDisk is the boot disk created for this machine.
	// +optional
	BootDisk *ManagedDisk `json:"bootDisk,omitempty"`

	// PublicIP information for this machine.
	// +optional
	PublicIP *ManagedPublicIP `json:"publicIP,omitempty"`

	// SecurityGroups created or used for this machine.
	// +optional
	SecurityGroups []ManagedSecurityGroup `json:"securityGroups,omitempty"`

	// PlacementGroup used for this machine.
	// +optional
	PlacementGroup *ManagedPlacementGroup `json:"placementGroup,omitempty"`

	// AdditionalDisks attached to this machine.
	// +optional
	AdditionalDisks []ManagedDisk `json:"additionalDisks,omitempty"`
}

// ManagedPlacementGroup tracks a placement group resource.
type ManagedPlacementGroup struct {
	// ID is the cloud resource ID.
	ID string `json:"id"`

	// Name is the cloud resource name.
	Name string `json:"name"`

	// Strategy is the placement strategy. EVROC only supports spread.
	Strategy string `json:"strategy"`

	// Managed indicates whether CAPI created and owns this resource.
	Managed bool `json:"managed"`
}

// ManagedDisk tracks a disk resource.
type ManagedDisk struct {
	// ID is the cloud resource ID.
	ID string `json:"id"`

	// Name is the cloud resource name.
	Name string `json:"name"`

	// SizeGB is the size in gigabytes.
	SizeGB int `json:"sizeGB"`

	// Managed indicates whether CAPI created and owns this resource.
	Managed bool `json:"managed"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=evrocmachines,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta1=v1beta1"
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1beta1"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready",description="Machine is ready"
// +kubebuilder:printcolumn:name="MachineID",type="string",JSONPath=".status.machineID",description="evroc Machine ID"
// +kubebuilder:printcolumn:name="ComputeProfile",type="string",JSONPath=".spec.computeProfile",description="VM compute profile"

// EvrocMachine is the Schema for the evrocmachines API
type EvrocMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EvrocMachineSpec   `json:"spec,omitempty"`
	Status EvrocMachineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EvrocMachineList contains a list of EvrocMachine
type EvrocMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EvrocMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EvrocMachine{}, &EvrocMachineList{})
}

// GetConditions returns the set of conditions for this object.
func (m *EvrocMachine) GetConditions() clusterv1.Conditions {
	return m.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (m *EvrocMachine) SetConditions(conditions clusterv1.Conditions) {
	m.Status.Conditions = conditions
}
