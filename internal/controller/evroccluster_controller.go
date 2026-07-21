// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/evroc-oss/evroc-go-sdk/metrics"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/controller/helpers"
	capipatch "sigs.k8s.io/cluster-api/util/patch"
)

const (
	clusterFinalizer = "evroccluster.infrastructure.cluster.x-k8s.io"

	// clusterResourcePrefixAnnotation preserves the identity of Evroc-managed
	// cloud resources across clusterctl move. The move recreates EvrocCluster
	// with a new UID and without status, but preserves metadata annotations.
	clusterResourcePrefixAnnotation = "evroccluster.infrastructure.cluster.x-k8s.io/resource-prefix"
)

// EvrocClusterReconciler reconciles an EvrocCluster object
type EvrocClusterReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	SDKMetrics *metrics.Manager

	// clientFactory builds the cloud client for a cluster. Defaults to
	// cloud.ClientForCluster; tests override it to inject a fake.
	clientFactory clusterClientFactory
}

// clusterClientFactory matches the signature of cloud.ClientForCluster.
type clusterClientFactory func(
	ctx context.Context,
	k8sClient client.Reader,
	secretName, secretNamespace string,
	clusterCtx cloud.ClusterContext,
	m *metrics.Manager,
) (cloud.ClientInterface, error)

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
		setClusterCondition(evrocCluster, infrav1.PausedCondition, corev1.ConditionTrue, infrav1.PausedReason, "Reconciliation is paused")
		if err := r.Status().Update(ctx, evrocCluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating paused condition: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Resolve the cloud client for this cluster from its credentialsRef.
	cloudClient, err := r.resolveCloudClient(ctx, evrocCluster)
	if err != nil {
		// Surface the cause on the object. Without this the controller retries
		// silently and the only clue is in its logs, which looks identical to
		// "still provisioning" from the outside.
		setClusterCondition(evrocCluster, infrav1.ClusterReadyCondition, corev1.ConditionFalse,
			infrav1.CredentialsNotFoundReason, err.Error())
		credentialErr := fmt.Errorf("resolving cloud credentials: %w", err)
		if statusErr := r.Status().Update(ctx, evrocCluster); statusErr != nil {
			return ctrl.Result{}, errors.Join(credentialErr, fmt.Errorf("updating credential failure condition: %w", statusErr))
		}
		return ctrl.Result{}, credentialErr
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

	// Mark the Paused condition as False now that reconciliation has resumed.
	setClusterCondition(evrocCluster, infrav1.PausedCondition, corev1.ConditionFalse, "Reconciling", "Reconciliation is active")

	// Keep an owned copy of the credential secret so clusterctl move carries the
	// credentials with the cluster, without taking ownership of the user's secret.
	if err := r.ensureCredentialSecretCopy(ctx, evrocCluster); err != nil {
		log.Error(err, "Failed to copy credential secret")
		// Non-fatal: the cluster still reconciles using the referenced secret.
	}

	// Set up failure domains based on the region.
	if err := r.reconcileFailureDomains(evrocCluster); err != nil {
		log.Error(err, "Failed to reconcile failure domains")
		return ctrl.Result{}, err
	}

	// Reconcile control plane load balancer (auto-created or existing).
	// Reconcile control plane load balancer (always auto-created).
	{
		lbName, err := resolveLoadBalancerName(evrocCluster)
		if err != nil {
			return ctrl.Result{}, err
		}
		result, err := r.reconcileAutoCreatedLoadBalancer(ctx, evrocCluster, lbName, cloudClient)
		if err != nil || !result.IsZero() {
			return result, err
		}
	}

	// Reconcile SecurityGroups
	if err := r.reconcileSecurityGroups(ctx, evrocCluster, cloudClient); err != nil {
		log.Error(err, "Failed to reconcile security groups")
		return ctrl.Result{}, err
	}

	// Mark infrastructure as provisioned and ready.
	// CAPRKE2 requires Ready=true before it will create control plane machines.
	provisioned := true
	evrocCluster.Status.Initialization.Provisioned = &provisioned
	evrocCluster.Status.Initialization.InfrastructureProvisioned = &provisioned
	evrocCluster.Status.Ready = true

	if evrocCluster.Spec.ControlPlaneEndpoint.IsZero() {
		log.Info("Waiting for control plane endpoint (LoadBalancer not yet ready)")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	log.Info("EvrocCluster is ready",
		"endpoint", evrocCluster.Spec.ControlPlaneEndpoint.String())

	return ctrl.Result{}, nil
}

// resolveLoadBalancerName returns the deterministic LB name for this cluster.
func resolveLoadBalancerName(cluster *infrav1.EvrocCluster) (string, error) {
	prefix, err := clusterResourcePrefix(cluster)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-cp-lb", prefix), nil
}

// buildLoadBalancerCreateRequest creates a LoadBalancerCreateRequest from the cluster config.
func buildLoadBalancerCreateRequest(cluster *infrav1.EvrocCluster, resourceName string) *cloud.LoadBalancerCreateRequest {
	req := &cloud.LoadBalancerCreateRequest{
		Name:        resourceName,
		Port:        6443,
		BackendPort: 6443,
		Labels:      helpers.ResourceLabels(cluster.Name, string(cluster.UID), cluster.Spec.AdditionalLabels),
	}
	if lbCfg := cluster.Spec.ControlPlaneConfig.GetLoadBalancer(); lbCfg != nil {
		if lbCfg.ExistingPublicIPID != nil {
			req.ExistingPublicIPID = *lbCfg.ExistingPublicIPID
		}
		req.AdditionalPorts = lbCfg.AdditionalPorts
	}
	return req
}

// reconcileAutoCreatedLoadBalancer creates and manages a load balancer for the control plane endpoint.
func (r *EvrocClusterReconciler) reconcileAutoCreatedLoadBalancer(
	ctx context.Context,
	cluster *infrav1.EvrocCluster,
	resourceName string,
	cloudClient cloud.ClientInterface,
) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Check cloud API directly — do not use status as source of truth.
	log.V(1).Info("Checking if LoadBalancer exists in cloud", "name", resourceName)
	lb, err := cloudClient.LoadBalancers().Get(ctx, resourceName)
	if err != nil {
		if !cloud.IsNotFoundError(err) {
			return ctrl.Result{}, fmt.Errorf("failed to check for existing load balancer: %w", err)
		}

		// Not found — create.
		log.Info("Creating managed LoadBalancer in cloud", "name", resourceName)
		request := buildLoadBalancerCreateRequest(cluster, resourceName)
		lb, err = cloudClient.LoadBalancers().Create(ctx, request)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create load balancer: %w", err)
		}
	} else {
		log.Info("Found existing managed LoadBalancer in cloud, will use it", "name", resourceName)
	}

	// Wait for LB to be active and have an address.
	if lb.Address == "" || !lb.IsActive() {
		log.Info("Waiting for LoadBalancer to become active", "name", resourceName, "status", lb.Status)
		setClusterCondition(cluster, infrav1.LoadBalancerReadyCondition, corev1.ConditionFalse,
			infrav1.LoadBalancerProvisioningReason, "Load balancer is being provisioned")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	return r.useLoadBalancer(ctx, cluster, lb)
}

// reconcileExistingLoadBalancer uses a pre-existing load balancer (not managed by CAPI).
// useLoadBalancer stores LoadBalancer information in cluster status and sets controlPlaneEndpoint.
func (r *EvrocClusterReconciler) useLoadBalancer(
	_ context.Context,
	cluster *infrav1.EvrocCluster,
	lb *cloud.LoadBalancer,
) (ctrl.Result, error) {
	if lb.Address == "" {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Set controlPlaneEndpoint to the LB address.
	if cluster.Spec.ControlPlaneEndpoint.IsZero() {
		cluster.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{
			Host: lb.Address,
			Port: 6443,
		}
	}

	// Store LB info in status.
	if cluster.Status.Resources == nil {
		cluster.Status.Resources = &infrav1.ClusterResources{}
	}

	backendNames := make([]string, len(lb.Backends))
	for i, b := range lb.Backends {
		backendNames[i] = b.Name
	}

	cluster.Status.Resources.LoadBalancer = &infrav1.ManagedLoadBalancer{
		ID:       lb.Name,
		UID:      lb.ID,
		Address:  lb.Address,
		Backends: backendNames,
	}

	setClusterCondition(cluster, infrav1.LoadBalancerReadyCondition, corev1.ConditionTrue,
		infrav1.LoadBalancerReadyReason, "Load balancer is active")

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
			ID: sgName, Managed: true, Role: role,
		})
	}

	for _, existingID := range sgConfig.ExistingIDs {
		exists, err := cloudClient.SecurityGroups().Exists(ctx, existingID)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing security group %s: %w", existingID, err)
		}
		if !exists {
			return nil, fmt.Errorf("existing security group %s not found", existingID)
		}

		managed = append(managed, infrav1.ManagedSecurityGroup{
			ID: existingID, Managed: false, Role: role,
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
		desiredNames[sg.ID] = true
	}

	for _, old := range cluster.Status.Resources.SecurityGroups {
		if !old.Managed {
			continue // never delete externally-managed SGs
		}
		if desiredNames[old.ID] {
			continue // still desired
		}
		log.Info("Deleting stale managed security group", "name", old.ID)
		if err := cloudClient.SecurityGroups().Delete(ctx, old.ID); err != nil {
			if !helpers.IsNotFoundError(err) {
				return fmt.Errorf("failed to delete stale security group %s: %w", old.ID, err)
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
			names = append(names, sg.ID)
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

	// TODO: DO NOT rely on status to determine resources to clean up.
	if cluster.Status.Resources == nil {
		return nil
	}

	var errs []error
	stillDeleting := 0

	// Delete managed LoadBalancer (and its sub-resources: PublicIP, BackendPool, BackendService, L4Route).
	if lb := cluster.Status.Resources.LoadBalancer; lb != nil {
		log.Info("Deleting LoadBalancer", "id", lb.ID)
		if err := cloudClient.LoadBalancers().Delete(ctx, lb.ID); err != nil {
			if !cloud.IsNotFoundError(err) {
				errs = append(errs, fmt.Errorf("failed to delete managed LoadBalancer %s: %w", lb.ID, err))
			}
		}
		if exists, err := cloudClient.LoadBalancers().Exists(ctx, lb.ID); err != nil {
			errs = append(errs, fmt.Errorf("failed to check LoadBalancer %s existence: %w", lb.ID, err))
		} else if exists {
			stillDeleting++
		}
	}

	// Delete managed SecurityGroups.
	for _, sg := range cluster.Status.Resources.SecurityGroups {
		if !sg.Managed {
			continue
		}
		log.Info("Deleting managed SecurityGroup", "uid", sg.UID, "id", sg.ID)
		if err := cloudClient.SecurityGroups().Delete(ctx, sg.ID); err != nil {
			if !cloud.IsNotFoundError(err) {
				errs = append(errs, fmt.Errorf("failed to delete managed SecurityGroup %s: %w", sg.ID, err))
			}
		}
		// Check if it's gone yet.
		if exists, err := cloudClient.SecurityGroups().Exists(ctx, sg.ID); err != nil {
			errs = append(errs, fmt.Errorf("failed to check SecurityGroup %s existence: %w", sg.ID, err))
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

// resolveCloudClient returns a cloud client for the given cluster, built from
// the secret named by the cluster's mandatory CredentialsRef.
func (r *EvrocClusterReconciler) resolveCloudClient(ctx context.Context, cluster *infrav1.EvrocCluster) (cloud.ClientInterface, error) {
	secretKey := types.NamespacedName{Namespace: cluster.Namespace}
	if ref := cluster.Spec.CredentialsRef; ref != nil {
		secretKey.Name = ref.Name
	}
	clusterCtx := cloud.ClusterContext{Project: cluster.Spec.Project, Region: cluster.Spec.Region}
	factory := r.clientFactory
	if factory == nil {
		factory = cloud.ClientForCluster
	}
	cloudClient, err := factory(ctx, r.Client, secretKey.Name, secretKey.Namespace, clusterCtx, r.SDKMetrics)
	if !apierrors.IsNotFound(err) {
		return cloudClient, err
	}

	// clusterctl move carries the owned copy, while the user-managed source
	// secret may not exist in the target management cluster.
	copyKey := credentialSecretCopyKey(cluster)
	cloudClient, copyErr := factory(ctx, r.Client, copyKey.Name, copyKey.Namespace, clusterCtx, r.SDKMetrics)
	if apierrors.IsNotFound(copyErr) {
		return nil, err
	}
	return cloudClient, copyErr
}

// credentialSecretCopyKey identifies the owned credential copy in the same
// namespace as the EvrocCluster so it can be included in clusterctl move.
func credentialSecretCopyKey(cluster *infrav1.EvrocCluster) types.NamespacedName {
	return types.NamespacedName{Name: cluster.Name + "-evroc-credentials", Namespace: cluster.Namespace}
}

// ensureCredentialSecretCopy copies the secret named by credentialsRef into a
// per-cluster secret that this EvrocCluster owns. The copy, rather than the
// user-managed source, moves with and is garbage-collected with the cluster.
func (r *EvrocClusterReconciler) ensureCredentialSecretCopy(ctx context.Context, cluster *infrav1.EvrocCluster) error {
	ref := cluster.Spec.CredentialsRef
	if ref == nil {
		return nil // defensive: the webhook rejects a cluster without credentialsRef
	}

	ns := cluster.Namespace

	src := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: ns}, src); err != nil {
		if apierrors.IsNotFound(err) {
			// The source is not moved because it is user-managed. Keep using the
			// owned copy transferred with the cluster when it is available.
			if copyErr := r.Get(ctx, credentialSecretCopyKey(cluster), &corev1.Secret{}); copyErr == nil {
				return nil
			}
		}
		return fmt.Errorf("failed to get credential secret %s/%s: %w", ns, ref.Name, err)
	}

	copyKey := credentialSecretCopyKey(cluster)
	gvk := infrav1.GroupVersion.WithKind("EvrocCluster")
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      copyKey.Name,
			Namespace: copyKey.Namespace,
			Labels:    map[string]string{clusterv1.ClusterNameLabel: cluster.Name},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gvk.GroupVersion().String(),
				Kind:       gvk.Kind,
				Name:       cluster.Name,
				UID:        cluster.UID,
			}},
		},
		Type: src.Type,
		Data: src.Data,
	}

	existing := &corev1.Secret{}
	err := r.Get(ctx, copyKey, existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create credential secret copy: %w", err)
		}
		log.FromContext(ctx).Info("Copied credential secret for this cluster",
			"from", ns+"/"+ref.Name, "to", desired.Namespace+"/"+desired.Name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get credential secret copy: %w", err)
	}

	// Keep the copy in step with the source.
	if !reflect.DeepEqual(existing.Data, desired.Data) {
		existing.Data = desired.Data
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update credential secret copy: %w", err)
		}
		log.FromContext(ctx).Info("Refreshed credential secret copy", "secret", desired.Namespace+"/"+desired.Name)
	}
	return nil
}

// clusterResourcePrefix returns the stable prefix for cluster-level cloud resources.
// The annotation survives clusterctl move, where the UID changes and status is
// not transferred. Existing clusters recover the prefix from their managed LB.
func clusterResourcePrefix(cluster *infrav1.EvrocCluster) (string, error) {
	if prefix := cluster.Annotations[clusterResourcePrefixAnnotation]; prefix != "" {
		return prefix, nil
	}
	if cluster.Status.Resources != nil && cluster.Status.Resources.LoadBalancer != nil {
		if lbName := cluster.Status.Resources.LoadBalancer.ID; strings.HasSuffix(lbName, "-cp-lb") {
			prefix := strings.TrimSuffix(lbName, "-cp-lb")
			// reconcileNormal's deferred patchHelper.Patch persists this annotation.
			setClusterResourcePrefixAnnotation(cluster, prefix)
			return prefix, nil
		}
	}
	if cluster.UID == "" {
		return "", fmt.Errorf("cluster %s/%s has empty UID, cannot generate unique resource prefix", cluster.Namespace, cluster.Name)
	}
	prefix := fmt.Sprintf("%s-%s", cluster.Name, cluster.UID[:8])
	setClusterResourcePrefixAnnotation(cluster, prefix)
	return prefix, nil
}

func setClusterResourcePrefixAnnotation(cluster *infrav1.EvrocCluster, prefix string) {
	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}
	cluster.Annotations[clusterResourcePrefixAnnotation] = prefix
}

// setClusterCondition sets or updates a condition on the cluster's status.
func setClusterCondition(cluster *infrav1.EvrocCluster, condType clusterv1.ConditionType, status corev1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	newCond := clusterv1.Condition{
		Type:               condType,
		Status:             status,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	}
	for i, c := range cluster.Status.Conditions {
		if c.Type == condType {
			if c.Status == status {
				newCond.LastTransitionTime = c.LastTransitionTime
			}
			cluster.Status.Conditions[i] = newCond
			return
		}
	}
	cluster.Status.Conditions = append(cluster.Status.Conditions, newCond)
}

// SetupWithManager sets up the controller with the Manager.
func (r *EvrocClusterReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.EvrocCluster{}).
		Watches(
			&infrav1.EvrocMachine{},
			handler.EnqueueRequestsFromMapFunc(r.machineToCluster),
		).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(r.capiClusterToEvrocCluster),
		).
		Complete(r)
}

// capiClusterToEvrocCluster maps a CAPI Cluster event to the associated EvrocCluster.
// This is required so the controller is notified when the Cluster is unpaused
// after clusterctl move.
func (r *EvrocClusterReconciler) capiClusterToEvrocCluster(_ context.Context, obj client.Object) []reconcile.Request {
	cluster, ok := obj.(*clusterv1.Cluster)
	if !ok {
		return nil
	}
	ref := cluster.Spec.InfrastructureRef
	if ref.Kind != "EvrocCluster" || ref.Name == "" {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: ref.Name, Namespace: cluster.Namespace}},
	}
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
