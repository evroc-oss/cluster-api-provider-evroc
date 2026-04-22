// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// EvrocClusterSpec defines the desired state of EvrocCluster
type EvrocClusterSpec struct {
	// Project is the evroc project name
	Project string `json:"project"`

	// Region is the evroc region (e.g., "se-sto").
	// Defaults to "se-sto" if not specified.
	// +optional
	Region string `json:"region,omitempty"`

	// CredentialsRef is a reference to a Secret containing evroc API credentials.
	// The secret must contain a "config.yaml" key with the evroc SDK config format:
	//   auth:
	//     token: "..."
	//     refresh_token: "..."
	//   context:
	//     project: "..."
	//     region: "..."
	// When omitted the controller falls back to the globally mounted config file.
	// +optional
	CredentialsRef *SecretReference `json:"credentialsRef,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// FailureDomains is a list of failure domains (availability zones) in which machines should be placed.
	// This provides high availability by spreading machines across zones.
	// Defaults to ["a", "b", "c"] if not specified.
	// +optional
	FailureDomains []string `json:"failureDomains,omitempty"`

	// NetworkSpec defines the network configuration for the cluster.
	// Currently unused; reserved for future VPC/Subnet integration.
	// +optional
	Network NetworkSpec `json:"network,omitempty"`

	// AdditionalLabels is an optional set of labels to add to all Evroc resources managed by this cluster.
	// These labels are applied to VMs, disks, IPs, security groups, and other infrastructure.
	// Use these for cost tracking, billing allocation, and resource organization.
	// Example: {"environment": "production", "team": "platform", "cost-center": "engineering"}
	// +optional
	AdditionalLabels map[string]string `json:"additionalLabels,omitempty"`

	// ControlPlaneConfig defines networking configuration for control plane nodes.
	// Currently holds public IP configuration for the control plane endpoint.
	// +optional
	ControlPlaneConfig *ControlPlaneConfig `json:"controlPlaneConfig,omitempty"`

	// SecurityGroups defines security group configuration for all nodes in the cluster.
	// Rules are split into three sections: common (all nodes), controlPlane (CP only),
	// and worker (workers only). Machines with inheritFromCluster: true automatically
	// receive the common section plus their role-specific section.
	// +optional
	SecurityGroups *ClusterSecurityGroupsConfig `json:"securityGroups,omitempty"`
}

// InfrastructureClusterInitialization tracks infrastructure provisioning status.
// This matches the CAPI v1.12 infrastructure provider contract which expects
// status.initialization.provisioned (not infrastructureProvisioned).
type InfrastructureClusterInitialization struct {
	// Provisioned is true when the infrastructure is fully provisioned.
	// This is the field that CAPI v1.12 infrastructure contract checks.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`

	// InfrastructureProvisioned is the v1beta2 field for forwards compatibility.
	// Both fields should be set to the same value.
	// +optional
	InfrastructureProvisioned *bool `json:"infrastructureProvisioned,omitempty"`
}

// SecretReference is a reference to a Secret in a given namespace.
type SecretReference struct {
	// Name is the name of the Secret.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the Secret. Defaults to the namespace of the
	// EvrocCluster if omitted.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// NetworkSpec defines network configuration for the cluster
type NetworkSpec struct {
	// VPCRef is a reference to an EvrocVPC resource (future implementation)
	// +optional
	VPCRef *string `json:"vpcRef,omitempty"`

	// SubnetRef is a reference to an EvrocSubnet resource (future implementation)
	// +optional
	SubnetRef *string `json:"subnetRef,omitempty"`
}

// ControlPlaneConfig defines networking configuration for control plane nodes.
type ControlPlaneConfig struct {
	// PublicIP configuration for the control plane endpoint.
	// When enabled, a public IP will be auto-created and used for ControlPlaneEndpoint.Host.
	// +optional
	PublicIP *PublicIPConfig `json:"publicIP,omitempty"`
}

// ClusterSecurityGroupsConfig defines security groups by node role.
// Each section can define inline groups (auto-created) and reference existing groups.
// Machines with inheritFromCluster: true receive common + their role-specific section.
type ClusterSecurityGroupsConfig struct {
	// Common security groups applied to ALL nodes (control plane and workers).
	// +optional
	Common *SecurityGroupsConfig `json:"common,omitempty"`

	// ControlPlane security groups applied only to control plane nodes.
	// +optional
	ControlPlane *SecurityGroupsConfig `json:"controlPlane,omitempty"`

	// Worker security groups applied only to worker nodes.
	// +optional
	Worker *SecurityGroupsConfig `json:"worker,omitempty"`
}

// PublicIPConfig defines how to configure a public IP.
// Only one of Enabled or ExistingName should be set.
type PublicIPConfig struct {
	// Enabled auto-creates a public IP managed by the controller.
	// The IP will be named "<cluster-name>-cp-ip" and tracked in cluster.status.resources.
	// When the cluster is deleted, the IP is automatically cleaned up.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// ExistingName references a pre-existing public IP by name (e.g., created by Terraform).
	// The IP is looked up in the evroc project configured via the controller's credentials.
	// This IP will be used but NOT managed by CAPI - CAPI will not create or delete it.
	// Mutually exclusive with Enabled.
	// +optional
	ExistingName *string `json:"existingName,omitempty"`
}

// SecurityGroupsConfig defines security group configuration.
// Multiple approaches can be combined (e.g., some inline, some external).
type SecurityGroupsConfig struct {
	// InlineSecurityGroups defines security groups to auto-create with specified rules.
	// Each group will be named "<cluster-name>-<cluster-uid>-<group-name>" and tracked in cluster.status.
	// +optional
	InlineSecurityGroups []InlineSecurityGroup `json:"inlineSecurityGroups,omitempty"`

	// ExistingNames references pre-existing security groups by name (e.g., created by Terraform).
	// These groups will be attached but NOT managed by CAPI - CAPI will not create or delete them.
	// +optional
	ExistingNames []string `json:"existingNames,omitempty"`
}

// InlineSecurityGroup defines a security group with inline rules for auto-creation.
type InlineSecurityGroup struct {
	// Name is the logical name for this security group.
	// The actual resource will be named "<cluster-name>-<cluster-uid>-<name>".
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Rules defines the firewall rules for this security group.
	// +optional
	Rules []SecurityGroupRule `json:"rules,omitempty"`
}

// EvrocClusterStatus defines the observed state of EvrocCluster
type EvrocClusterStatus struct {
	// Ready indicates whether the cluster infrastructure is ready
	// +optional
	Ready bool `json:"ready"`

	// Initialization tracks the status of cluster infrastructure provisioning.
	// This is part of the CAPI v1.12+ contract for orchestrating provisioning.
	// +optional
	Initialization InfrastructureClusterInitialization `json:"initialization,omitempty"`

	// FailureDomains reports available zones to CAPI's KCP controller for control plane spreading.
	// Required by the InfraCluster contract; KCP reads this from Status, not Spec.
	// Populated by reconcileFailureDomains from Spec.FailureDomains.
	// +optional
	FailureDomains []clusterv1.FailureDomain `json:"failureDomains,omitempty"`

	// Conditions defines current service state of the EvrocCluster.
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`

	// Network contains information about the cluster's network configuration
	// +optional
	Network NetworkStatus `json:"network,omitempty"`

	// Resources tracks cloud resources created or referenced by this controller.
	// These are cloud resource IDs, not Kubernetes CRDs.
	// Managed resources are automatically cleaned up when the cluster is deleted.
	// +optional
	Resources *ClusterResources `json:"resources,omitempty"`
}

// NetworkStatus provides information about the cluster network
type NetworkStatus struct {
	// VPCID is the ID of the VPC (when implemented)
	// +optional
	VPCID string `json:"vpcId,omitempty"`

	// SubnetID is the ID of the subnet (when implemented)
	// +optional
	SubnetID string `json:"subnetId,omitempty"`
}

// ClusterResources tracks cloud resources created or referenced by the cluster controller.
// These are direct cloud resources, not Kubernetes CRDs.
type ClusterResources struct {
	// PublicIP information for the control plane endpoint.
	// Only populated when using inline publicIP configuration (enabled or existingName).
	// +optional
	PublicIP *ManagedPublicIP `json:"publicIP,omitempty"`

	// SecurityGroups created or used for this cluster.
	// +optional
	SecurityGroups []ManagedSecurityGroup `json:"securityGroups,omitempty"`
}

// ManagedPublicIP tracks a public IP resource used by the cluster.
type ManagedPublicIP struct {
	// ID is the cloud resource ID (e.g., "my-cluster-cp-ip").
	ID string `json:"id"`

	// Address is the allocated IPv4 address.
	Address string `json:"address"`

	// Name is the cloud resource name.
	Name string `json:"name"`

	// Managed indicates whether CAPI created and owns this resource.
	// True: CAPI created it, will delete it on cluster deletion.
	// False: External resource (e.g., Terraform), CAPI only uses it.
	Managed bool `json:"managed"`
}

// ManagedSecurityGroup tracks a security group resource used by the cluster.
type ManagedSecurityGroup struct {
	// ID is the cloud resource ID.
	ID string `json:"id"`

	// Name is the cloud resource name.
	Name string `json:"name"`

	// Managed indicates whether CAPI created and owns this resource.
	// True: CAPI created it, will delete it on cluster deletion.
	// False: External resource (e.g., Terraform), CAPI only uses it.
	Managed bool `json:"managed"`

	// Role indicates which section this SG belongs to: "common", "controlPlane", or "worker".
	// Used by the machine controller to select the right SGs based on node role.
	Role string `json:"role"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=evrocclusters,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta1=v1beta1"
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1beta1"
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels['cluster\\.x-k8s\\.io/cluster-name']",description="Cluster"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready",description="Cluster infrastructure is ready"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".spec.controlPlaneEndpoint.host",description="Control plane endpoint"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Time duration since creation"

// EvrocCluster is the Schema for the evrocclusters API
type EvrocCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EvrocClusterSpec   `json:"spec,omitempty"`
	Status EvrocClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EvrocClusterList contains a list of EvrocCluster
type EvrocClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EvrocCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EvrocCluster{}, &EvrocClusterList{})
}

// GetConditions returns the set of conditions for this object.
func (c *EvrocCluster) GetConditions() clusterv1.Conditions {
	return c.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (c *EvrocCluster) SetConditions(conditions clusterv1.Conditions) {
	c.Status.Conditions = conditions
}
