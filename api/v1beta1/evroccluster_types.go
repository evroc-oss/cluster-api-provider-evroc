// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	"strings"

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

	// CredentialsRef is a reference to a Secret in the EvrocCluster namespace
	// containing evroc service account credentials. Cross-namespace references
	// are intentionally unsupported.
	// Required: every cluster must name the credentials it uses.
	// The secret must contain serviceAccountID and serviceAccountSecret, with
	// organization optionally specifying the evroc organization.
	// +kubebuilder:validation:Required
	CredentialsRef *SecretReference `json:"credentialsRef"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// FailureDomains is a list of failure domains (availability zones) in which machines should be placed.
	// This provides high availability by spreading machines across zones.
	// Defaults to ["a", "b", "c"] if not specified.
	// +optional
	FailureDomains []string `json:"failureDomains,omitempty"`

	// Network defines the VPC, subnet, and IP stack configuration for the cluster.
	// When omitted, the project's default VPC with default subnets and dual-stack
	// networking is used.
	// +optional
	Network NetworkSpec `json:"network,omitempty"`

	// AdditionalLabels is an optional set of labels to add to all Evroc resources managed by this cluster.
	// These labels are applied to VMs, disks, IPs, security groups, and other infrastructure.
	// Use these for cost tracking, billing allocation, and resource organization.
	// Example: {"environment": "production", "team": "platform", "cost-center": "engineering"}
	// +optional
	AdditionalLabels map[string]string `json:"additionalLabels,omitempty"`

	// ControlPlaneConfig defines configuration for the control plane endpoint.
	// A load balancer is always created for the control plane.
	// Use this section to customize LB behavior (e.g., bring-your-own LB or IP).
	// +optional
	ControlPlaneConfig *ControlPlaneConfig `json:"controlPlaneConfig,omitempty"`

	// SecurityGroups defines security group configuration for all nodes in the cluster.
	// Rules are split into three sections: common (all nodes), controlPlane (CP only),
	// and worker (workers only). Machines with inheritFromCluster: true automatically
	// receive the common section plus their role-specific section.
	// +optional
	SecurityGroups *ClusterSecurityGroupsConfig `json:"securityGroups,omitempty"`

	// Endpoints overrides the evroc API and authentication endpoints this cluster
	// talks to. Omit it to use the public evroc cloud; set it to target a private
	// cloud deployment. The credentials in credentialsRef must be valid against
	// whichever endpoints are configured here — a mismatch is only detected when
	// the controller first authenticates.
	// +optional
	Endpoints *EndpointsConfig `json:"endpoints,omitempty"`
}

// EndpointsConfig overrides the evroc service endpoints for a cluster.
// Each field is independently optional; an unset field keeps the public evroc
// cloud default. All fields are immutable once set, since repointing a running
// cluster at a different deployment would orphan its existing infrastructure.
type EndpointsConfig struct {
	// APIBaseURL is the base URL for the evroc APIs, e.g.
	// "https://api.private.example.com". Defaults to the public evroc API.
	// +optional
	APIBaseURL string `json:"apiBaseURL,omitempty"`

	// IssuerURL is the OIDC issuer (Keycloak realm) URL, e.g.
	// "https://authn.private.example.com/realms/evroc-customer". This is the
	// same value as issuerURL in the evroc CLI config; the OAuth2 token endpoint
	// is derived from it by appending "/protocol/openid-connect/token", matching
	// what the evroc SDK does when loading that config.
	// Defaults to the public evroc authentication server.
	// +optional
	IssuerURL string `json:"issuerURL,omitempty"`

	// ClientID is the OAuth2 client ID used when requesting tokens. When unset,
	// it is derived as "<serviceAccountID>_<project>", which is the convention
	// used by the public evroc cloud. Set it only if a private deployment's
	// identity provider registers clients under a different name.
	// +optional
	ClientID string `json:"clientID,omitempty"`
}

// GetAPIBaseURL safely returns the configured API base URL, or "".
func (e *EndpointsConfig) GetAPIBaseURL() string {
	if e == nil {
		return ""
	}
	return e.APIBaseURL
}

// TokenPathSuffix is appended to IssuerURL to form the OAuth2 token endpoint.
// This mirrors how the evroc SDK derives the token URL from the issuerURL in
// the CLI config.
const TokenPathSuffix = "/protocol/openid-connect/token"

// GetIssuerURL safely returns the configured issuer URL, or "".
func (e *EndpointsConfig) GetIssuerURL() string {
	if e == nil {
		return ""
	}
	return e.IssuerURL
}

// GetAuthTokenURL returns the OAuth2 token endpoint derived from IssuerURL, or
// "" when no issuer is configured (in which case the SDK default applies).
func (e *EndpointsConfig) GetAuthTokenURL() string {
	issuer := e.GetIssuerURL()
	if issuer == "" {
		return ""
	}
	return strings.TrimSuffix(issuer, "/") + TokenPathSuffix
}

// GetClientID safely returns the configured OAuth2 client ID, or "".
func (e *EndpointsConfig) GetClientID() string {
	if e == nil {
		return ""
	}
	return e.ClientID
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

// SecretReference identifies a Secret in the EvrocCluster's namespace.
type SecretReference struct {
	// Name is the name of the Secret.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// NetworkSpec defines network configuration for the cluster.
type NetworkSpec struct {
	// VPCRef references a pre-existing VPC to place all cluster resources in
	// (security groups, VMs, load balancers). The VPC must already exist in the
	// evroc project. When omitted, the project's default VPC is used.
	//
	// Accepts either a bare VPC name (e.g. "my-vpc"), which is resolved to an
	// FQID within this cluster's project and region, or an already
	// fully-qualified ref, i.e.
	// "/networking/projects/{project}/regions/{region}/virtualPrivateClouds/my-vpc".
	// +optional
	VPCRef *string `json:"vpcRef,omitempty"`

	// SubnetRefs maps availability zone letters to subnets within the VPC.
	// Key is the zone letter (e.g., "a", "b", "c"), value is the subnet.
	// When VPCRef is set, SubnetRefs should map every zone in FailureDomains to
	// the appropriate subnet. When omitted, the default subnet for each zone is
	// used (default-{region}-{zone}).
	//
	// Each value accepts either a bare subnet name (e.g. "my-subnet-a"),
	// resolved to an FQID within this cluster's project and region, or an
	// already fully-qualified ref, i.e.
	// "/networking/projects/{project}/regions/{region}/subnets/my-subnet-a".
	// +optional
	SubnetRefs map[string]string `json:"subnetRefs,omitempty"`

	// StackType is the IP stack type for the internal cluster network.
	// When "dual-stack", VMs and pods get both IPv4 and IPv6 addresses.
	// When "ipv6-only", VMs get only IPv6 addresses; the LB uses
	// ipProtocolSelection to reach backends over IPv6.
	// The control plane endpoint (load balancer frontend) is always IPv4.
	// Defaults to "dual-stack" if not specified.
	// +optional
	// +kubebuilder:validation:Enum="ipv4-only";"dual-stack";"ipv6-only"
	// +kubebuilder:default="dual-stack"
	StackType *string `json:"stackType,omitempty"`
}

// ControlPlaneConfig defines configuration for the control plane endpoint.
type ControlPlaneConfig struct {
	// LoadBalancer allows overriding the auto-created control plane load balancer.
	// If omitted, the controller auto-creates a managed LB with default settings.
	// +optional
	LoadBalancer *LoadBalancerConfig `json:"loadBalancer,omitempty"`
}

// GetLoadBalancer safely returns the LoadBalancer config, or nil.
func (c *ControlPlaneConfig) GetLoadBalancer() *LoadBalancerConfig {
	if c == nil {
		return nil
	}
	return c.LoadBalancer
}

// LoadBalancerConfig allows customizing the control plane load balancer.
// A load balancer is always auto-created — this config overrides defaults.
type LoadBalancerConfig struct {
	// ExistingPublicIPID references a pre-existing public IP to attach to the managed LB.
	// If omitted, a new public IP is auto-created alongside the LB.
	// +optional
	ExistingPublicIPID *string `json:"existingPublicIPID,omitempty"`

	// AdditionalPorts specifies extra TCP ports to forward through the public LB.
	// Each port gets its own BackendService + L4Route with TCP health check.
	// The API server port (6443) is always included and does not need to be listed here.
	// Node registration (e.g. RKE2 supervisor on 9345) happens cluster-internally over
	// private IPs and does not belong here — open it via an inline security group instead.
	// +optional
	AdditionalPorts []int32 `json:"additionalPorts,omitempty"`
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

// PublicIPConfig defines how to configure a public IP for a VM.
// Only one of Enabled or ExistingID should be set.
// Used by MachineNetworkingConfig for per-machine public IPs.
type PublicIPConfig struct {
	// Enabled auto-creates a public IP managed by the controller.
	// The IP will be named "<machine-name>-ip" and tracked in machine.status.resources.
	// When the machine is deleted, the IP is automatically cleaned up.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// ExistingID references a pre-existing public IP by its evroc resource ID (e.g., created by Terraform).
	// The IP is looked up in the evroc project configured via the controller's credentials.
	// This IP will be used but NOT managed by CAPI - CAPI will not create or delete it.
	// Mutually exclusive with Enabled.
	// +optional
	ExistingID *string `json:"existingID,omitempty"`
}

// SecurityGroupsConfig defines security group configuration.
// Multiple approaches can be combined (e.g., some inline, some external).
type SecurityGroupsConfig struct {
	// InlineSecurityGroups defines security groups to auto-create with specified rules.
	// Each group will be named "<cluster-name>-<cluster-uid>-<group-name>" and tracked in cluster.status.
	// +optional
	InlineSecurityGroups []InlineSecurityGroup `json:"inlineSecurityGroups,omitempty"`

	// ExistingIDs references pre-existing security groups by their evroc resource ID (e.g., created by Terraform).
	// These groups will be attached but NOT managed by CAPI - CAPI will not create or delete them.
	// +optional
	ExistingIDs []string `json:"existingIDs,omitempty"`
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

// NetworkStatus provides information about the resolved cluster network configuration.
type NetworkStatus struct {
	// VPCID is the name of the VPC in use by this cluster.
	// +optional
	VPCID string `json:"vpcId,omitempty"`

	// SubnetIDs maps zone letters to the resolved subnet names in use.
	// +optional
	SubnetIDs map[string]string `json:"subnetIds,omitempty"`

	// StackType is the resolved IP stack type for VMs in this cluster.
	// +optional
	StackType string `json:"stackType,omitempty"`
}

// ClusterResources tracks cloud resources created or referenced by the cluster controller.
// These are direct cloud resources, not Kubernetes CRDs.
type ClusterResources struct {
	// LoadBalancer tracks the L4 load balancer for the control plane API endpoint.
	// Always populated — CAPI auto-creates an LB for every cluster.
	// +optional
	LoadBalancer *ManagedLoadBalancer `json:"loadBalancer,omitempty"`

	// SecurityGroups created or used for this cluster.
	// +optional
	SecurityGroups []ManagedSecurityGroup `json:"securityGroups,omitempty"`
}

// ManagedLoadBalancer tracks a load balancer resource used by the cluster.
type ManagedLoadBalancer struct {
	// ID is the evroc resource identifier (metadata.id in the evroc API).
	ID string `json:"id"`

	// UID is the system-generated UUID (metadata.uid in the evroc API).
	UID string `json:"uid"`

	// Address is the LB's public IPv4 address (used as ControlPlaneEndpoint.Host).
	Address string `json:"address"`

	// Backends lists the evroc resource IDs (metadata.id) of VMs registered as backends.
	// +optional
	Backends []string `json:"backends,omitempty"`
}

// ManagedPublicIP tracks a public IP resource used by a machine.
type ManagedPublicIP struct {
	// ID is the evroc resource identifier (metadata.id in the evroc API).
	ID string `json:"id"`

	// UID is the system-generated UUID (metadata.uid in the evroc API).
	UID string `json:"uid"`

	// Address is the allocated IPv4 address.
	Address string `json:"address"`

	// Managed indicates whether CAPI created and owns this resource.
	// True: CAPI created it, will delete it on cluster deletion.
	// False: External resource (e.g., Terraform), CAPI only uses it.
	Managed bool `json:"managed"`
}

// ManagedSecurityGroup tracks a security group resource used by the cluster.
type ManagedSecurityGroup struct {
	// ID is the evroc resource identifier (metadata.id in the evroc API).
	ID string `json:"id"`

	// UID is the system-generated UUID (metadata.uid in the evroc API).
	UID string `json:"uid"`

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
// +kubebuilder:metadata:labels="clusterctl.cluster.x-k8s.io="
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
