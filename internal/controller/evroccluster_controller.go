// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/evroc-oss/evroc-go-sdk/metrics"
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/controller/helpers"
	capipatch "sigs.k8s.io/cluster-api/util/patch"
)

const (
	clusterFinalizer = "evroccluster.infrastructure.cluster.x-k8s.io"
)

// PublicIPResolution describes how to handle the control plane public IP
type PublicIPResolution struct {
	Mode         PublicIPMode
	ResourceName string // Cloud resource name or CRD name
}

// clusterResourcePrefix returns a unique prefix for cluster-level cloud resources.
// It includes the first 8 characters of the cluster UID to prevent name collisions
// when a cluster is deleted and recreated with the same name. This follows the same
// pattern used by cluster-api-provider-hetzner (CAPH), which adds random suffixes to
// all managed cloud resources for the same reason.
//
// Without this, stale cloud resources from a deleted cluster (e.g. a public IP stuck
// in a broken state) would be blindly reused by a new cluster with the same name.
func clusterResourcePrefix(cluster *infrav1.EvrocCluster) (string, error) {
	if cluster.UID == "" {
		// UID is always set by the API server before persisting. If we get here,
		// something is seriously wrong — fail the reconciliation rather than creating
		// resources with colliding names (which is the exact bug this function prevents).
		return "", fmt.Errorf("cluster %s/%s has empty UID, cannot generate unique resource prefix", cluster.Namespace, cluster.Name)
	}
	return fmt.Sprintf("%s-%s", cluster.Name, cluster.UID[:8]), nil
}

// PublicIPMode defines the strategy for control plane public IP
type PublicIPMode string

const (
	PublicIPModeAutoCreate   PublicIPMode = "AutoCreate"   // Create cloud resource directly
	PublicIPModeUseExisting  PublicIPMode = "UseExisting"  // Use existing cloud resource
	PublicIPModeAutoDiscover PublicIPMode = "AutoDiscover" // Auto-discover from machines
)

// EvrocClusterReconciler reconciles an EvrocCluster object
type EvrocClusterReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	CloudClient cloud.ClientInterface
	SDKMetrics  *metrics.Manager
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=evrocclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=evrocclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=evrocclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;get;list;patch;update;watch

func (r *EvrocClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Fetch the EvrocCluster instance
	evrocCluster := &infrav1.EvrocCluster{}
	if err := r.Get(ctx, req.NamespacedName, evrocCluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check for pause (CAPI contract): check both the annotation on this object
	// and Spec.Paused on the owning Cluster. This must happen BEFORE deletion
	// handling so that clusterctl move does not trigger infrastructure cleanup.
	if helpers.IsPaused(ctx, r.Client, evrocCluster) {
		log.FromContext(ctx).Info("Reconciliation is paused for this object")
		return ctrl.Result{}, nil
	}

	// Resolve the cloud client for this cluster (per-cluster credentials or global fallback).
	cloudClient, err := r.resolveCloudClient(ctx, evrocCluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolving cloud credentials: %w", err)
	}

	// Handle deletion using helper
	if deleting, err := helpers.HandleDeletion(ctx, r.Client, evrocCluster, clusterFinalizer,
		func(ctx context.Context) error {
			return r.cleanupResources(ctx, evrocCluster, cloudClient)
		}); deleting {
		return ctrl.Result{}, err
	}

	// Ensure finalizer using helper
	if requeue, err := helpers.EnsureFinalizer(ctx, r.Client, evrocCluster, clusterFinalizer); err != nil || requeue {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, err
	}

	// Handle normal reconciliation
	return r.reconcileNormal(ctx, evrocCluster, cloudClient)
}

// resolveCloudClient returns a cloud client for the given cluster. If the
// cluster specifies a CredentialsRef, the secret is read and a new client
// is created. Otherwise the globally configured fallback client is returned.
func (r *EvrocClusterReconciler) resolveCloudClient(ctx context.Context, cluster *infrav1.EvrocCluster) (cloud.ClientInterface, error) {
	secretName := ""
	secretNamespace := cluster.Namespace
	if ref := cluster.Spec.CredentialsRef; ref != nil {
		secretName = ref.Name
		if ref.Namespace != "" {
			secretNamespace = ref.Namespace
		}
	}
	return cloud.ClientForCluster(ctx, r.Client, r.CloudClient, secretName, secretNamespace, r.SDKMetrics)
}

func (r *EvrocClusterReconciler) reconcileNormal(ctx context.Context, evrocCluster *infrav1.EvrocCluster, cloudClient cloud.ClientInterface) (_ ctrl.Result, retErr error) {
	log := log.FromContext(ctx)

	// Snapshot the object before mutations. The deferred Patch computes a
	// diff (spec and status separately) and sends a single pair of patches
	// at the end, eliminating intermediate Status().Update() conflicts.
	patchHelper, err := capipatch.NewHelper(evrocCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if err := patchHelper.Patch(ctx, evrocCluster); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()

	log.Info("Reconciling EvrocCluster",
		"project", evrocCluster.Spec.Project,
		"region", evrocCluster.Spec.Region)

	// Set up failure domains based on the region.
	if err := r.reconcileFailureDomains(evrocCluster); err != nil {
		log.Error(err, "Failed to reconcile failure domains")
		return ctrl.Result{}, err
	}

	// Reconcile PublicIP if endpoint not yet set or if the PublicIP status was
	// lost (e.g. due to a conflict during a previous reconcile).
	publicIPMissing := evrocCluster.Status.Resources == nil ||
		evrocCluster.Status.Resources.PublicIP == nil
	if evrocCluster.Spec.ControlPlaneEndpoint.IsZero() || publicIPMissing {
		resolution, err := resolvePublicIPConfig(evrocCluster)
		if err != nil {
			return ctrl.Result{}, err
		}

		switch resolution.Mode {
		case PublicIPModeAutoCreate:
			result, err := r.reconcileAutoCreatedPublicIP(ctx, evrocCluster, resolution.ResourceName, cloudClient)
			if err != nil || !result.IsZero() {
				return result, err
			}

		case PublicIPModeUseExisting:
			result, err := r.reconcileExistingPublicIP(ctx, evrocCluster, resolution.ResourceName, cloudClient)
			if err != nil || !result.IsZero() {
				return result, err
			}

		case PublicIPModeAutoDiscover:
			// Fall through to machine auto-discovery below
		}
	}

	// Reconcile SecurityGroups
	if err := r.reconcileSecurityGroups(ctx, evrocCluster, cloudClient); err != nil {
		log.Error(err, "Failed to reconcile security groups")
		return ctrl.Result{}, err
	}

	// Mark infrastructure as provisioned and ready.
	// The infrastructure layer has no resources to provision before machines exist (no VPC, no LB).
	// CAPRKE2 requires Ready=true before it will create control plane machines.
	provisioned := true
	evrocCluster.Status.Initialization.Provisioned = &provisioned
	evrocCluster.Status.Initialization.InfrastructureProvisioned = &provisioned
	evrocCluster.Status.Ready = true

	// When no public IP is configured, discover the endpoint from the first
	// control-plane machine's private address. When a public IP IS configured
	// (and the endpoint is already set), leave it alone — the management
	// cluster and CAPI controllers need the public IP to reach the workload
	// cluster's API server.
	if evrocCluster.Spec.ControlPlaneEndpoint.IsZero() {
		result, err := r.reconcileControlPlaneEndpointFromMachines(ctx, evrocCluster)
		if err != nil {
			return result, err
		}
	}
	if evrocCluster.Spec.ControlPlaneEndpoint.IsZero() {
		log.Info("Waiting for control plane endpoint")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	log.Info("EvrocCluster is ready",
		"endpoint", evrocCluster.Spec.ControlPlaneEndpoint.String())

	return ctrl.Result{}, nil
}

// reconcileFailureDomains sets up the failure domains based on the spec
func (r *EvrocClusterReconciler) reconcileFailureDomains(evrocCluster *infrav1.EvrocCluster) error {
	// Use zones from spec (simple format: "a", "b", "c")
	// Create a slice of FailureDomain objects (v1beta2 uses list instead of map)
	failureDomains := make([]clusterv1.FailureDomain, 0, len(evrocCluster.Spec.FailureDomains))

	for _, zone := range evrocCluster.Spec.FailureDomains {
		controlPlane := true
		failureDomains = append(failureDomains, clusterv1.FailureDomain{
			Name:         zone,
			ControlPlane: &controlPlane, // All zones can host control plane
		})
	}

	evrocCluster.Status.FailureDomains = failureDomains
	return nil
}

// reconcileControlPlaneEndpointFromMachines discovers the controlPlaneEndpoint from the
// first EvrocMachine with the control-plane label that has discovered addresses.
//
// Note: control-plane label matching must check key existence (not value equality),
// because CAPI sets `cluster.x-k8s.io/control-plane=true`.
func (r *EvrocClusterReconciler) reconcileControlPlaneEndpointFromMachines(
	ctx context.Context, evrocCluster *infrav1.EvrocCluster,
) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Use the CAPI cluster name label (set by CAPI on infra objects) rather
	// than assuming the EvrocCluster name matches the CAPI Cluster name.
	clusterName := evrocCluster.Name
	if name, ok := evrocCluster.Labels[clusterv1.ClusterNameLabel]; ok && name != "" {
		clusterName = name
	}

	machineList := &infrav1.EvrocMachineList{}
	if err := r.List(ctx, machineList,
		client.InNamespace(evrocCluster.Namespace),
		client.MatchingLabels{
			clusterv1.ClusterNameLabel: clusterName,
		}); err != nil {
		return ctrl.Result{}, err
	}

	for _, m := range machineList.Items {
		if _, isControlPlane := m.Labels[clusterv1.MachineControlPlaneLabel]; !isControlPlane {
			continue
		}
		// Don't require Ready=true here. We only need a discovered private address,
		// and publishing it early prevents worker bootstrap data from being generated
		// against a temporary public endpoint.
		if len(m.Status.Addresses) == 0 {
			continue
		}
		ip := firstUsableIP(m.Status.Addresses)
		if ip == "" {
			continue
		}
		log.Info("Auto-populating controlPlaneEndpoint from control-plane machine",
			"machine", m.Name, "ip", ip)
		evrocCluster.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{
			Host: ip,
			Port: 6443,
		}
		return ctrl.Result{}, nil
	}

	log.Info("No control-plane machine with addresses found yet, waiting")
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// firstUsableIP prefers InternalIP for cluster-internal communication.
// This ensures nodes communicate over the private network while external
// access (kubectl, Rancher) can still use the public IP if configured.
func firstUsableIP(addrs []corev1.NodeAddress) string {
	// Prefer InternalIP for control plane endpoint
	for _, addr := range addrs {
		if addr.Type == corev1.NodeInternalIP && addr.Address != "" {
			return addr.Address
		}
	}
	// Fallback to ExternalIP if no internal IP available
	for _, addr := range addrs {
		if addr.Type == corev1.NodeExternalIP && addr.Address != "" {
			return addr.Address
		}
	}
	return ""
}

// resolvePublicIPConfig determines how to handle public IP based on cluster configuration.
// Priority order: inline enabled > inline existingName > none (auto-discover from machines)
func resolvePublicIPConfig(cluster *infrav1.EvrocCluster) (PublicIPResolution, error) {
	// Priority 1: controlPlaneConfig.publicIP.enabled
	if cluster.Spec.ControlPlaneConfig != nil &&
		cluster.Spec.ControlPlaneConfig.PublicIP != nil &&
		cluster.Spec.ControlPlaneConfig.PublicIP.Enabled {
		prefix, err := clusterResourcePrefix(cluster)
		if err != nil {
			return PublicIPResolution{}, err
		}
		return PublicIPResolution{
			Mode:         PublicIPModeAutoCreate,
			ResourceName: fmt.Sprintf("%s-cp-ip", prefix),
		}, nil
	}

	// Priority 2: controlPlaneConfig.publicIP.existingName
	if cluster.Spec.ControlPlaneConfig != nil &&
		cluster.Spec.ControlPlaneConfig.PublicIP != nil &&
		cluster.Spec.ControlPlaneConfig.PublicIP.ExistingName != nil {
		return PublicIPResolution{
			Mode:         PublicIPModeUseExisting,
			ResourceName: *cluster.Spec.ControlPlaneConfig.PublicIP.ExistingName,
		}, nil
	}

	// Priority 3: None - auto-discover from machines
	return PublicIPResolution{Mode: PublicIPModeAutoDiscover}, nil
}

// reconcileAutoCreatedPublicIP creates a cloud public IP directly via SDK.
// The IP is created in the evroc cloud and tracked in cluster.status.resources.
func (r *EvrocClusterReconciler) reconcileAutoCreatedPublicIP(
	ctx context.Context,
	cluster *infrav1.EvrocCluster,
	resourceName string,
	cloudClient cloud.ClientInterface,
) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Check if we already created it (stored in status)
	if cluster.Status.Resources != nil &&
		cluster.Status.Resources.PublicIP != nil &&
		cluster.Status.Resources.PublicIP.Managed {

		existingID := cluster.Status.Resources.PublicIP.ID
		log.V(1).Info("Managed PublicIP already exists in status", "id", existingID)

		// Verify it still exists in cloud
		cloudIP, err := cloudClient.PublicIPs().Get(ctx, existingID)
		if err != nil {
			if cloud.IsNotFoundError(err) {
				// Cloud resource deleted outside CAPI - clear status and requeue to recreate
				log.Info("Managed PublicIP deleted outside CAPI, will recreate", "id", existingID)
				cluster.Status.Resources.PublicIP = nil
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to verify managed PublicIP: %w", err)
		}

		// Still exists - use it
		return r.usePublicIP(ctx, cluster, cloudIP, true)
	}

	// Check if PublicIP already exists in cloud (e.g., from previous reconciliation before status was lost)
	log.V(1).Info("Checking if PublicIP already exists in cloud", "name", resourceName)
	cloudIP, err := cloudClient.PublicIPs().Get(ctx, resourceName)
	if err != nil {
		if !cloud.IsNotFoundError(err) {
			return ctrl.Result{}, fmt.Errorf("failed to check for existing public IP: %w", err)
		}

		// Not found - create new cloud resource
		log.Info("Creating managed PublicIP in cloud", "name", resourceName)
		cloudIP, err = cloudClient.PublicIPs().Create(ctx, resourceName, helpers.ResourceLabels(cluster.Name, string(cluster.UID), cluster.Spec.AdditionalLabels))
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create public IP: %w", err)
		}
	} else {
		// Found existing resource - use it
		log.Info("Found existing managed PublicIP in cloud, will use it", "name", resourceName)
	}

	// Wait for cloud resource to have an address allocated
	if cloudIP.Status.PublicIPv4Address == nil || *cloudIP.Status.PublicIPv4Address == "" {
		log.Info("Waiting for cloud PublicIP address to be allocated", "name", resourceName)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Store in status and use
	return r.usePublicIP(ctx, cluster, cloudIP, true)
}

// reconcileExistingPublicIP uses a pre-existing cloud resource (not managed by CAPI).
// This supports Terraform/external infrastructure integration.
func (r *EvrocClusterReconciler) reconcileExistingPublicIP(
	ctx context.Context,
	cluster *infrav1.EvrocCluster,
	existingName string,
	cloudClient cloud.ClientInterface,
) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info("Using existing PublicIP", "name", existingName)

	// Query cloud API for the existing resource
	cloudIP, err := cloudClient.PublicIPs().Get(ctx, existingName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("existing public IP %q not found: %w", existingName, err)
	}

	if cloudIP.Status.PublicIPv4Address == nil || *cloudIP.Status.PublicIPv4Address == "" {
		log.Info("Waiting for existing PublicIP address to be allocated", "name", existingName)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Store reference (marked as NOT managed)
	return r.usePublicIP(ctx, cluster, cloudIP, false)
}

// usePublicIP stores PublicIP information in cluster status and sets controlPlaneEndpoint.
// All mutations are on the in-memory object; the deferred patchHelper persists them.
func (r *EvrocClusterReconciler) usePublicIP(
	ctx context.Context,
	cluster *infrav1.EvrocCluster,
	cloudIP *networkingtypes.PublicIP,
	managed bool,
) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if cloudIP.Status.PublicIPv4Address == nil || *cloudIP.Status.PublicIPv4Address == "" {
		log.V(1).Info("PublicIP address not yet allocated")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	address := *cloudIP.Status.PublicIPv4Address
	ipName := cloudIP.Metadata.Id

	// Set controlPlaneEndpoint to the public IP if not already set.
	if cluster.Spec.ControlPlaneEndpoint.IsZero() {
		cluster.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{
			Host: address,
			Port: 6443,
		}
		log.Info("Set controlPlaneEndpoint to public IP",
			"address", address, "managed", managed)
	}

	// Store PublicIP info in status.
	if cluster.Status.Resources == nil {
		cluster.Status.Resources = &infrav1.ClusterResources{}
	}
	cluster.Status.Resources.PublicIP = &infrav1.ManagedPublicIP{
		ID:      ipName,
		Address: address,
		Name:    ipName,
		Managed: managed,
	}

	return ctrl.Result{}, nil
}

// reconcileSecurityGroups reconciles security groups across all three sections
// (common, controlPlane, worker). Creates inline security groups, tracks existing
// ones in status, and deletes cloud resources for inline SGs removed from the spec.
func (r *EvrocClusterReconciler) reconcileSecurityGroups(ctx context.Context, cluster *infrav1.EvrocCluster, cloudClient cloud.ClientInterface) error {
	log := log.FromContext(ctx)

	// If SG config was removed entirely, clean up all previously managed SGs.
	if cluster.Spec.SecurityGroups == nil {
		if err := r.deleteStaleSecurityGroups(ctx, cluster, cloudClient, nil); err != nil {
			return err
		}
		if cluster.Status.Resources != nil && len(cluster.Status.Resources.SecurityGroups) > 0 {
			cluster.Status.Resources.SecurityGroups = nil
			if err := r.reconcileClusterSecurityGroupsOnVMs(ctx, cluster, cloudClient); err != nil {
				return fmt.Errorf("failed to propagate empty SG list to VMs: %w", err)
			}
		}
		return nil
	}

	managedSGs := make([]infrav1.ManagedSecurityGroup, 0)

	// Compute prefix once if any section has inline SGs.
	sgCfg := cluster.Spec.SecurityGroups
	needsPrefix := (sgCfg.Common != nil && len(sgCfg.Common.InlineSecurityGroups) > 0) ||
		(sgCfg.ControlPlane != nil && len(sgCfg.ControlPlane.InlineSecurityGroups) > 0) ||
		(sgCfg.Worker != nil && len(sgCfg.Worker.InlineSecurityGroups) > 0)

	var prefix string
	if needsPrefix {
		var err error
		prefix, err = clusterResourcePrefix(cluster)
		if err != nil {
			return err
		}
	}

	// Process each section: common, controlPlane, worker.
	sections := []struct {
		role   string
		config *infrav1.SecurityGroupsConfig
	}{
		{"common", sgCfg.Common},
		{"controlPlane", sgCfg.ControlPlane},
		{"worker", sgCfg.Worker},
	}

	for _, section := range sections {
		if section.config == nil {
			continue
		}
		sgs, err := r.reconcileSecurityGroupSection(ctx, cluster, cloudClient, prefix, section.role, section.config)
		if err != nil {
			return err
		}
		managedSGs = append(managedSGs, sgs...)
	}

	// Delete cloud resources for managed SGs that were removed from the spec.
	if err := r.deleteStaleSecurityGroups(ctx, cluster, cloudClient, managedSGs); err != nil {
		return err
	}

	// Update status
	if cluster.Status.Resources == nil {
		cluster.Status.Resources = &infrav1.ClusterResources{}
	}
	cluster.Status.Resources.SecurityGroups = managedSGs

	// Update VMs that inherit cluster security groups.
	if err := r.reconcileClusterSecurityGroupsOnVMs(ctx, cluster, cloudClient); err != nil {
		return fmt.Errorf("failed to propagate cluster SGs to VMs: %w", err)
	}

	log.Info("Reconciled security groups", "count", len(managedSGs))
	return nil
}

// reconcileSecurityGroupSection reconciles a single SG section (common/controlPlane/worker).
func (r *EvrocClusterReconciler) reconcileSecurityGroupSection(
	ctx context.Context,
	cluster *infrav1.EvrocCluster,
	cloudClient cloud.ClientInterface,
	prefix, role string,
	sgConfig *infrav1.SecurityGroupsConfig,
) ([]infrav1.ManagedSecurityGroup, error) {
	log := log.FromContext(ctx)
	var managed []infrav1.ManagedSecurityGroup

	for _, inlineSG := range sgConfig.InlineSecurityGroups {
		sgName := fmt.Sprintf("%s-%s", prefix, inlineSG.Name)
		sdkRules := helpers.ConvertSecurityGroupRulesToSDK(inlineSG.Rules)

		exists, err := cloudClient.SecurityGroups().Exists(ctx, sgName)
		if err != nil {
			return nil, fmt.Errorf("failed to check security group existence: %w", err)
		}

		if !exists {
			log.Info("Creating inline security group", "name", sgName, "role", role, "rules", len(inlineSG.Rules))
			_, err := cloudClient.SecurityGroups().Create(ctx, sgName, sdkRules, helpers.ResourceLabels(cluster.Name, string(cluster.UID), cluster.Spec.AdditionalLabels))
			if err != nil {
				return nil, fmt.Errorf("failed to create security group %s: %w", sgName, err)
			}
		} else {
			log.V(1).Info("Updating inline security group", "name", sgName, "role", role, "rules", len(inlineSG.Rules))
			existingSG, err := cloudClient.SecurityGroups().Get(ctx, sgName)
			if err != nil {
				return nil, fmt.Errorf("failed to get security group %s: %w", sgName, err)
			}
			existingSG.Spec.Rules = &sdkRules
			_, err = cloudClient.SecurityGroups().Update(ctx, sgName, existingSG)
			if err != nil {
				return nil, fmt.Errorf("failed to update security group %s: %w", sgName, err)
			}
		}

		managed = append(managed, infrav1.ManagedSecurityGroup{
			ID: sgName, Name: sgName, Managed: true, Role: role,
		})
	}

	for _, existingName := range sgConfig.ExistingNames {
		exists, err := cloudClient.SecurityGroups().Exists(ctx, existingName)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing security group %s: %w", existingName, err)
		}
		if !exists {
			return nil, fmt.Errorf("existing security group %s not found", existingName)
		}

		managed = append(managed, infrav1.ManagedSecurityGroup{
			ID: existingName, Name: existingName, Managed: false, Role: role,
		})
	}

	return managed, nil
}

// deleteStaleSecurityGroups removes cloud resources for managed SGs that exist in the
// old status but are no longer in the desired list. This prevents orphaned cloud
// resources when a user removes an inline SG from the spec.
func (r *EvrocClusterReconciler) deleteStaleSecurityGroups(ctx context.Context, cluster *infrav1.EvrocCluster, cloudClient cloud.ClientInterface, desired []infrav1.ManagedSecurityGroup) error {
	if cluster.Status.Resources == nil {
		return nil
	}

	log := log.FromContext(ctx)

	desiredNames := make(map[string]bool, len(desired))
	for _, sg := range desired {
		desiredNames[sg.Name] = true
	}

	for _, old := range cluster.Status.Resources.SecurityGroups {
		if !old.Managed {
			continue // never delete externally-managed SGs
		}
		if desiredNames[old.Name] {
			continue // still desired
		}
		log.Info("Deleting stale managed security group", "name", old.Name)
		if err := cloudClient.SecurityGroups().Delete(ctx, old.Name); err != nil {
			if !helpers.IsNotFoundError(err) {
				return fmt.Errorf("failed to delete stale security group %s: %w", old.Name, err)
			}
		}
	}
	return nil
}

// reconcileClusterSecurityGroupsOnVMs triggers a reconcile of machines that inherit
// cluster security groups by annotating them. The actual SG update is performed
// by the machine controller's reconcileVMSecurityGroups, which is the single owner
// of VM security group state.
func (r *EvrocClusterReconciler) reconcileClusterSecurityGroupsOnVMs(ctx context.Context, cluster *infrav1.EvrocCluster, _ cloud.ClientInterface) error {
	log := log.FromContext(ctx)

	// Use the CAPI cluster name label (set by CAPI on infra objects) rather
	// than assuming the EvrocCluster name matches the CAPI Cluster name.
	clusterName := cluster.Name
	if name, ok := cluster.Labels[clusterv1.ClusterNameLabel]; ok && name != "" {
		clusterName = name
	}

	machineList := &infrav1.EvrocMachineList{}
	if err := r.List(ctx, machineList, client.InNamespace(cluster.Namespace), client.MatchingLabels{
		clusterv1.ClusterNameLabel: clusterName,
	}); err != nil {
		return fmt.Errorf("failed to list machines: %w", err)
	}

	touchedCount := 0
	for i := range machineList.Items {
		machine := &machineList.Items[i]

		if machine.Spec.NetworkingConfig == nil ||
			machine.Spec.NetworkingConfig.SecurityGroups == nil ||
			!machine.Spec.NetworkingConfig.SecurityGroups.InheritFromCluster {
			continue
		}

		if machine.Status.MachineID == "" {
			continue
		}

		// Annotate the machine to trigger a reconcile by the machine controller,
		// which owns the actual UpdateSecurityGroups call.
		annotations := machine.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations["infrastructure.cluster.x-k8s.io/sg-sync"] = cluster.ResourceVersion
		machine.SetAnnotations(annotations)
		if err := r.Update(ctx, machine); err != nil {
			log.Error(err, "Failed to trigger machine SG reconcile", "machine", machine.Name)
			continue
		}
		touchedCount++
	}

	if touchedCount > 0 {
		log.Info("Triggered SG reconcile on machines", "count", touchedCount)
	}

	return nil
}

// clusterSecurityGroupNamesForRole returns SG names for a machine based on its role.
// Control plane machines get "common" + "controlPlane" SGs.
// Worker machines get "common" + "worker" SGs.
func clusterSecurityGroupNamesForRole(cluster *infrav1.EvrocCluster, isControlPlane bool) []string {
	if cluster == nil || cluster.Status.Resources == nil {
		return nil
	}

	roleFilter := "worker"
	if isControlPlane {
		roleFilter = "controlPlane"
	}

	var names []string
	for _, sg := range cluster.Status.Resources.SecurityGroups {
		if sg.Role == "common" || sg.Role == roleFilter {
			names = append(names, sg.Name)
		}
	}
	return names
}

// cleanupResources deletes cloud resources that were created and owned by CAPI.
// External resources (managed=false) are not deleted.
//
// This function is non-blocking: it fires off delete requests, then checks if
// resources still exist. If any resource is still being deleted, it returns an
// error so the controller requeues without blocking other reconciles.
func (r *EvrocClusterReconciler) cleanupResources(ctx context.Context, cluster *infrav1.EvrocCluster, cloudClient cloud.ClientInterface) error {
	log := log.FromContext(ctx)

	if cluster.Status.Resources == nil {
		return nil
	}

	var errs []error
	stillDeleting := 0

	// Delete managed PublicIP.
	if pip := cluster.Status.Resources.PublicIP; pip != nil && pip.Managed {
		log.Info("Deleting managed PublicIP", "id", pip.ID, "name", pip.Name)
		if err := cloudClient.PublicIPs().Delete(ctx, pip.Name); err != nil {
			if !cloud.IsNotFoundError(err) {
				errs = append(errs, fmt.Errorf("failed to delete managed PublicIP %s: %w", pip.Name, err))
			}
		}
		// Check if it's gone yet.
		if exists, err := cloudClient.PublicIPs().Exists(ctx, pip.Name); err != nil {
			errs = append(errs, fmt.Errorf("failed to check PublicIP %s existence: %w", pip.Name, err))
		} else if exists {
			stillDeleting++
		}
	}

	// Delete managed SecurityGroups.
	for _, sg := range cluster.Status.Resources.SecurityGroups {
		if !sg.Managed {
			continue
		}
		log.Info("Deleting managed SecurityGroup", "id", sg.ID, "name", sg.Name)
		if err := cloudClient.SecurityGroups().Delete(ctx, sg.Name); err != nil {
			if !cloud.IsNotFoundError(err) {
				errs = append(errs, fmt.Errorf("failed to delete managed SecurityGroup %s: %w", sg.Name, err))
			}
		}
		// Check if it's gone yet.
		if exists, err := cloudClient.SecurityGroups().Exists(ctx, sg.Name); err != nil {
			errs = append(errs, fmt.Errorf("failed to check SecurityGroup %s existence: %w", sg.Name, err))
		} else if exists {
			stillDeleting++
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if stillDeleting > 0 {
		return fmt.Errorf("%d cloud resource(s) still being deleted, will requeue", stillDeleting)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EvrocClusterReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.EvrocCluster{}).
		Watches(
			&infrav1.EvrocMachine{},
			handler.EnqueueRequestsFromMapFunc(r.machineToCluster),
		).
		Complete(r)
}

// machineToCluster maps an EvrocMachine event to its owning EvrocCluster.
// It resolves the CAPI Cluster's InfrastructureRef to find the correct
// EvrocCluster name, rather than assuming it matches the CAPI Cluster name.
func (r *EvrocClusterReconciler) machineToCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	clusterName, ok := obj.GetLabels()[clusterv1.ClusterNameLabel]
	if !ok || clusterName == "" {
		return nil
	}

	capiCluster := &clusterv1.Cluster{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      clusterName,
		Namespace: obj.GetNamespace(),
	}, capiCluster); err != nil {
		return nil
	}

	infraName := capiCluster.Spec.InfrastructureRef.Name
	if infraName == "" {
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      infraName,
				Namespace: obj.GetNamespace(),
			},
		},
	}
}
