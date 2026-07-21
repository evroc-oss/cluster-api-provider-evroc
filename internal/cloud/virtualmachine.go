// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/evroc-oss/evroc-go-sdk/compute"
	computetypes "github.com/evroc-oss/evroc-go-sdk/types/compute"
)

// Create creates a new virtual machine.
func (vms *VirtualMachineService) Create(
	ctx context.Context,
	request *computetypes.VirtualMachineRequest,
) (*computetypes.VirtualMachine, error) {
	created, err := vms.client.client.Compute().VirtualMachines().Create(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to create virtual machine: %w", err)
	}

	return created, nil
}

// Get retrieves a virtual machine by name.
func (vms *VirtualMachineService) Get(
	ctx context.Context,
	name string,
) (*computetypes.VirtualMachine, error) {
	vm, err := vms.client.client.Compute().VirtualMachines().Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get virtual machine: %w", err)
	}

	return vm, nil
}

// Delete deletes a virtual machine.
func (vms *VirtualMachineService) Delete(ctx context.Context, name string) error {
	if err := vms.client.client.Compute().VirtualMachines().Delete(ctx, name); err != nil {
		return fmt.Errorf("failed to delete virtual machine: %w", err)
	}

	return nil
}

// List lists all virtual machines.
func (vms *VirtualMachineService) List(
	ctx context.Context,
) ([]computetypes.VirtualMachine, error) {
	response, err := vms.client.client.Compute().VirtualMachines().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list virtual machines: %w", err)
	}

	return response.Items, nil
}

// Exists checks if a virtual machine exists.
func (vms *VirtualMachineService) Exists(ctx context.Context, name string) (bool, error) {
	_, err := vms.Get(ctx, name)
	if err != nil {
		// Check if it's a not found error.
		if isNotFoundError(err) {
			return false, nil
		}
		// Return other errors (network, auth, etc.) to caller.
		return false, fmt.Errorf("failed to check VM existence: %w", err)
	}

	return true, nil
}

// UpdateSecurityGroups updates the security groups attached to a VM.
// Only patches the VM if the security groups have changed.
func (vms *VirtualMachineService) UpdateSecurityGroups(
	ctx context.Context,
	vmName string,
	desiredSecurityGroupNames []string,
) error {
	// Get current VM to check existing security groups
	vm, err := vms.Get(ctx, vmName)
	if err != nil {
		return fmt.Errorf("failed to get VM for security group update: %w", err)
	}

	// Extract current security group refs from VM status
	// EVROC API returns active memberships in status.networking.securityGroupActiveMembershipRefs
	var currentSGs []string
	if vm.Status.Networking != nil && vm.Status.Networking.SecurityGroupActiveMembershipRefs != nil {
		for _, ref := range *vm.Status.Networking.SecurityGroupActiveMembershipRefs {
			// Extract just the name from the ref (format: /networking/projects/xxx/regions/xxx/securityGroups/NAME)
			parts := strings.Split(ref, "/")
			if len(parts) > 0 {
				currentSGs = append(currentSGs, parts[len(parts)-1])
			}
		}
	}

	// Check if security groups have changed
	if securityGroupsEqual(currentSGs, desiredSecurityGroupNames) {
		// No change needed
		return nil
	}

	// Build full resource paths for security groups using SDK helper
	// EVROC API requires full paths: /networking/projects/{project}/regions/{region}/securityGroups/{name}
	sgRefs := make([]string, len(desiredSecurityGroupNames))
	for i, name := range desiredSecurityGroupNames {
		sgRefs[i] = string(vms.client.client.Networking().SecurityGroupRef(name))
	}

	// Build patch request with correct EVROC API structure.
	// VMs reference security groups via securityGroupSettings.securityGroupMemberRefs
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"networking": map[string]interface{}{
				"securityGroupSettings": map[string]interface{}{
					"securityGroupMemberRefs": sgRefs,
				},
			},
		},
	}

	// Call SDK patch method.
	_, err = vms.client.client.Compute().VirtualMachines().Patch(ctx, vmName, patch)
	if err != nil {
		return fmt.Errorf("failed to update VM security groups: %w", err)
	}

	return nil
}

// securityGroupsEqual checks if two security group lists are equal (order-independent).
func securityGroupsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	aMap := make(map[string]bool, len(a))
	for _, sg := range a {
		aMap[sg] = true
	}

	for _, sg := range b {
		if !aMap[sg] {
			return false
		}
	}

	return true
}

// UpdatePublicIP attaches or detaches a public IP from a VM.
// Only patches the VM if the public IP has changed.
func (vms *VirtualMachineService) UpdatePublicIP(
	ctx context.Context,
	vmName string,
	publicIPName string,
) error {
	// Get current VM to check existing public IP
	vm, err := vms.Get(ctx, vmName)
	if err != nil {
		return fmt.Errorf("failed to get VM for public IP update: %w", err)
	}

	// Extract current public IP ref from VM spec
	currentPublicIPName := ""
	if vm.Spec.Networking.PublicIPv4Address != nil &&
		vm.Spec.Networking.PublicIPv4Address.Static != nil &&
		vm.Spec.Networking.PublicIPv4Address.Static.PublicIPRef != nil {
		ref := *vm.Spec.Networking.PublicIPv4Address.Static.PublicIPRef
		// Extract just the name from the full ref path
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			currentPublicIPName = parts[len(parts)-1]
		}
	}

	// Check if public IP has changed
	if currentPublicIPName == publicIPName {
		return nil
	}

	// Build patch request using the correct API structure:
	// spec.networking.publicIPv4Address.static.publicIPRef
	var patch map[string]interface{}
	if publicIPName != "" {
		// Attach public IP using SDK helper to build full resource path
		ipRef := string(vms.client.client.Networking().PublicIPRef(publicIPName))
		patch = map[string]interface{}{
			"spec": map[string]interface{}{
				"networking": map[string]interface{}{
					"publicIPv4Address": map[string]interface{}{
						"static": map[string]interface{}{
							"publicIPRef": ipRef,
						},
					},
				},
			},
		}
	} else {
		// Detach public IP by setting to null.
		patch = map[string]interface{}{
			"spec": map[string]interface{}{
				"networking": map[string]interface{}{
					"publicIPv4Address": nil,
				},
			},
		}
	}

	// Call SDK patch method.
	_, err = vms.client.client.Compute().VirtualMachines().Patch(ctx, vmName, patch)
	if err != nil {
		return fmt.Errorf("failed to update VM public IP: %w", err)
	}

	return nil
}

// UpdateDisks patches the disks attached to a VM using fully-qualified disk refs.
func (vms *VirtualMachineService) UpdateDisks(
	ctx context.Context,
	vmName string,
	diskNames []string,
) error {
	type diskItem struct {
		DiskRef  string `json:"diskRef"`
		BootFrom *bool  `json:"bootFrom,omitempty"`
	}

	items := make([]diskItem, len(diskNames))
	for i, name := range diskNames {
		items[i] = diskItem{
			DiskRef: string(vms.client.client.Compute().DiskRef(name)),
		}
		if i == 0 {
			bootFrom := true
			items[i].BootFrom = &bootFrom
		}
	}

	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"disks": items,
		},
	}

	_, err := vms.client.client.Compute().VirtualMachines().Patch(ctx, vmName, patch)
	if err != nil {
		return fmt.Errorf("failed to update VM disks: %w", err)
	}

	return nil
}

// UpdatePlacement patches the placement configuration of a VM.
// The evroc API rejects this if the VM is running.
func (vms *VirtualMachineService) UpdatePlacement(
	ctx context.Context,
	vmName string,
	placement computetypes.VirtualMachineSpecPlacement,
) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"placement": placement,
		},
	}

	_, err := vms.client.client.Compute().VirtualMachines().Patch(ctx, vmName, patch)
	if err != nil {
		return fmt.Errorf("failed to update VM placement: %w", err)
	}

	return nil
}

// WaitForReady waits for a virtual machine to reach ready state.
func (vms *VirtualMachineService) WaitForReady(
	ctx context.Context,
	name string,
	timeout time.Duration,
	opts ...compute.WaiterOption,
) (*computetypes.VirtualMachine, error) {
	return vms.client.client.Compute().VirtualMachines().WaitForReady(ctx, name, timeout, opts...)
}

// WaitForDeleted waits for a virtual machine to be deleted.
func (vms *VirtualMachineService) WaitForDeleted(
	ctx context.Context,
	name string,
	timeout time.Duration,
) error {
	return vms.client.client.Compute().VirtualMachines().WaitForDeleted(ctx, name, timeout)
}
