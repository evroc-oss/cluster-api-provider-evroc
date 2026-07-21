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

	// 3. Create BackendService with TCP health check on the backend port
	svc, err := loadbalancer.NewBackendServiceBuilder(svcName).
		WithPort(request.BackendPort).
		WithBackendPoolRef(pool.Ref()).
		WithTCPHealthCheck().
		WithLabels(request.Labels).
		Create(ctx, lbc.BackendServices())
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

	// 5. Create additional port chains (e.g. 9345 for RKE2 supervisor)
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
	_, err = lbBuilder.Create(ctx, lbc.LoadBalancers())
	if err != nil && !isConflictError(err) {
		return nil, fmt.Errorf("failed to create load balancer: %w", err)
	}

	return ls.buildLoadBalancer(ctx, request.Name)
}

// Get retrieves a load balancer by name.
func (ls *LoadBalancerService) Get(ctx context.Context, name string) (*LoadBalancer, error) {
	return ls.buildLoadBalancer(ctx, name)
}

// Delete deletes a load balancer and all sub-resources.
// All delete requests are fired immediately — the API accepts them and
// resources enter a pending-delete state until their dependents are gone.
// We only wait on the LB itself (top of the chain) to confirm full teardown.
func (ls *LoadBalancerService) Delete(ctx context.Context, name string) error {
	lbc := ls.lbClient()
	sdk := ls.client.SDKClient()

	deletions := []struct {
		name string
		fn   func() error
	}{
		{"loadBalancer", func() error { return lbc.LoadBalancers().Delete(ctx, name) }},
		{"l4Route", func() error { return lbc.L4Routes().Delete(ctx, ls.routeName(name)) }},
		{"backendService", func() error { return lbc.BackendServices().Delete(ctx, ls.svcName(name)) }},
		{"backendPool", func() error { return lbc.BackendPools().Delete(ctx, ls.poolName(name)) }},
		{"publicIP", func() error { return sdk.Networking().PublicIPs().Delete(ctx, ls.ipName(name)) }},
	}

	var errs []error
	for _, d := range deletions {
		if err := d.fn(); err != nil && !isNotFoundError(err) {
			errs = append(errs, fmt.Errorf("%s: %w", d.name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("delete LB resources: %v", errs)
	}

	return nil
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
		{ip, func() error { _, e := ls.client.SDKClient().Networking().PublicIPs().Get(ctx, ip); return e }},
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

func apiServerListener(port int32, routeRef string) lbtypes.LoadbalancerSpecListenersItem {
	name := "kube-apiserver"
	refs := []string{routeRef}
	return lbtypes.LoadbalancerSpecListenersItem{
		Name:      &name,
		Port:      port,
		Protocol:  lbtypes.TCP,
		RouteRefs: &refs,
	}
}
