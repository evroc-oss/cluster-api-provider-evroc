// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

// SecurityGroupRule defines a single firewall rule.
// This type is used by inline security group configuration on EvrocCluster and EvrocMachine.
type SecurityGroupRule struct {
	// Name is the unique name of this rule within the security group
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Direction specifies if this is an Ingress or Egress rule
	// +kubebuilder:validation:Enum=Ingress;Egress
	// +kubebuilder:validation:Required
	Direction string `json:"direction"`

	// Protocol specifies the protocol (TCP, UDP, ICMP, or All)
	// +kubebuilder:validation:Enum=TCP;UDP;ICMP;All
	// +optional
	Protocol string `json:"protocol,omitempty"`

	// Port specifies the port number (0 means all ports)
	// +optional
	Port *int32 `json:"port,omitempty"`

	// EndPort specifies the end of a port range
	// +optional
	EndPort *int32 `json:"endPort,omitempty"`

	// RemoteCIDR specifies the remote IP address or CIDR block
	// +optional
	RemoteCIDR string `json:"remoteCIDR,omitempty"`

	// RemoteSecurityGroup references another security group
	// +optional
	RemoteSecurityGroup string `json:"remoteSecurityGroup,omitempty"`
}
