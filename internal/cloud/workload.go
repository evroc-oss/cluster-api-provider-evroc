// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"errors"
	"fmt"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/evroc-oss/evroc-go-sdk/compute"
	"github.com/evroc-oss/evroc-go-sdk/filter"
	"github.com/evroc-oss/evroc-go-sdk/loadbalancer"
	"github.com/evroc-oss/evroc-go-sdk/networking"
)

// WorkloadResourceService removes cloud resources that the evroc CCM and CSI
// drivers created from inside the workload cluster. The drivers label every
// resource they create with managed-by=<identifier>; when the identifier is
// the cluster's ownership ID, that label finds their resources at teardown
// without an inventory of what the drivers did.
type WorkloadResourceService struct {
	client *evroc.Client
}

// WorkloadResources returns the workload resource service.
func (c *Client) WorkloadResources() WorkloadResourceServiceInterface {
	return &WorkloadResourceService{client: c.client}
}

// Cleanup deletes the CCM load balancer stacks and CSI disk attachments owned
// by clusterID, then the CSI disks when deleteDisks is set. Every delete is
// issued in one pass; a resource still referenced by another one is left
// pending and picked up on the next call once its dependents are gone.
// Pending stays true until a call finds nothing owned, because the API accepts
// a delete before the resource has actually disappeared.
func (ws *WorkloadResourceService) Cleanup(ctx context.Context, clusterID string, deleteDisks bool) (bool, error) {
	sdk := ws.client
	// A removed Machine does not prove its VM has finished asynchronous
	// deletion, and a disk cannot be detached while its VM exists.
	vms, err := sdk.Compute().VirtualMachines().List(ctx, ownerSelector(LabelClusterID, clusterID))
	if err != nil {
		return false, fmt.Errorf("failed to list cluster VMs: %w", err)
	}
	if len(vms.Items) > 0 {
		return true, nil
	}

	lbc := sdk.LoadBalancer()
	pending := false
	for _, step := range []func() (bool, error){
		func() (bool, error) {
			return deleteOwned(ctx, "loadBalancer", clusterID, lbc.LoadBalancers().List, loadBalancerNames, lbc.LoadBalancers().Delete)
		},
		func() (bool, error) {
			return deleteOwned(ctx, "l4Route", clusterID, lbc.L4Routes().List, l4RouteNames, lbc.L4Routes().Delete)
		},
		func() (bool, error) {
			return deleteOwned(ctx, "backendService", clusterID, lbc.BackendServices().List, backendServiceNames, lbc.BackendServices().Delete)
		},
		func() (bool, error) {
			return deleteOwned(ctx, "backendPool", clusterID, lbc.BackendPools().List, backendPoolNames, lbc.BackendPools().Delete)
		},
		func() (bool, error) {
			return deleteOwned(ctx, "publicIP", clusterID, sdk.Networking().PublicIPs().List, publicIPNames, sdk.Networking().PublicIPs().Delete)
		},
	} {
		found, err := step()
		if err != nil {
			return false, err
		}
		pending = pending || found
	}

	// Disks only depend on their attachments being gone, not on the load
	// balancer resources above.
	hsda := sdk.Compute().HotswapDiskAttachments()
	attached, err := deleteOwned(ctx, "disk attachment", clusterID, hsda.List, attachmentNames, hsda.Delete)
	if err != nil {
		return false, err
	}
	if attached || !deleteDisks {
		return pending || attached, nil
	}
	found, err := deleteOwned(ctx, "disk", clusterID, sdk.Compute().Disks().List, diskNames, sdk.Compute().Disks().Delete)
	if err != nil {
		return false, err
	}
	return pending || found, nil
}

// driverSelector matches resources the CCM and CSI drivers created with the
// cluster's ownership ID as their identifier.
func driverSelector(clusterID string) filter.ListFilter {
	return filter.WithLabelSelector("managed-by=" + clusterID)
}

// deleteOwned deletes every resource of one kind owned by clusterID and
// reports whether any was found. A conflict means the resource is still
// referenced by one deleted in the same pass, so it is retried next time.
func deleteOwned[L any](ctx context.Context, kind, clusterID string, list func(context.Context, ...filter.ListFilter) (*L, error), names func(*L) []string, del func(context.Context, string) error) (bool, error) {
	listed, err := list(ctx, driverSelector(clusterID))
	if err != nil {
		return false, fmt.Errorf("failed to list %ss: %w", kind, err)
	}
	ids := names(listed)
	for _, id := range ids {
		if err := del(ctx, id); err != nil && !isNotFoundError(err) && !errors.Is(err, evroc.ErrConflict) {
			return false, fmt.Errorf("failed to delete %s %s: %w", kind, id, err)
		}
	}
	return len(ids) > 0, nil
}

func loadBalancerNames(list *loadbalancer.LoadbalancerList) []string {
	if list == nil {
		return nil
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Id)
	}
	return names
}

func publicIPNames(list *networking.PublicIPList) []string {
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Id)
	}
	return names
}

func attachmentNames(list *compute.HotswapDiskAttachmentList) []string {
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Id)
	}
	return names
}

func diskNames(list *compute.DiskList) []string {
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Id)
	}
	return names
}
