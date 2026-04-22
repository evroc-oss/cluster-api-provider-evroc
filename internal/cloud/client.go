// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"errors"
	"fmt"
	"time"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/evroc-oss/evroc-go-sdk/compute"
	"github.com/evroc-oss/evroc-go-sdk/config"
	"github.com/evroc-oss/evroc-go-sdk/metrics"
	"github.com/evroc-oss/evroc-go-sdk/networking"
	computetypes "github.com/evroc-oss/evroc-go-sdk/types/compute"
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
	"gopkg.in/yaml.v3"
)

// Client wraps the evroc SDK client for CAPI provider use.
type Client struct {
	client *evroc.Client
}

// SDKClient returns the underlying evroc SDK client for advanced usage.
// such as using builder patterns and .Ref() methods.
func (c *Client) SDKClient() *evroc.Client {
	return c.client
}

// ConfigPath is the default path for the mounted evroc config file.
const ConfigPath = "/etc/evroc/config.yaml"

// sdkOpts returns evroc.Option slice with metrics if a manager is provided.
func sdkOpts(m *metrics.Manager) []evroc.Option {
	if m != nil {
		return []evroc.Option{evroc.WithMetrics(m)}
	}
	return nil
}

// NewClient creates a new evroc cloud client from the mounted config file.
func NewClient(ctx context.Context, m *metrics.Manager) (*Client, error) {
	evrocClient, err := evroc.NewFromFile(ctx, ConfigPath, sdkOpts(m)...)
	if err != nil {
		return nil, fmt.Errorf("failed to create evroc SDK client: %w", err)
	}

	return &Client{
		client: evrocClient,
	}, nil
}

// NewClientFromConfig creates a new evroc cloud client from explicit configuration.
func NewClientFromConfig(ctx context.Context, cfg *config.Config, m *metrics.Manager) (*Client, error) {
	evrocClient, err := evroc.New(ctx, *cfg, sdkOpts(m)...)
	if err != nil {
		return nil, fmt.Errorf("failed to create evroc SDK client: %w", err)
	}

	return &Client{
		client: evrocClient,
	}, nil
}

// NewClientFromYAML creates a new evroc cloud client by parsing raw YAML.
// credential data (the same format as the mounted config.yaml).
func NewClientFromYAML(ctx context.Context, data []byte, m *metrics.Manager) (*Client, error) {
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse credentials YAML: %w", err)
	}
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid credentials: %w", err)
	}
	return NewClientFromConfig(ctx, &cfg, m)
}

// DiskService provides access to disk operations.
type DiskService struct {
	client *evroc.Client
}

// Disks returns the disk service.
func (c *Client) Disks() DiskServiceInterface {
	return &DiskService{client: c.client}
}

// Create creates a new disk.
// Note: The SDK client is already configured with project/region from environment.
func (ds *DiskService) Create(
	ctx context.Context,
	name string,
	sizeGB int,
	image, zone string,
	labels map[string]string,
) (*computetypes.Disk, error) {
	// Use SDK builder directly instead of custom helper
	builder := compute.NewDiskBuilder(name).
		WithSizeGB(int32(sizeGB)).
		WithZone(zone)
	if image != "" {
		builder = builder.WithImage(image)
	}
	if len(labels) > 0 {
		builder = builder.WithLabels(labels)
	}
	diskReq := builder.Build()

	created, err := ds.client.Compute().Disks().Create(ctx, diskReq)
	if err != nil {
		// Provide context-specific error messages using SDK typed errors
		if errors.Is(err, evroc.ErrConflict) {
			return nil, fmt.Errorf("disk %q already exists in zone %q: %w", name, zone, err)
		}
		if errors.Is(err, evroc.ErrForbidden) {
			return nil, fmt.Errorf("insufficient permissions to create disk in zone %q: %w", zone, err)
		}
		if errors.Is(err, evroc.ErrBadRequest) {
			return nil, fmt.Errorf("invalid disk configuration (name=%q, size=%dGB, image=%q, zone=%q): %w",
				name, sizeGB, image, zone, err)
		}
		return nil, fmt.Errorf("failed to create disk %q in zone %q: %w", name, zone, err)
	}
	return created, nil
}

// Get retrieves a disk by name.
func (ds *DiskService) Get(ctx context.Context, name string) (*computetypes.Disk, error) {
	disk, err := ds.client.Compute().Disks().Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get disk %s: %w", name, err)
	}
	return disk, nil
}

// Delete deletes a disk by name.
func (ds *DiskService) Delete(ctx context.Context, name string) error {
	err := ds.client.Compute().Disks().Delete(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to delete disk %s: %w", name, err)
	}
	return nil
}

// List lists all disks.
func (ds *DiskService) List(ctx context.Context) ([]computetypes.Disk, error) {
	response, err := ds.client.Compute().Disks().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list disks: %w", err)
	}
	return response.Items, nil
}

// Exists checks if a disk exists.
func (ds *DiskService) Exists(ctx context.Context, name string) (bool, error) {
	_, err := ds.Get(ctx, name)
	if err != nil {
		// Check if it's a not found error.
		if isNotFoundError(err) {
			return false, nil
		}
		// Return other errors (network, auth, etc.) to caller.
		return false, fmt.Errorf("failed to check disk existence: %w", err)
	}
	return true, nil
}

// WaitForReady waits for a disk to reach ready state.
func (ds *DiskService) WaitForReady(
	ctx context.Context,
	name string,
	timeout time.Duration,
	opts ...compute.WaiterOption,
) (*computetypes.Disk, error) {
	return ds.client.Compute().Disks().WaitForReady(ctx, name, timeout, opts...)
}

// WaitForDeleted waits for a disk to be deleted.
func (ds *DiskService) WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error {
	return ds.client.Compute().Disks().WaitForDeleted(ctx, name, timeout)
}

// PublicIPService provides access to public IP operations.
type PublicIPService struct {
	client *evroc.Client
}

// PublicIPs returns the public IP service.
func (c *Client) PublicIPs() PublicIPServiceInterface {
	return &PublicIPService{client: c.client}
}

// SecurityGroupService provides access to security group operations.
type SecurityGroupService struct {
	client *evroc.Client
}

// SecurityGroups returns the security group service.
func (c *Client) SecurityGroups() SecurityGroupServiceInterface {
	return &SecurityGroupService{client: c.client}
}

// PlacementGroupService provides access to placement group operations.
type PlacementGroupService struct {
	client *Client
}

// PlacementGroups returns the placement group service.
func (c *Client) PlacementGroups() PlacementGroupServiceInterface {
	return &PlacementGroupService{client: c}
}

// VirtualMachineService provides access to virtual machine operations.
type VirtualMachineService struct {
	client *Client
}

// VirtualMachines returns the virtual machine service.
func (c *Client) VirtualMachines() VirtualMachineServiceInterface {
	return &VirtualMachineService{client: c}
}

// Create creates a new public IP using the SDK builder pattern.
// Note: The SDK client is already configured with project/region from environment.
func (ps *PublicIPService) Create(ctx context.Context, name string, labels map[string]string) (*networkingtypes.PublicIP, error) {
	builder := networking.NewPublicIPBuilder(name)
	if len(labels) > 0 {
		builder = builder.WithLabels(labels)
	}
	request := builder.Build()

	created, err := ps.client.Networking().PublicIPs().Create(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to create public IP: %w", err)
	}
	return created, nil
}

// Get retrieves a public IP by name.
func (ps *PublicIPService) Get(ctx context.Context, name string) (*networkingtypes.PublicIP, error) {
	ip, err := ps.client.Networking().PublicIPs().Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get public IP %s: %w", name, err)
	}
	return ip, nil
}

// Delete deletes a public IP by name.
func (ps *PublicIPService) Delete(ctx context.Context, name string) error {
	err := ps.client.Networking().PublicIPs().Delete(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to delete public IP %s: %w", name, err)
	}
	return nil
}

// List lists all public IPs.
func (ps *PublicIPService) List(ctx context.Context) ([]networkingtypes.PublicIP, error) {
	response, err := ps.client.Networking().PublicIPs().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list public IPs: %w", err)
	}
	return response.Items, nil
}

// Exists checks if a public IP exists.
func (ps *PublicIPService) Exists(ctx context.Context, name string) (bool, error) {
	_, err := ps.Get(ctx, name)
	if err != nil {
		// Check if it's a not found error.
		if isNotFoundError(err) {
			return false, nil
		}
		// Return other errors (network, auth, etc.) to caller.
		return false, fmt.Errorf("failed to check public IP existence: %w", err)
	}
	return true, nil
}

// WaitForReady waits for a public IP to reach ready state.
func (ps *PublicIPService) WaitForReady(
	ctx context.Context,
	name string,
	timeout time.Duration,
	opts ...networking.WaiterOption,
) (*networkingtypes.PublicIP, error) {
	return ps.client.Networking().PublicIPs().WaitForReady(ctx, name, timeout, opts...)
}

// WaitForDeleted waits for a public IP to be deleted.
func (ps *PublicIPService) WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error {
	return ps.client.Networking().PublicIPs().WaitForDeleted(ctx, name, timeout)
}

// Create creates a new security group using the SDK builder pattern.
func (sg *SecurityGroupService) Create(
	ctx context.Context,
	name string,
	rules []networkingtypes.SecurityGroupSpecRulesItem,
	labels map[string]string,
) (*networkingtypes.SecurityGroup, error) {
	builder := networking.NewSecurityGroupBuilder(name)

	// Add rules using builder methods based on rule properties.
	for _, rule := range rules {
		// Extract rule properties.
		ruleName := ""
		if rule.Name != nil {
			ruleName = *rule.Name
		}

		protocol := "all"
		if rule.Protocol != nil {
			protocol = string(*rule.Protocol)
		}

		port := int32(0)
		if rule.Port != nil {
			port = *rule.Port
		}

		endPort := int32(0)
		if rule.EndPort != nil {
			endPort = *rule.EndPort
		}

		// Determine remote (source/destination).
		remote := ""
		if rule.Remote.Address != nil {
			remote = rule.Remote.Address.IpAddressOrCIDR
		}

		// Add ingress or egress rule.
		switch rule.Direction {
		case networkingtypes.SecurityGroupSpecRulesItemDirectionIngress:
			if rule.Remote.SecurityGroupRef != nil {
				builder = builder.AllowIngressFromSecurityGroup(ruleName, protocol, port, endPort, *rule.Remote.SecurityGroupRef)
			} else {
				builder = builder.AllowIngressRule(ruleName, protocol, port, endPort, remote)
			}
		case networkingtypes.SecurityGroupSpecRulesItemDirectionEgress:
			builder = builder.AllowEgressRule(ruleName, protocol, port, endPort, remote)
		}
	}

	if len(labels) > 0 {
		builder = builder.WithLabels(labels)
	}
	request := builder.Build()

	created, err := sg.client.Networking().SecurityGroups().Create(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to create security group: %w", err)
	}
	return created, nil
}

// Get retrieves a security group by name.
func (sg *SecurityGroupService) Get(ctx context.Context, name string) (*networkingtypes.SecurityGroup, error) {
	group, err := sg.client.Networking().SecurityGroups().Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get security group %s: %w", name, err)
	}
	return group, nil
}

// Delete deletes a security group by name.
func (sg *SecurityGroupService) Delete(ctx context.Context, name string) error {
	err := sg.client.Networking().SecurityGroups().Delete(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to delete security group %s: %w", name, err)
	}
	return nil
}

// List lists all security groups.
func (sg *SecurityGroupService) List(ctx context.Context) ([]networkingtypes.SecurityGroup, error) {
	response, err := sg.client.Networking().SecurityGroups().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list security groups: %w", err)
	}
	return response.Items, nil
}

// Update updates a security group.
func (sg *SecurityGroupService) Update(
	ctx context.Context,
	name string,
	group *networkingtypes.SecurityGroup,
) (*networkingtypes.SecurityGroup, error) {
	updated, err := sg.client.Networking().SecurityGroups().Patch(ctx, name, group)
	if err != nil {
		return nil, fmt.Errorf("failed to update security group %s: %w", name, err)
	}
	return updated, nil
}

// Exists checks if a security group exists.
func (sg *SecurityGroupService) Exists(ctx context.Context, name string) (bool, error) {
	_, err := sg.Get(ctx, name)
	if err != nil {
		// Check if it's a not found error.
		if isNotFoundError(err) {
			return false, nil
		}
		// Return other errors (network, auth, etc.) to caller.
		return false, fmt.Errorf("failed to check security group existence: %w", err)
	}
	return true, nil
}

// WaitForReady waits for a security group to reach ready state.
func (sg *SecurityGroupService) WaitForReady(
	ctx context.Context,
	name string,
	timeout time.Duration,
	opts ...networking.WaiterOption,
) (*networkingtypes.SecurityGroup, error) {
	return sg.client.Networking().SecurityGroups().WaitForReady(ctx, name, timeout, opts...)
}

// WaitForDeleted waits for a security group to be deleted.
func (sg *SecurityGroupService) WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error {
	return sg.client.Networking().SecurityGroups().WaitForDeleted(ctx, name, timeout)
}

// IsNotFoundError checks if an error indicates a resource was not found.
// Uses the SDK's typed ErrNotFound error for reliable detection.
func IsNotFoundError(err error) bool {
	return errors.Is(err, evroc.ErrNotFound)
}

// isNotFoundError is the internal alias for backward compatibility.
func isNotFoundError(err error) bool {
	return IsNotFoundError(err)
}
