// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

// LoadBalancerStatus represents the lifecycle state of a load balancer.
type LoadBalancerStatus string

const (
	LoadBalancerStatusCreating LoadBalancerStatus = "Creating"
	LoadBalancerStatusActive   LoadBalancerStatus = "Active"
	LoadBalancerStatusError    LoadBalancerStatus = "Error"
	LoadBalancerStatusDeleting LoadBalancerStatus = "Deleting"
)

// LoadBalancerCreateRequest defines the parameters for creating a load balancer.
// The cloud layer transparently creates all required sub-resources
// (PublicIP, BackendPool, BackendService, L4Route, LoadBalancer).
type LoadBalancerCreateRequest struct {
	Name               string
	Port               int32   // Frontend listener port (e.g. 6443)
	BackendPort        int32   // Backend port forwarded to VMs (e.g. 6443)
	AdditionalPorts    []int32 // Additional ports to forward (e.g. 9345 for RKE2 supervisor)
	ExistingPublicIPID string  // If set, use this pre-existing IP instead of auto-creating one
	Labels             map[string]string
}

// Backend represents a VM registered as a backend target of the load balancer.
type Backend struct {
	Name string // VM name (used as backend pool ref key)
}

// LoadBalancer represents a load balancer and its associated sub-resources.
type LoadBalancer struct {
	Name     string
	ID       string
	Address  string             // Public IPv4 address allocated to the LB
	Status   LoadBalancerStatus // Creating, Active, Error, Deleting
	Backends []Backend
}

// IsActive returns true when the load balancer has reached a usable state.
func (lb *LoadBalancer) IsActive() bool {
	return lb.Status == LoadBalancerStatusActive
}
