// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"fmt"
	"time"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/evroc-oss/evroc-go-sdk/loadbalancer"
	"github.com/evroc-oss/evroc-go-sdk/networking"
	lbtypes "github.com/evroc-oss/evroc-go-sdk/types/loadbalancer"
)

// LoadBalancerService provides access to load balancer operations.
// It transparently manages the full resource graph (PublicIP, BackendPool,
// BackendService, L4Route, LoadBalancer) behind a simple interface.
type LoadBalancerService struct {
	client *Client
}

func (ls *LoadBalancerService) lbClient() *loadbalancer.Client {
	return ls.client.SDKClient().LoadBalancer()
}

func (ls *LoadBalancerService) ipName(lbName string) string    { return lbName + "-ip" }
func (ls *LoadBalancerService) poolName(lbName string) string  { return lbName + "-pool" }
func (ls *LoadBalancerService) svcName(lbName string) string   { return lbName + "-svc" }
func (ls *LoadBalancerService) routeName(lbName string) string { return lbName + "-route" }

func (ls *LoadBalancerService) vmRef(vmName string) string {
	return string(ls.client.SDKClient().Compute().VMRef(vmName))
}

// Create creates a load balancer and all required sub-resources:
// PublicIP → BackendPool → BackendService → L4Route → LoadBalancer.
func (ls *LoadBalancerService) Create(ctx context.Context, request *LoadBalancerCreateRequest) (*LoadBalancer, error) {
	if request == nil {
		return nil, fmt.Errorf("load balancer create request is nil")
	}

	poolName := ls.poolName(request.Name)
	svcName := ls.svcName(request.Name)
	routeName := ls.routeName(request.Name)
	lbc := ls.lbClient()
	sdk := ls.client.SDKClient()

	// 1. Resolve PublicIP — use existing or auto-create
	ipName := request.ExistingPublicIPID
	if ipName == "" {
		ipName = ls.ipName(request.Name)
		_, err := sdk.Networking().PublicIPs().Create(ctx,
			networking.NewPublicIPBuilder(ipName).WithLabels(request.Labels).Build())
		if err != nil && !isConflictError(err) {
			return nil, fmt.Errorf("failed to create public IP for LB: %w", err)
		}
	}

	readyIP, err := sdk.Networking().PublicIPs().WaitForReady(ctx, ipName, 3*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("public IP for LB never became ready: %w", err)
	}

	// 2. Create BackendPool (starts empty — machines register later)
	pool, err := loadbalancer.NewBackendPoolBuilder(poolName).
		WithLabels(request.Labels).
		Create(ctx, lbc.BackendPools())
	if err != nil {
		if !isConflictError(err) {
			return nil, fmt.Errorf("failed to create backend pool: %w", err)
		}
		if pool, err = lbc.BackendPools().Get(ctx, poolName); err != nil {
			return nil, fmt.Errorf("failed to get existing backend pool: %w", err)
		}
	}

	// 3. Create BackendService with TCP health check on the backend port.
	// For ipv6-only clusters, set ipProtocolSelection so the LB reaches
	// backends over IPv6 (the LB frontend is always IPv4).
	svcBuilder := loadbalancer.NewBackendServiceBuilder(svcName).
		WithPort(request.BackendPort).
		WithBackendPoolRef(pool.Ref()).
		WithTCPHealthCheck().
		WithLabels(request.Labels)
	svcReq := svcBuilder.Build()
	if request.StackType == "ipv6-only" {
		proto := lbtypes.IPv6
		svcReq.Spec.IpProtocolSelection = &proto
	}
	svc, err := lbc.BackendServices().Create(ctx, svcReq)
	if err != nil {
		if !isConflictError(err) {
			return nil, fmt.Errorf("failed to create backend service: %w", err)
		}
		if svc, err = lbc.BackendServices().Get(ctx, svcName); err != nil {
			return nil, fmt.Errorf("failed to get existing backend service: %w", err)
		}
	}

	// 4. Create L4Route — refs the service from step 3
	route, err := loadbalancer.NewL4RouteBuilder(routeName).
		WithBackendServiceRef(svc.Ref()).
		WithLabels(request.Labels).
		Create(ctx, lbc.L4Routes())
	if err != nil {
		if !isConflictError(err) {
			return nil, fmt.Errorf("failed to create L4 route: %w", err)
		}
		if route, err = lbc.L4Routes().Get(ctx, routeName); err != nil {
			return nil, fmt.Errorf("failed to get existing L4 route: %w", err)
		}
	}

	// 5. Create a service and route for each additional port
	var additionalListeners []lbtypes.LoadbalancerSpecListenersItem
	for _, port := range request.AdditionalPorts {
		portSuffix := fmt.Sprintf("-%d", port)
		extraSvc, extraErr := loadbalancer.NewBackendServiceBuilder(svcName+portSuffix).
			WithPort(port).
			WithBackendPoolRef(pool.Ref()).
			WithTCPHealthCheck().
			WithLabels(request.Labels).
			Create(ctx, lbc.BackendServices())
		if extraErr != nil {
			if !isConflictError(extraErr) {
				return nil, fmt.Errorf("failed to create backend service for port %d: %w", port, extraErr)
			}
			if extraSvc, extraErr = lbc.BackendServices().Get(ctx, svcName+portSuffix); extraErr != nil {
				return nil, fmt.Errorf("failed to get existing backend service for port %d: %w", port, extraErr)
			}
		}
		extraRoute, extraErr := loadbalancer.NewL4RouteBuilder(routeName+portSuffix).
			WithBackendServiceRef(extraSvc.Ref()).
			WithLabels(request.Labels).
			Create(ctx, lbc.L4Routes())
		if extraErr != nil {
			if !isConflictError(extraErr) {
				return nil, fmt.Errorf("failed to create L4 route for port %d: %w", port, extraErr)
			}
			if extraRoute, extraErr = lbc.L4Routes().Get(ctx, routeName+portSuffix); extraErr != nil {
				return nil, fmt.Errorf("failed to get existing L4 route for port %d: %w", port, extraErr)
			}
		}
		name := fmt.Sprintf("port-%d", port)
		refs := []string{extraRoute.Ref()}
		additionalListeners = append(additionalListeners, lbtypes.LoadbalancerSpecListenersItem{
			Name:      &name,
			Port:      port,
			Protocol:  lbtypes.TCP,
			RouteRefs: &refs,
		})
	}

	// 6. Create LoadBalancer — refs the IP from step 1 and all routes
	lbBuilder := loadbalancer.NewLoadBalancerBuilder(request.Name).
		WithPublicIPRef(string(readyIP.Ref())).
		WithListener(apiServerListener(request.Port, route.Ref())).
		WithLabels(request.Labels)
	for _, listener := range additionalListeners {
		lbBuilder = lbBuilder.WithListener(listener)
	}
	lbReq := lbBuilder.Build()

	if request.BackendNetwork != nil {
		bn := &lbtypes.LoadbalancerSpecBackendNetwork{
			VpcRef: VPCRef(sdk, request.BackendNetwork.VPCName),
		}
		for zone, subnetName := range request.BackendNetwork.SubnetNames {
			bn.Subnets = append(bn.Subnets, struct {
				SubnetRef string                                            `json:"subnetRef"`
				Zone      lbtypes.LoadbalancerSpecBackendNetworkSubnetsZone `json:"zone"`
			}{
				SubnetRef: SubnetRef(sdk, subnetName),
				Zone:      lbtypes.LoadbalancerSpecBackendNetworkSubnetsZone(zone),
			})
		}
		lbReq.Spec.BackendNetwork = bn
	}

	_, err = lbc.LoadBalancers().Create(ctx, lbReq)
	if err != nil && !isConflictError(err) {
		return nil, fmt.Errorf("failed to create load balancer: %w", err)
	}

	return ls.buildLoadBalancer(ctx, request.Name)
}

// Get retrieves a load balancer by name.
func (ls *LoadBalancerService) Get(ctx context.Context, name string) (*LoadBalancer, error) {
	return ls.buildLoadBalancer(ctx, name)
}

// lbResourceKinds are the load balancer sub-resource types, in the order they
// must be deleted: each references the one after it.
var lbResourceKinds = []string{"l4Route", "backendService", "backendPool"}

// Delete removes a load balancer and every resource it owns for the given
// cluster.
//
// Sub-resources are found by the capi_cluster-id label rather than by
// reconstructing their names. A load balancer with additional ports has a
// backend service and route per port, and any name-based scheme has to know
// every port to avoid orphaning them; selecting by owner label removes them all
// regardless of how many there are or how they are named. The cluster ID is the
// stable resource prefix, so this still matches after a clusterctl move (unlike
// the live UID, which changes).
//
// The load balancer and provider-managed public IP are named deterministically
// from the cluster and deleted by name, since they are the stable anchors the
// caller already knows. A referenced public IP is never deleted.
func (ls *LoadBalancerService) Delete(ctx context.Context, name, clusterID string, deletePublicIP bool) error {
	lbc := ls.lbClient()
	sdk := ls.client.SDKClient()

	var errs []error
	del := func(kind, resName string, err error) {
		if err != nil && !isNotFoundError(err) {
			errs = append(errs, fmt.Errorf("%s %q: %w", kind, resName, err))
		}
	}

	// The load balancer first, so its listeners stop referencing the routes.
	del("loadBalancer", name, lbc.LoadBalancers().Delete(ctx, name))

	// Then every sub-resource owned by this cluster, by label.
	for _, kind := range lbResourceKinds {
		names, err := ls.listByOwner(ctx, kind, clusterID)
		if err != nil {
			errs = append(errs, fmt.Errorf("list %s: %w", kind, err))
			continue
		}
		for _, resName := range names {
			del(kind, resName, ls.deleteResource(ctx, kind, resName))
		}
	}

	if deletePublicIP {
		del("publicIP", ls.ipName(name), sdk.Networking().PublicIPs().Delete(ctx, ls.ipName(name)))
	}

	if len(errs) > 0 {
		return fmt.Errorf("delete LB resources: %v", errs)
	}
	return nil
}

// DeletionComplete reports whether every managed load balancer resource is
// gone. The caller uses it to hold the finalizer until teardown has actually
// finished, rather than removing it as soon as the deletes are issued.
func (ls *LoadBalancerService) DeletionComplete(ctx context.Context, name, clusterID string, checkPublicIP bool) (bool, error) {
	exists, err := ls.exists(ctx, name, checkPublicIP)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	for _, kind := range lbResourceKinds {
		names, err := ls.listByOwner(ctx, kind, clusterID)
		if err != nil {
			return false, fmt.Errorf("list %s: %w", kind, err)
		}
		if len(names) > 0 {
			return false, nil
		}
	}
	return true, nil
}

// listByOwner returns the names of the given sub-resource kind labeled with the
// cluster UID.
func (ls *LoadBalancerService) listByOwner(ctx context.Context, kind, clusterID string) ([]string, error) {
	selector := ownerSelector(LabelClusterID, clusterID)
	lbc := ls.lbClient()

	switch kind {
	case "l4Route":
		list, err := lbc.L4Routes().List(ctx, selector)
		if err != nil {
			return nil, err
		}
		return l4RouteNames(list), nil
	case "backendService":
		list, err := lbc.BackendServices().List(ctx, selector)
		if err != nil {
			return nil, err
		}
		return backendServiceNames(list), nil
	case "backendPool":
		list, err := lbc.BackendPools().List(ctx, selector)
		if err != nil {
			return nil, err
		}
		return backendPoolNames(list), nil
	default:
		return nil, fmt.Errorf("unknown load balancer resource kind %q", kind)
	}
}

func (ls *LoadBalancerService) deleteResource(ctx context.Context, kind, name string) error {
	lbc := ls.lbClient()
	switch kind {
	case "l4Route":
		return lbc.L4Routes().Delete(ctx, name)
	case "backendService":
		return lbc.BackendServices().Delete(ctx, name)
	case "backendPool":
		return lbc.BackendPools().Delete(ctx, name)
	default:
		return fmt.Errorf("unknown load balancer resource kind %q", kind)
	}
}

func l4RouteNames(list *loadbalancer.L4routeList) []string {
	if list == nil {
		return nil
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Id)
	}
	return names
}

func backendServiceNames(list *loadbalancer.BackendserviceList) []string {
	if list == nil {
		return nil
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Id)
	}
	return names
}

func backendPoolNames(list *loadbalancer.BackendpoolList) []string {
	if list == nil {
		return nil
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Id)
	}
	return names
}

// List lists all load balancers.
func (ls *LoadBalancerService) List(ctx context.Context) ([]LoadBalancer, error) {
	list, err := ls.lbClient().LoadBalancers().List(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]LoadBalancer, 0, len(list.Items))
	for _, sdkLB := range list.Items {
		lb := LoadBalancer{
			Name: sdkLB.Metadata.Id,
			ID:   sdkLB.Metadata.Uid.String(),
		}
		if loadbalancer.IsReady(&sdkLB) {
			lb.Status = LoadBalancerStatusActive
		} else {
			lb.Status = LoadBalancerStatusCreating
		}
		result = append(result, lb)
	}

	return result, nil
}

// Exists checks if a load balancer or any of its sub-resources still exist.
// Returns true if any resource in the chain (LB, L4Route, BackendService,
// BackendPool, PublicIP) is still present. This ensures the controller
// doesn't remove the finalizer until all cloud resources are fully gone.
func (ls *LoadBalancerService) Exists(ctx context.Context, name string) (bool, error) {
	return ls.exists(ctx, name, true)
}

func (ls *LoadBalancerService) exists(ctx context.Context, name string, checkPublicIP bool) (bool, error) {
	lbc := ls.lbClient()
	ip, pool, svc, route := ls.ipName(name), ls.poolName(name), ls.svcName(name), ls.routeName(name)

	checks := []struct {
		name string
		fn   func() error
	}{
		{name, func() error { _, e := lbc.LoadBalancers().Get(ctx, name); return e }},
		{route, func() error { _, e := lbc.L4Routes().Get(ctx, route); return e }},
		{svc, func() error { _, e := lbc.BackendServices().Get(ctx, svc); return e }},
		{pool, func() error { _, e := lbc.BackendPools().Get(ctx, pool); return e }},
	}
	if checkPublicIP {
		checks = append(checks, struct {
			name string
			fn   func() error
		}{ip, func() error { _, e := ls.client.SDKClient().Networking().PublicIPs().Get(ctx, ip); return e }})
	}

	for _, c := range checks {
		if err := c.fn(); err == nil {
			return true, nil
		} else if !isNotFoundError(err) {
			return false, fmt.Errorf("checking %s: %w", c.name, err)
		}
	}

	return false, nil
}

// AddBackend adds a VM to the load balancer's backend pool (idempotent).
func (ls *LoadBalancerService) AddBackend(ctx context.Context, lbName string, backend Backend) error {
	poolName := ls.poolName(lbName)
	return ls.lbClient().BackendPools().AddBackendRef(ctx, poolName, ls.vmRef(backend.Name))
}

// RemoveBackend removes a VM from the load balancer's backend pool (idempotent).
func (ls *LoadBalancerService) RemoveBackend(ctx context.Context, lbName string, backendName string) error {
	poolName := ls.poolName(lbName)
	return ls.lbClient().BackendPools().RemoveBackendRef(ctx, poolName, ls.vmRef(backendName))
}

// ListBackends lists all backends registered with the load balancer.
func (ls *LoadBalancerService) ListBackends(ctx context.Context, lbName string) ([]Backend, error) {
	poolName := ls.poolName(lbName)

	pool, err := ls.lbClient().BackendPools().Get(ctx, poolName)
	if err != nil {
		return nil, err
	}

	if pool.Spec.BackendRefs == nil {
		return []Backend{}, nil
	}

	backends := make([]Backend, 0, len(*pool.Spec.BackendRefs))
	for _, ref := range *pool.Spec.BackendRefs {
		backends = append(backends, Backend{
			Name: evroc.NameFromRef(ref),
		})
	}

	return backends, nil
}

// WaitForReady waits for a load balancer to reach Ready state.
func (ls *LoadBalancerService) WaitForReady(ctx context.Context, name string, timeout time.Duration) (*LoadBalancer, error) {
	if _, err := ls.lbClient().LoadBalancers().WaitForReady(ctx, name, timeout); err != nil {
		return nil, fmt.Errorf("timeout waiting for load balancer %q to be ready: %w", name, err)
	}
	return ls.buildLoadBalancer(ctx, name)
}

// WaitForDeleted waits for a load balancer to be fully deleted.
func (ls *LoadBalancerService) WaitForDeleted(ctx context.Context, name string, timeout time.Duration) error {
	return ls.lbClient().LoadBalancers().WaitForDeleted(ctx, name, timeout)
}

// buildLoadBalancer constructs a complete LoadBalancer from the cloud API by
// fetching the LB and all sub-resources (PublicIP, BackendPool, BackendService,
// L4Route). Status is only Active when the entire chain is ready.
func (ls *LoadBalancerService) buildLoadBalancer(ctx context.Context, name string) (*LoadBalancer, error) {
	lbc := ls.lbClient()

	sdkLB, err := lbc.LoadBalancers().Get(ctx, name)
	if err != nil {
		return nil, err
	}

	lb := &LoadBalancer{
		Name:   sdkLB.Metadata.Id,
		ID:     sdkLB.Metadata.Uid.String(),
		Status: LoadBalancerStatusCreating,
	}

	if loadbalancer.IsReady(sdkLB) {
		lb.Status = LoadBalancerStatusActive
	}

	// Resolve IP address from the LB's publicIPRef
	if ipName := evroc.NameFromRef(sdkLB.Spec.PublicIPRef); ipName != "" {
		ip, err := ls.client.SDKClient().Networking().PublicIPs().Get(ctx, ipName)
		if err == nil {
			lb.Address = networking.GetPublicIPAddress(ip)
		}
	}

	// Resolve backends from the pool
	if pool, err := lbc.BackendPools().Get(ctx, ls.poolName(name)); err == nil {
		if pool.Spec.BackendRefs != nil {
			for _, ref := range *pool.Spec.BackendRefs {
				lb.Backends = append(lb.Backends, Backend{
					Name: evroc.NameFromRef(ref),
				})
			}
		}
	}

	return lb, nil
}

// apiServerListenerName is the name of the primary listener, whose backend
// service and route are the base-named resources (no port suffix).
const apiServerListenerName = "kube-apiserver"

func apiServerListener(port int32, routeRef string) lbtypes.LoadbalancerSpecListenersItem {
	name := apiServerListenerName
	refs := []string{routeRef}
	return lbtypes.LoadbalancerSpecListenersItem{
		Name:      &name,
		Port:      port,
		Protocol:  lbtypes.TCP,
		RouteRefs: &refs,
	}
}
