// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"time"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/evroc-oss/evroc-go-sdk/compute"
	"github.com/evroc-oss/evroc-go-sdk/networking"
	computetypes "github.com/evroc-oss/evroc-go-sdk/types/compute"
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
)

// ClientInterface defines the interface for the cloud client.
// This allows for easy mocking in tests.
type ClientInterface interface {
	Disks() DiskServiceInterface
	PublicIPs() PublicIPServiceInterface
	SecurityGroups() SecurityGroupServiceInterface
	PlacementGroups() PlacementGroupServiceInterface
	VirtualMachines() VirtualMachineServiceInterface
	LoadBalancers() LoadBalancerServiceInterface
	SDKClient() *evroc.Client
}

// DiskServiceInterface defines the interface for disk operations.
type DiskServiceInterface interface {
	Create(ctx context.Context, name string, sizeGB int, image, zone string, labels map[string]string) (*computetypes.Disk, error)
	Get(ctx context.Context, name string) (*computetypes.Disk, error)
	Delete(ctx context.Context, name string) error
	List(ctx context.Context) ([]computetypes.Disk, error)
	Exists(ctx context.Context, name string) (bool, error)

	// Waiter methods for async operations.
	WaitForReady(
		ctx context.Context,
		name string,
		timeout time.Duration,
		opts ...compute.WaiterOption,
	) (*computetypes.Disk, error)
	WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error
}

// PublicIPServiceInterface defines the interface for public IP operations.
type PublicIPServiceInterface interface {
	Create(ctx context.Context, name string, labels map[string]string) (*networkingtypes.PublicIP, error)
	Get(ctx context.Context, name string) (*networkingtypes.PublicIP, error)
	Delete(ctx context.Context, name string) error
	List(ctx context.Context) ([]networkingtypes.PublicIP, error)
	Exists(ctx context.Context, name string) (bool, error)

	// Waiter methods for async operations.
	WaitForReady(
		ctx context.Context,
		name string,
		timeout time.Duration,
		opts ...networking.WaiterOption,
	) (*networkingtypes.PublicIP, error)
	WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error
}

// SecurityGroupServiceInterface defines the interface for security group operations.
type SecurityGroupServiceInterface interface {
	Create(
		ctx context.Context,
		name string,
		rules []networkingtypes.SecurityGroupSpecRulesItem,
		labels map[string]string,
	) (*networkingtypes.SecurityGroup, error)
	Get(ctx context.Context, name string) (*networkingtypes.SecurityGroup, error)
	Delete(ctx context.Context, name string) error
	List(ctx context.Context) ([]networkingtypes.SecurityGroup, error)
	Update(
		ctx context.Context,
		name string,
		group *networkingtypes.SecurityGroup,
	) (*networkingtypes.SecurityGroup, error)
	Exists(ctx context.Context, name string) (bool, error)

	// Waiter methods for async operations.
	WaitForReady(
		ctx context.Context,
		name string,
		timeout time.Duration,
		opts ...networking.WaiterOption,
	) (*networkingtypes.SecurityGroup, error)
	WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error
}

// PlacementGroupServiceInterface defines the interface for placement group operations.
type PlacementGroupServiceInterface interface {
	Create(
		ctx context.Context,
		name string,
		strategy string,
		zone string,
		labels map[string]string,
	) (*computetypes.PlacementGroup, error)
	Get(ctx context.Context, name string) (*computetypes.PlacementGroup, error)
	Delete(ctx context.Context, name string) error
	List(ctx context.Context) ([]computetypes.PlacementGroup, error)
	Exists(ctx context.Context, name string) (bool, error)

	// Waiter methods for async operations.
	WaitForReady(
		ctx context.Context,
		name string,
		timeout time.Duration,
		opts ...compute.WaiterOption,
	) (*computetypes.PlacementGroup, error)
}

// VirtualMachineServiceInterface defines the interface for virtual machine operations.
type VirtualMachineServiceInterface interface {
	Create(
		ctx context.Context,
		request *computetypes.VirtualMachineRequest,
	) (*computetypes.VirtualMachine, error)
	Get(ctx context.Context, name string) (*computetypes.VirtualMachine, error)
	Delete(ctx context.Context, name string) error
	List(ctx context.Context) ([]computetypes.VirtualMachine, error)
	Exists(ctx context.Context, name string) (bool, error)

	// UpdateSecurityGroups updates the security groups attached to a VM.
	UpdateSecurityGroups(
		ctx context.Context,
		vmName string,
		securityGroupNames []string,
	) error

	// UpdatePublicIP attaches or detaches a public IP from a VM.
	// If publicIPName is empty, the public IP will be detached.
	UpdatePublicIP(ctx context.Context, vmName string, publicIPName string) error

	// UpdateDisks patches the disks attached to a VM using fully-qualified disk refs.
	UpdateDisks(ctx context.Context, vmName string, diskNames []string) error

	// UpdatePlacement patches the placement configuration of a VM.
	// The evroc API rejects this if the VM is running — the caller should
	// handle the error and requeue.
	UpdatePlacement(ctx context.Context, vmName string, placement computetypes.VirtualMachineSpecPlacement) error

	// Waiter methods for async operations.
	WaitForReady(
		ctx context.Context,
		name string,
		timeout time.Duration,
		opts ...compute.WaiterOption,
	) (*computetypes.VirtualMachine, error)
	WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error
}

// LoadBalancerServiceInterface defines the interface for load balancer operations.
// Load balancers distribute traffic across multiple control plane nodes, providing
// true HA for the Kubernetes API server endpoint.
type LoadBalancerServiceInterface interface {
	// Create creates a load balancer and all sub-resources (PublicIP, BackendPool,
	// BackendService, L4Route). This is idempotent — if any resource already exists,
	// the conflict is ignored and creation continues with the remaining resources.
	Create(ctx context.Context, request *LoadBalancerCreateRequest) (*LoadBalancer, error)

	// Get retrieves a load balancer by its evroc resource ID.
	Get(ctx context.Context, name string) (*LoadBalancer, error)

	// Delete deletes a load balancer and all sub-resources.
	// This is idempotent — deleting a non-existent resource is a no-op.
	Delete(ctx context.Context, name string) error

	// List lists all load balancers.
	List(ctx context.Context) ([]LoadBalancer, error)

	// Exists checks if a load balancer exists.
	Exists(ctx context.Context, name string) (bool, error)

	// AddBackend registers a VM as a backend target of the load balancer.
	// This is idempotent — adding a backend that already exists is a no-op.
	AddBackend(ctx context.Context, lbName string, backend Backend) error

	// RemoveBackend deregisters a VM from the load balancer.
	// This is idempotent — removing a backend that doesn't exist is a no-op.
	RemoveBackend(ctx context.Context, lbName string, backendName string) error

	// ListBackends lists all backends registered with the load balancer.
	ListBackends(ctx context.Context, lbName string) ([]Backend, error)

	// Waiter methods for async operations.
	WaitForReady(
		ctx context.Context,
		name string,
		timeout time.Duration,
	) (*LoadBalancer, error)
	WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error
}

// Ensure our implementation satisfies the interfaces.
var _ ClientInterface = (*Client)(nil)
var _ DiskServiceInterface = (*DiskService)(nil)
var _ PublicIPServiceInterface = (*PublicIPService)(nil)
var _ SecurityGroupServiceInterface = (*SecurityGroupService)(nil)
var _ PlacementGroupServiceInterface = (*PlacementGroupService)(nil)
var _ VirtualMachineServiceInterface = (*VirtualMachineService)(nil)
var _ LoadBalancerServiceInterface = (*LoadBalancerService)(nil)
