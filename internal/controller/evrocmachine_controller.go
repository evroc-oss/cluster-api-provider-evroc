// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"regexp"
	"strings"
	"time"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/evroc-oss/evroc-go-sdk/compute"
	"github.com/evroc-oss/evroc-go-sdk/metrics"
	computetypes "github.com/evroc-oss/evroc-go-sdk/types/compute"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/controller/helpers"
)

const (
	machineFinalizer                = "infrastructure.cluster.x-k8s.io/evrocmachine"
	machineOwnershipIDAnnotation    = "evrocmachine.infrastructure.cluster.x-k8s.io/ownership-id"
	externalCloudProviderAnnotation = "infrastructure.cluster.x-k8s.io/external-cloud-provider"

	// Requeue intervals for various waiting scenarios
	requeueImmediately = 1 * time.Second
	requeueShort       = 10 * time.Second
	requeueMedium      = 15 * time.Second
	requeueLong        = 30 * time.Second
)

var (
	errMachineDeletionPending = errors.New("machine deletion pending")

	// Compiled regexes for parsing quota error messages
	requestedQuotaRegex = regexp.MustCompile(`Requested additional ([0-9]+) vCPUs? and ([0-9.]+[A-Z]*) memory`)
	availableQuotaRegex = regexp.MustCompile(`Only ([0-9]+) vCPUs? \(out of ([0-9]+) in quota\) and ([0-9.]+[A-Z]*) memory \(out of ([0-9.]+[A-Z]*) in quota\)`)
)

// machineResourceLabels returns ownership labels for cloud resources created by this machine.
// It merges cluster-level additionalLabels, machine-level additionalLabels, and ownership
// labels. Machine labels override cluster labels; ownership labels always win.
func machineResourceLabels(machine *infrav1.EvrocMachine, clusterID string, clusterLabels map[string]string) map[string]string {
	clusterName := machine.Labels[clusterv1.ClusterNameLabel]
	if clusterName == "" {
		clusterName = "unknown"
	}
	if clusterID == "" {
		clusterID = "unknown"
	}
	labels := helpers.ResourceLabels(clusterName, clusterID, clusterLabels, machine.Spec.AdditionalLabels)
	labels[cloud.LabelMachineID] = machineOwnershipID(machine)
	labels[cloud.LabelMachineName] = machine.Name
	return labels
}

// EvrocMachineReconciler reconciles a EvrocMachine object
type EvrocMachineReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Recorder   record.EventRecorder
	SDKMetrics *metrics.Manager

	// clientFactory builds the cloud client for a machine's cluster. Defaults to
	// cloud.ClientForCluster; tests override it to inject a fake.
	clientFactory clusterClientFactory
}

func (r *EvrocMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the EvrocMachine instance
	machine := &infrav1.EvrocMachine{}
	if err := r.Get(ctx, req.NamespacedName, machine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check for pause (CAPI contract): check both the annotation on this object
	// and Spec.Paused on the owning Cluster. This must happen BEFORE deletion
	// handling so that clusterctl move does not trigger infrastructure cleanup.
	if helpers.IsPaused(ctx, r.Client, machine) {
		log.Info("Reconciliation is paused for this object")
		setMachineCondition(machine, string(infrav1.PausedCondition), corev1.ConditionTrue, infrav1.PausedReason, "Reconciliation is paused")
		if err := r.Status().Update(ctx, machine); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating paused condition: %w", err)
		}
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling EvrocMachine",
		"name", machine.Name,
		"project", machine.Spec.Project,
		"region", machine.Spec.Region,
		"computeProfile", machine.Spec.ComputeProfile,
		"image", machine.Spec.Image)

	// Resolve the owning EvrocCluster via CAPI Cluster.Spec.InfrastructureRef.
	// This is fetched once and threaded through to avoid repeated lookups and
	// the assumption that CAPI Cluster name == EvrocCluster name.
	evrocCluster, err := r.resolveEvrocCluster(ctx, machine)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolving EvrocCluster: %w", err)
	}

	// Resolve cloud client — per-cluster credentials take priority over global.
	cloudClient, err := r.resolveCloudClient(ctx, machine, evrocCluster)
	if err != nil {
		// Surface the cause on the object rather than only in controller logs.
		setMachineCondition(machine, "Ready", corev1.ConditionFalse,
			infrav1.CredentialsNotFoundReason, err.Error())
		credentialErr := fmt.Errorf("resolving cloud credentials: %w", err)
		if statusErr := r.Status().Update(ctx, machine); statusErr != nil {
			return ctrl.Result{}, errors.Join(credentialErr, fmt.Errorf("updating credential failure condition: %w", statusErr))
		}
		return ctrl.Result{}, credentialErr
	}

	ownershipInitialized := false
	if machine.DeletionTimestamp.IsZero() {
		ownershipInitialized, err = ensureOwnershipID(machine, machineOwnershipIDAnnotation)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// Handle deletion using helper
	if deleting, err := helpers.HandleDeletion(ctx, r.Client, machine, machineFinalizer,
		func(ctx context.Context) error {
			return r.deleteMachineFromCloud(ctx, machine, cloudClient)
		}); deleting {
		if errors.Is(err, errMachineDeletionPending) {
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}
		return ctrl.Result{}, err
	}

	// Ensure finalizer using helper
	if requeue, err := helpers.EnsureFinalizer(ctx, r.Client, machine, machineFinalizer); err != nil {
		return ctrl.Result{}, err
	} else if requeue {
		return ctrl.Result{RequeueAfter: requeueImmediately}, nil
	}
	if ownershipInitialized {
		if err := r.Update(ctx, machine); err != nil {
			return ctrl.Result{}, fmt.Errorf("persisting machine ownership ID: %w", err)
		}
		return ctrl.Result{RequeueAfter: requeueImmediately}, nil
	}

	return r.reconcileNormal(ctx, machine, cloudClient, evrocCluster)
}

// resolveEvrocCluster fetches the EvrocCluster that owns this machine by going
// through the CAPI Cluster's InfrastructureRef, rather than assuming the CAPI
// Cluster name and EvrocCluster name are identical.
// Returns nil (not an error) when the cluster is not yet available.
func (r *EvrocMachineReconciler) resolveEvrocCluster(ctx context.Context, machine *infrav1.EvrocMachine) (*infrav1.EvrocCluster, error) {
	clusterName := machine.Labels[clusterv1.ClusterNameLabel]
	if clusterName == "" {
		return nil, nil
	}

	// Fetch the CAPI Cluster to get the canonical InfrastructureRef.
	capiCluster := &clusterv1.Cluster{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      clusterName,
		Namespace: machine.Namespace,
	}, capiCluster); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get CAPI Cluster %q: %w", clusterName, err)
	}

	infraName := capiCluster.Spec.InfrastructureRef.Name
	if infraName == "" {
		return nil, nil
	}

	evrocCluster := &infrav1.EvrocCluster{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      infraName,
		Namespace: machine.Namespace,
	}, evrocCluster); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get EvrocCluster %q: %w", infraName, err)
	}

	return evrocCluster, nil
}

// resolveCloudClient returns a cloud client for this machine's cluster, using the
// credentials named by the owning EvrocCluster's CredentialsRef. A machine without
// an owning EvrocCluster has no credential source and is an error.
func (r *EvrocMachineReconciler) resolveCloudClient(ctx context.Context, machine *infrav1.EvrocMachine, evrocCluster *infrav1.EvrocCluster) (cloud.ClientInterface, error) {
	if evrocCluster == nil {
		return nil, fmt.Errorf("no EvrocCluster found for machine %s/%s", machine.Namespace, machine.Name)
	}

	secretName := ""
	secretNamespace := evrocCluster.Namespace
	if ref := evrocCluster.Spec.CredentialsRef; ref != nil {
		secretName = ref.Name
	}
	clusterCtx := cloud.ClusterContext{
		Project:      evrocCluster.Spec.Project,
		Region:       evrocCluster.Spec.Region,
		APIBaseURL:   evrocCluster.Spec.Endpoints.GetAPIBaseURL(),
		AuthTokenURL: evrocCluster.Spec.Endpoints.GetAuthTokenURL(),
		ClientID:     evrocCluster.Spec.Endpoints.GetClientID(),
	}
	factory := r.clientFactory
	if factory == nil {
		factory = cloud.ClientForCluster
	}
	cloudClient, err := factory(ctx, r.Client, secretName, secretNamespace, clusterCtx, r.SDKMetrics)
	if !apierrors.IsNotFound(err) {
		return cloudClient, err
	}

	copyKey := credentialSecretCopyKey(evrocCluster)
	cloudClient, copyErr := factory(ctx, r.Client, copyKey.Name, copyKey.Namespace, clusterCtx, r.SDKMetrics)
	if apierrors.IsNotFound(copyErr) {
		return nil, err
	}
	return cloudClient, copyErr
}

// clusterLabelInfo holds cluster-level label and network data fetched once per reconciliation.
type clusterLabelInfo struct {
	// ID is the immutable annotation-backed cluster ownership ID.
	ID string
	// AdditionalLabels are user-specified labels from the EvrocCluster spec.
	AdditionalLabels map[string]string
	// VPCName is the custom VPC name from the cluster spec, or empty for default VPC.
	VPCName string
}

// clusterLabelInfoFrom extracts label propagation data from an already-fetched EvrocCluster.
func clusterLabelInfoFrom(evrocCluster *infrav1.EvrocCluster) clusterLabelInfo {
	if evrocCluster == nil {
		return clusterLabelInfo{}
	}
	info := clusterLabelInfo{
		ID:               clusterOwnershipID(evrocCluster),
		AdditionalLabels: evrocCluster.Spec.AdditionalLabels,
	}
	if evrocCluster.Spec.Network.VPCRef != nil {
		info.VPCName = *evrocCluster.Spec.Network.VPCRef
	}
	return info
}

func (r *EvrocMachineReconciler) reconcileNormal(ctx context.Context, machine *infrav1.EvrocMachine, cloudClient cloud.ClientInterface, evrocCluster *infrav1.EvrocCluster) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Mark the Paused condition as False now that reconciliation has resumed.
	setMachineCondition(machine, string(infrav1.PausedCondition), corev1.ConditionFalse, "Reconciling", "Reconciliation is active")

	// Extract cluster-level label info from the already-resolved EvrocCluster.
	clusterInfo := clusterLabelInfoFrom(evrocCluster)

	// Find the owner CAPI Machine by walking owner references.
	// Use unstructured to support any CAPI API version installed in the cluster.
	ownerName, ownerAPIVersion := getOwnerMachineRef(machine.OwnerReferences)
	if ownerName == "" {
		log.Info("Owner CAPI Machine not found yet, requeueing", "name", machine.Name)
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Fetch the Machine unstructured to read bootstrap data regardless of CAPI API version.
	ownerMachine := &unstructured.Unstructured{}
	ownerMachine.SetAPIVersion(ownerAPIVersion)
	ownerMachine.SetKind("Machine")
	if err := r.Get(ctx, types.NamespacedName{Name: ownerName, Namespace: machine.Namespace}, ownerMachine); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info("Owner CAPI Machine not found yet, requeueing", "machineName", ownerName)
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get owner Machine: %w", err)
	}

	// A zone is assigned once and kept: re-picking on every reconcile can land
	// on a different zone once other machines have been counted, leaving the
	// boot disk and the VM in different zones.
	if machine.Status.AvailabilityZone == "" {
		if err := r.pickAndUpdateZoneForMachine(ctx, machine, ownerMachine, evrocCluster); err != nil {
			return r.handleMachineError(ctx, machine, err)
		}
	}

	// Check the cloud first: if the VM already exists, skip all pre-creation
	// work and go straight to status reconciliation. This uses the cloud as
	// source of truth rather than relying on status fields, which matters
	// after clusterctl move (status is empty) and avoids race conditions
	// between ProviderID and MachineID being set non-atomically.
	evrocVM, err := cloudClient.VirtualMachines().Get(ctx, machine.Name)
	if err == nil {
		return r.reconcileExistingVM(ctx, machine, evrocVM, cloudClient, evrocCluster)
	}
	if !errors.Is(err, evroc.ErrNotFound) {
		return r.handleMachineError(ctx, machine, fmt.Errorf("failed to get VM from cloud: %w", err))
	}

	// Wait for bootstrap provider to write cloud-init data.
	dataSecretName, found, nestedErr := unstructured.NestedString(ownerMachine.Object, "spec", "bootstrap", "dataSecretName")
	if nestedErr != nil {
		return ctrl.Result{}, fmt.Errorf("reading spec.bootstrap.dataSecretName from owner Machine: %w", nestedErr)
	}
	if !found || dataSecretName == "" {
		log.Info("Bootstrap data not yet ready, requeueing", "machine", machine.Name)
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Read cloud-init from the bootstrap secret.
	bootstrapSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: machine.Namespace,
		Name:      dataSecretName,
	}, bootstrapSecret); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info("Bootstrap secret not ready yet, requeueing", "secretName", dataSecretName)
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get bootstrap secret %q: %w", dataSecretName, err)
	}
	bootstrapUserData := string(bootstrapSecret.Data["value"])
	userData := mergeUserData(bootstrapUserData, machine.Spec.UserData)

	// Ensure the boot disk exists and is ready before creating the VM.
	// Evroc requires a disk to be pre-created (with image) and referenced by name in VMs.
	if machine.Spec.RootDiskSize > 0 {
		diskName := fmt.Sprintf("%s-boot-disk", machine.Name)
		evrocDisk, diskErr := cloudClient.Disks().Get(ctx, diskName)
		if errors.Is(diskErr, evroc.ErrNotFound) {
			log.Info("Creating boot disk", "disk", diskName, "image", machine.Spec.Image, "size", machine.Spec.RootDiskSize)
			if _, createErr := cloudClient.Disks().Create(ctx, diskName,
				machine.Spec.RootDiskSize, machine.Spec.Image, machine.Status.AvailabilityZone, machineResourceLabels(machine, clusterInfo.ID, clusterInfo.AdditionalLabels)); createErr != nil {
				return r.handleMachineError(ctx, machine, fmt.Errorf("failed to create boot disk: %w", createErr))
			}
			// Track boot disk in status so deletion reads from status, not guessing.
			r.ensureResources(machine)
			machine.Status.Resources.BootDisk = &infrav1.ManagedDisk{
				ID:      diskName,
				SizeGB:  machine.Spec.RootDiskSize,
				Managed: true,
			}
			if err := r.persistResourcesStatus(ctx, machine); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to persist boot disk status: %w", err)
			}
			return ctrl.Result{RequeueAfter: requeueLong}, nil
		} else if diskErr != nil {
			return r.handleMachineError(ctx, machine, fmt.Errorf("failed to get boot disk: %w", diskErr))
		}
		if !compute.IsDiskReady(evrocDisk) {
			if reason, message := getDiskErrorCondition(evrocDisk); reason != "" {
				return r.handleMachineError(ctx, machine,
					fmt.Errorf("boot disk %q failed: %s: %s", diskName, reason, message))
			}
			log.Info("Boot disk not yet ready, requeueing", "disk", diskName)
			return ctrl.Result{RequeueAfter: requeueLong}, nil
		}
		// Ensure boot disk is tracked (may have been created before this tracking was added).
		r.ensureResources(machine)
		if machine.Status.Resources.BootDisk == nil {
			machine.Status.Resources.BootDisk = &infrav1.ManagedDisk{
				ID:      evrocDisk.Metadata.Id,
				UID:     evrocDisk.Metadata.Uid.String(),
				SizeGB:  machine.Spec.RootDiskSize,
				Managed: true,
			}
		}
	}

	// Ensure additional managed data disks are created before VM creation.
	additionalDiskPending, additionalDisks, err := r.reconcileAdditionalDisks(ctx, machine, cloudClient, clusterInfo)
	if err != nil {
		return r.handleMachineError(ctx, machine, fmt.Errorf("failed to reconcile additional disks: %w", err))
	}
	r.ensureResources(machine)
	machine.Status.Resources.AdditionalDisks = additionalDisks
	if additionalDiskPending {
		if err := r.persistResourcesStatus(ctx, machine); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to persist managed additional disks status: %w", err)
		}
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}

	// Ensure placement group references are valid/managed before VM creation.
	placementPending, placementGroup, err := r.reconcilePlacementGroup(ctx, machine, cloudClient, clusterInfo)
	if err != nil {
		return r.handleMachineError(ctx, machine, fmt.Errorf("failed to reconcile placement group: %w", err))
	}
	r.ensureResources(machine)
	machine.Status.Resources.PlacementGroup = placementGroup
	if placementPending {
		if err := r.persistResourcesStatus(ctx, machine); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to persist managed placement group status: %w", err)
		}
		return ctrl.Result{RequeueAfter: requeueMedium}, nil
	}

	// Ensure machine-level PublicIP intent is reconciled before VM creation.
	publicIPPending, publicIP, err := r.reconcilePublicIPCreation(ctx, machine, cloudClient, clusterInfo)
	if err != nil {
		return r.handleMachineError(ctx, machine, fmt.Errorf("failed to reconcile machine public IP: %w", err))
	}
	r.ensureResources(machine)
	machine.Status.Resources.PublicIP = publicIP
	if publicIPPending {
		if err := r.persistResourcesStatus(ctx, machine); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to persist managed public IP status: %w", err)
		}
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Ensure machine-level inline security groups exist before creating the VM.
	if err := r.reconcileMachineSecurityGroups(ctx, machine, cloudClient, clusterInfo); err != nil {
		return r.handleMachineError(ctx, machine, fmt.Errorf("failed to reconcile machine security groups: %w", err))
	}

	// VM does not exist — create it.
	log.Info("Creating machine in evroc", "name", machine.Name)
	vmRequest, buildErr := r.buildVMRequest(machine, userData, evrocCluster, cloudClient)
	if buildErr != nil {
		return r.handleMachineError(ctx, machine, fmt.Errorf("failed to build VM request: %w", buildErr))
	}

	if _, err := cloudClient.VirtualMachines().Create(ctx, vmRequest); err != nil {
		return r.handleMachineError(ctx, machine, err)
	}

	log.Info("Machine creation submitted, requeueing to check readiness", "name", machine.Name)
	return ctrl.Result{RequeueAfter: requeueLong}, nil
}

// reconcileExistingVM handles an EvrocMachine whose VM already exists in the
// cloud. It updates status, sets providerID, reconciles SG/IP drift, and
// patches the workload-cluster node.
func (r *EvrocMachineReconciler) reconcileExistingVM(ctx context.Context, machine *infrav1.EvrocMachine, evrocVM *computetypes.VirtualMachine, cloudClient cloud.ClientInterface, evrocCluster *infrav1.EvrocCluster) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// If machine is not yet ready, requeue to poll
	if !compute.IsVMReady(evrocVM) {
		log.Info("Machine not yet ready, requeueing", "name", machine.Name)
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}

	// Reconcile security groups on existing VM if configuration changed
	// This allows users to update security groups without destroying the cluster
	if err := r.reconcileVMSecurityGroups(ctx, machine, cloudClient, evrocCluster); err != nil {
		log.Error(err, "failed to reconcile VM security groups (will retry)")
		return ctrl.Result{}, err
	}

	// Reconcile public IP on existing VM if configuration changed
	// This allows users to add/remove/change public IPs without destroying the cluster
	if err := r.reconcilePublicIPAttachment(ctx, machine, cloudClient, evrocCluster); err != nil {
		log.Error(err, "failed to reconcile VM public IP (will retry)")
		return ctrl.Result{}, err
	}

	// Reconcile disks on existing VM if configuration changed.
	// This allows users to add/remove additional disks without destroying the VM.
	if err := r.reconcileVMDisks(ctx, machine, cloudClient); err != nil {
		log.Error(err, "failed to reconcile VM disks (will retry)")
		return ctrl.Result{}, err
	}

	// Reconcile placement on existing VM if the placement group changed.
	if err := r.reconcileVMPlacement(ctx, machine, evrocVM, cloudClient); err != nil {
		log.Error(err, "failed to reconcile VM placement (will retry)")
		return ctrl.Result{}, err
	}

	// Register this CP machine as a LB backend as soon as the VM exists.
	// Health checks on the backend service protect against routing to unready
	// backends — the LB only forwards once the apiserver passes the TCP probe.
	if err := r.reconcileLBBackend(ctx, machine, cloudClient, evrocCluster); err != nil {
		log.Error(err, "failed to register LB backend (will retry)")
		return ctrl.Result{}, err
	}

	// Set provider ID in spec if not already set (must be done before status update to avoid race)
	needsSpecUpdate := false
	if machine.Spec.ProviderID == nil {
		providerID := fmt.Sprintf("evroc://%s", evrocVM.Metadata.Uid.String())
		machine.Spec.ProviderID = &providerID
		needsSpecUpdate = true
	}

	// Update spec first if needed, then re-fetch to get latest resource version
	if needsSpecUpdate {
		if err := r.Update(ctx, machine); err != nil {
			log.Error(err, "failed to update spec")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(machine), machine); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	// Update status using SDK helper
	machine.Status.Ready = compute.IsVMReady(evrocVM)
	machine.Status.MachineID = evrocVM.Metadata.Uid.String()
	machine.Status.Addresses = extractMachineAddresses(evrocVM)

	// Set Ready condition.
	if machine.Status.Ready {
		setMachineCondition(machine, "Ready", corev1.ConditionTrue, "MachineReady", "")
	} else {
		setMachineCondition(machine, "Ready", corev1.ConditionFalse, "MachineNotReady", "VM is not yet ready")
	}

	// Set initialization.provisioned as soon as the VM exists.
	// CAPI v1beta2 contract: infrastructure is "provisioned" when the cloud
	// resource exists — this must NOT be gated on ready state, node joining,
	// or providerID patching, otherwise CAPI never transitions the Machine
	// past Provisioning and the node can never join (deadlock).
	provisioned := true
	machine.Status.Initialization.Provisioned = &provisioned

	// Update status (includes ready, addresses, phase, and initialization)
	if err := r.Status().Update(ctx, machine); err != nil {
		log.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	log.Info("Machine status updated",
		"ready", machine.Status.Ready,
		"machineID", machine.Status.MachineID,
		"addressCount", len(machine.Status.Addresses))

	// Patch the workload-cluster node's spec.providerID so CAPI can link the node
	// to this Machine and transition it to Running. Without this, an external CCM
	// would be required to set the providerID, which we don't have.
	if machine.Status.Ready {
		wc, err := r.getWorkloadClusterClient(ctx, machine)
		if err != nil {
			log.Error(err, "failed to get workload cluster client (will retry)")
			return ctrl.Result{RequeueAfter: requeueMedium}, nil
		}
		if wc == nil {
			// Kubeconfig not yet available; retry shortly.
			return ctrl.Result{RequeueAfter: requeueMedium}, nil
		}

		if patched, err := r.patchNodeProviderID(ctx, machine, wc); err != nil {
			log.Error(err, "failed to patch node providerID (will retry)")
			return ctrl.Result{RequeueAfter: requeueMedium}, nil
		} else if !patched {
			// Node not yet available; retry shortly.
			return ctrl.Result{RequeueAfter: requeueMedium}, nil
		}

		if err := r.reconcileNodeInitialization(ctx, machine, evrocCluster, wc); err != nil {
			log.Error(err, "failed to reconcile node labels and taints (will retry)")
			// Don't fail reconciliation, just log and continue
		}

	}

	return ctrl.Result{}, nil
}

func (r *EvrocMachineReconciler) handleMachineError(ctx context.Context, machine *infrav1.EvrocMachine, err error) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	machine.Status.Ready = false

	errMsg := err.Error()
	failureReason := "MachineOperationFailed"
	failureMessage := fmt.Sprintf("Failed to get/create machine: %v", err)
	eventReason := "MachineOperationFailed"
	eventMessage := "Failed to get/create machine: %v"
	eventArgs := []any{err}

	// Determine if this is a terminal (non-retryable) error.
	// Terminal errors are returned to controller-runtime which will use its
	// default backoff. Transient errors get an explicit requeue to avoid
	// rapid retry storms against the cloud API.
	terminalError := false

	// Check for quota errors specifically
	if errors.Is(err, evroc.ErrForbidden) && strings.Contains(strings.ToLower(errMsg), "quota") {
		quotaDetails := parseQuotaError(errMsg)
		failureReason = "QuotaExceeded"
		failureMessage = quotaDetails
		eventReason = "QuotaExceeded"
		eventMessage = quotaDetails
		eventArgs = nil
		terminalError = true
		log.Error(err, "Quota exceeded", "details", quotaDetails)
	} else if errors.Is(err, evroc.ErrForbidden) {
		failureReason = "InsufficientPermissions"
		failureMessage = fmt.Sprintf("Access denied creating machine: %v", err)
		eventReason = "InsufficientPermissions"
		eventMessage = "Access denied creating machine: %v"
		eventArgs = []any{err}
		terminalError = true
	} else if errors.Is(err, evroc.ErrBadRequest) {
		failureReason = "InvalidConfiguration"
		failureMessage = fmt.Sprintf("Invalid machine configuration: %v", err)
		eventReason = "InvalidConfiguration"
		eventMessage = "Invalid machine configuration: %v"
		eventArgs = []any{err}
		terminalError = true
	}

	setMachineCondition(machine, "Ready", corev1.ConditionFalse, failureReason, failureMessage)
	r.emitMachineWarningEvent(machine, eventReason, eventMessage, eventArgs...)

	if statusErr := r.Status().Update(ctx, machine); statusErr != nil {
		log.Error(statusErr, "failed to update status")
	}

	// Terminal errors (quota, permissions, bad request) won't resolve on retry.
	// Transient errors (network, server errors) get a 30s backoff before retry.
	if terminalError {
		return ctrl.Result{}, err
	}
	log.Info("Transient cloud API error, will retry", "error", err, "retryAfter", "30s")
	return ctrl.Result{RequeueAfter: requeueLong}, nil
}

func (r *EvrocMachineReconciler) emitMachineWarningEvent(machine *infrav1.EvrocMachine, reason, messageFmt string, args ...any) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(machine, corev1.EventTypeWarning, reason, messageFmt, args...)
}

// parseQuotaError extracts quota details from the error message to provide actionable feedback
func parseQuotaError(errMsg string) string {
	// Error format: "not enough quota to perform request. Requested additional X vCPUs and Y memory. Only Z vCPUs (out of W in quota) and A memory (out of B in quota) available"

	requestedMatches := requestedQuotaRegex.FindStringSubmatch(errMsg)
	availableMatches := availableQuotaRegex.FindStringSubmatch(errMsg)

	if len(requestedMatches) == 3 && len(availableMatches) == 5 {
		return fmt.Sprintf("Quota exceeded: Requested %s vCPUs and %s memory, but only %s/%s vCPUs and %s/%s memory available. "+
			"Reduce machine size or increase quota limits to proceed.",
			requestedMatches[1], requestedMatches[2],
			availableMatches[1], availableMatches[2],
			availableMatches[3], availableMatches[4])
	}

	// Fallback if regex doesn't match
	return fmt.Sprintf("Quota exceeded: %s. Reduce machine size or increase quota limits to proceed.", errMsg)
}

func (r *EvrocMachineReconciler) deleteMachineFromCloud(ctx context.Context, machine *infrav1.EvrocMachine, cloudClient cloud.ClientInterface) error {
	log := log.FromContext(ctx)
	log.Info("Deleting machine from evroc", "name", machine.Name)

	// Collect errors from all deletion steps so we attempt every resource
	// even if an earlier one fails. This prevents orphaned cloud resources.
	var errs []error
	ownerID := machineOwnershipID(machine)
	if ownerID == "" {
		return fmt.Errorf("machine %s/%s has no ownership ID", machine.Namespace, machine.Name)
	}

	// Deregister from LB before deleting the VM.
	if _, isCP := machine.Labels[clusterv1.MachineControlPlaneLabel]; isCP {
		evrocCluster, resolveErr := r.resolveEvrocCluster(ctx, machine)
		if resolveErr != nil {
			return fmt.Errorf("failed to resolve EvrocCluster for LB deregistration: %w", resolveErr)
		}
		// During deletion the EvrocCluster may already be gone — if so,
		// the LB is gone too, so deregistration is unnecessary.
		if evrocCluster != nil {
			lbName, resolveErr := resolveLoadBalancerName(evrocCluster)
			if resolveErr == nil && lbName != "" {
				log.Info("Deregistering CP machine from LB", "machine", machine.Name, "loadBalancer", lbName)
				if err := cloudClient.LoadBalancers().RemoveBackend(ctx, lbName, machine.Name); err != nil {
					if !helpers.IsNotFoundError(err) {
						errs = append(errs, fmt.Errorf("failed to deregister machine %s from LB %s: %w", machine.Name, lbName, err))
					}
				}
			}
		}
	}

	// VM deletion is asynchronous. Never block a reconcile worker in the SDK
	// waiter: submit deletion and poll on subsequent reconciles before cleaning
	// up dependent resources (disks, SGs, IPs).
	vmExists, err := cloudClient.VirtualMachines().Exists(ctx, machine.Name)
	if err != nil {
		return fmt.Errorf("failed to check whether machine exists: %w", err)
	}
	if vmExists {
		if err := cloudClient.VirtualMachines().Delete(ctx, machine.Name); err != nil && !helpers.IsNotFoundError(err) {
			return fmt.Errorf("failed to delete machine: %w", err)
		}
		log.Info("Machine deletion in progress, requeueing", "name", machine.Name)
		return errMachineDeletionPending
	}
	log.Info("Machine deleted", "name", machine.Name)

	// Delete managed sub-resources by the immutable capi_machine-id label, NOT
	// from status. clusterctl move recreates the EvrocMachine with empty status,
	// and an orphaned machine has none; a status-based teardown would skip every
	// managed disk, IP, and SG and leak them (recurring cloud charges). Only
	// provider-created resources carry both the owner ID and managed-by marker,
	// so externally-referenced resources are never selected.

	// Managed security groups.
	if sgNames, listErr := cloudClient.SecurityGroups().ListByMachineOwner(ctx, ownerID); listErr != nil {
		errs = append(errs, fmt.Errorf("failed to list machine security groups: %w", listErr))
	} else {
		for _, name := range sgNames {
			log.Info("Deleting managed security group", "name", name)
			if err := cloudClient.SecurityGroups().Delete(ctx, name); err != nil {
				if !helpers.IsNotFoundError(err) {
					errs = append(errs, fmt.Errorf("failed to delete managed security group %s: %w", name, err))
				}
			}
		}
	}

	// Placement groups are not managed by CAPI (only referenced via existingGroupID),
	// so no PG cleanup is needed here.

	// Managed public IPs.
	if ipNames, listErr := cloudClient.PublicIPs().ListByOwner(ctx, ownerID); listErr != nil {
		errs = append(errs, fmt.Errorf("failed to list machine public IPs: %w", listErr))
	} else {
		for _, name := range ipNames {
			log.Info("Deleting managed public IP", "name", name)
			if err := cloudClient.PublicIPs().Delete(ctx, name); err != nil {
				if !helpers.IsNotFoundError(err) {
					errs = append(errs, fmt.Errorf("failed to delete managed public IP %s: %w", name, err))
				}
			}
		}
	}

	// Managed disks (boot + additional).
	if diskNames, listErr := cloudClient.Disks().ListByOwner(ctx, ownerID); listErr != nil {
		errs = append(errs, fmt.Errorf("failed to list machine disks: %w", listErr))
	} else {
		for _, name := range diskNames {
			log.Info("Deleting managed disk", "name", name)
			if err := cloudClient.Disks().Delete(ctx, name); err != nil {
				if !helpers.IsNotFoundError(err) {
					errs = append(errs, fmt.Errorf("failed to delete disk %s: %w", name, err))
				}
			}
		}
	}

	return errors.Join(errs...)
}

func machineOwnershipID(machine *infrav1.EvrocMachine) string {
	if machine == nil {
		return ""
	}
	return machine.Annotations[machineOwnershipIDAnnotation]
}

// getWorkloadClusterClient builds a controller-runtime client for the workload
// cluster identified by the machine's cluster label. Returns (nil, nil) when the
// kubeconfig secret does not yet exist (caller should requeue).
func (r *EvrocMachineReconciler) getWorkloadClusterClient(ctx context.Context, machine *infrav1.EvrocMachine) (client.Client, error) {
	clusterName := machine.Labels["cluster.x-k8s.io/cluster-name"]
	if clusterName == "" {
		return nil, nil
	}

	kubeconfigSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: machine.Namespace,
		Name:      clusterName + "-kubeconfig",
	}, kubeconfigSecret)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil // kubeconfig not ready yet
		}
		return nil, fmt.Errorf("getting kubeconfig secret: %w", err)
	}

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigSecret.Data["value"])
	if err != nil {
		return nil, fmt.Errorf("parsing workload kubeconfig: %w", err)
	}
	if restCfg.Insecure {
		return nil, fmt.Errorf("workload kubeconfig must not use insecure-skip-tls-verify")
	}

	wc, err := client.New(restCfg, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("creating workload cluster client: %w", err)
	}
	return wc, nil
}

// patchNodeProviderID sets spec.providerID on the workload-cluster node whose
// name matches this EvrocMachine, so that CAPI can link the node to the Machine
// and transition it to Running phase. Returns (true, nil) when done (or already
// set), (false, nil) when the node is not yet available.
func (r *EvrocMachineReconciler) patchNodeProviderID(ctx context.Context, machine *infrav1.EvrocMachine, wc client.Client) (bool, error) {
	if machine.Spec.ProviderID == nil {
		return false, nil
	}

	node := &corev1.Node{}
	if err := wc.Get(ctx, types.NamespacedName{Name: machine.Name}, node); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return false, nil // node not found yet
		}
		return false, fmt.Errorf("getting workload cluster node: %w", err)
	}

	if node.Spec.ProviderID == *machine.Spec.ProviderID {
		return true, nil // already set
	}

	patch := client.MergeFrom(node.DeepCopy())
	node.Spec.ProviderID = *machine.Spec.ProviderID
	if err := wc.Patch(ctx, node, patch); err != nil {
		return false, fmt.Errorf("patching node providerID: %w", err)
	}
	return true, nil
}

// reconcileNodeInitialization keeps node initialization self-contained in CAPE
// unless the cluster delegates it to an external CCM. Delegating clusters leave
// the uninitialized taint in place: upstream's
// cloud-node-controller uses that taint to select the path which populates
// topology, addresses, and instance metadata before removing it.
func (r *EvrocMachineReconciler) reconcileNodeInitialization(ctx context.Context, machine *infrav1.EvrocMachine, evrocCluster *infrav1.EvrocCluster, wc client.Client) error {
	if evrocCluster != nil && strings.EqualFold(strings.TrimSpace(evrocCluster.Annotations[externalCloudProviderAnnotation]), "true") {
		log.FromContext(ctx).V(2).Info("Leaving node initialization to the external cloud provider", "machine", machine.Name)
		return nil
	}

	return r.reconcileNodeLabelsAndTaints(ctx, machine, wc)
}

// reconcileNodeLabelsAndTaints provides CAPE-managed initialization for clusters
// that do not delegate node initialization to an external CCM. It sets the
// instance-type label and removes the cloud-provider initialization taint.
//
// Topology labels (zone and region) must be configured via the bootstrap provider
// in this mode:
// - RKE2: Use agentConfig.nodeLabels in RKE2ConfigTemplate/RKE2ControlPlane
// - Kubeadm: Use kubeletExtraArgs.node-labels in KubeadmConfigTemplate/KubeadmControlPlane
func (r *EvrocMachineReconciler) reconcileNodeLabelsAndTaints(ctx context.Context, machine *infrav1.EvrocMachine, wc client.Client) error {
	targetNode := &corev1.Node{}
	if err := wc.Get(ctx, types.NamespacedName{Name: machine.Name}, targetNode); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil // node not found yet, will retry
		}
		return fmt.Errorf("getting workload cluster node: %w", err)
	}

	needsUpdate := false
	patch := client.MergeFrom(targetNode.DeepCopy())

	// Add instance-type label (infrastructure-specific information)
	if targetNode.Labels == nil {
		targetNode.Labels = make(map[string]string)
	}

	if targetNode.Labels["node.kubernetes.io/instance-type"] != machine.Spec.ComputeProfile {
		targetNode.Labels["node.kubernetes.io/instance-type"] = machine.Spec.ComputeProfile
		needsUpdate = true
	}

	// Remove cloud provider initialization taint if present
	var newTaints []corev1.Taint
	for _, taint := range targetNode.Spec.Taints {
		if taint.Key == "node.cloudprovider.kubernetes.io/uninitialized" {
			needsUpdate = true
			continue // skip this taint
		}
		newTaints = append(newTaints, taint)
	}

	if needsUpdate {
		targetNode.Spec.Taints = newTaints
		if err := wc.Patch(ctx, targetNode, patch); err != nil {
			return fmt.Errorf("patching node labels and taints: %w", err)
		}
	}

	return nil
}

// pickAndUpdateZoneForMachine resolves the availability zone for a machine
// that does not have one yet and persists it to status before any cloud
// resource is created. The owner CAPI Machine's failureDomain (assigned by KCP
// for control plane nodes) takes precedence; otherwise the least-used of the
// EvrocCluster's failureDomains is picked (workers).
func (r *EvrocMachineReconciler) pickAndUpdateZoneForMachine(
	ctx context.Context,
	machine *infrav1.EvrocMachine,
	ownerMachine *unstructured.Unstructured,
	evrocCluster *infrav1.EvrocCluster,
) error {
	zone, _, err := unstructured.NestedString(ownerMachine.Object, "spec", "failureDomain")
	if err != nil {
		return fmt.Errorf("reading spec.failureDomain from owner Machine: %w", err)
	}
	if zone == "" {
		if zone, err = r.pickZoneForMachine(ctx, machine, evrocCluster); err != nil {
			return err
		}
	}
	// Never proceed to cloud API calls without a zone; evroc's errors for an
	// empty zone are cryptic.
	if zone == "" {
		return errors.New("availabilityZone is empty: configure failureDomains on the EvrocCluster so the controller can assign one automatically")
	}

	log.FromContext(ctx).Info("Assigned availabilityZone", "zone", zone)
	machine.Status.AvailabilityZone = zone
	if err := r.Status().Update(ctx, machine); err != nil {
		return fmt.Errorf("persisting availability zone: %w", err)
	}
	return nil
}

// pickZoneForMachine selects a zone by finding the least-used failure domain
// among the EvrocCluster's failureDomains. This ensures even distribution
// regardless of cluster size (e.g. 3 workers across 3 zones → a, b, c).
func (r *EvrocMachineReconciler) pickZoneForMachine(
	ctx context.Context,
	machine *infrav1.EvrocMachine,
	evrocCluster *infrav1.EvrocCluster,
) (string, error) {
	if evrocCluster == nil || len(evrocCluster.Spec.FailureDomains) == 0 {
		return "", nil
	}

	clusterName := machine.Labels[clusterv1.ClusterNameLabel]
	if clusterName == "" {
		return "", nil
	}

	machineList := &infrav1.EvrocMachineList{}
	if err := r.List(ctx, machineList,
		client.InNamespace(machine.Namespace),
		client.MatchingLabels{clusterv1.ClusterNameLabel: clusterName},
	); err != nil {
		return "", fmt.Errorf("listing cluster machines for zone distribution: %w", err)
	}

	zoneCounts := make(map[string]int)
	for i := range machineList.Items {
		m := &machineList.Items[i]
		if m.Name == machine.Name {
			continue // skip self
		}
		if m.Status.AvailabilityZone != "" {
			zoneCounts[m.Status.AvailabilityZone]++
		}
	}

	return leastUsedZone(evrocCluster.Spec.FailureDomains, zoneCounts), nil
}

// leastUsedZone returns the failure domain with the fewest assigned machines.
// Ties are broken by choosing the zone that comes first in the failureDomains
// slice, giving deterministic behavior (typically alphabetical: a, b, c).
func leastUsedZone(failureDomains []string, zoneCounts map[string]int) string {
	if len(failureDomains) == 0 {
		return ""
	}

	best := failureDomains[0]
	bestCount := zoneCounts[best]
	for _, fd := range failureDomains[1:] {
		if zoneCounts[fd] < bestCount {
			best = fd
			bestCount = zoneCounts[fd]
		}
	}
	return best
}

// getOwnerMachineRef finds the CAPI Machine owner reference name and API version.
// It matches on Group=cluster.x-k8s.io and Kind=Machine, supporting any version.
func getOwnerMachineRef(refs []metav1.OwnerReference) (name, apiVersion string) {
	for _, ref := range refs {
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			continue
		}
		if ref.Kind == "Machine" && gv.Group == "cluster.x-k8s.io" {
			return ref.Name, ref.APIVersion
		}
	}
	return "", ""
}

// SetupWithManager sets up the controller with the Manager
func (r *EvrocMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.EvrocMachine{}).
		Watches(
			&clusterv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(r.capiMachineToEvrocMachine),
		).
		Complete(r)
}

// capiMachineToEvrocMachine maps a CAPI Machine event to the EvrocMachine it
// owns. This ensures the controller is notified when the Machine is
// paused/unpaused — whether from Cluster-level pause (clusterctl move) or
// MachineDeployment-level pause.
func (r *EvrocMachineReconciler) capiMachineToEvrocMachine(_ context.Context, obj client.Object) []reconcile.Request {
	machine, ok := obj.(*clusterv1.Machine)
	if !ok {
		return nil
	}
	ref := machine.Spec.InfrastructureRef
	if ref.Kind != "EvrocMachine" || ref.Name == "" {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: ref.Name, Namespace: machine.Namespace}},
	}
}

// buildVMRequest builds a VirtualMachineRequest from EvrocMachine spec
func (r *EvrocMachineReconciler) buildVMRequest(machine *infrav1.EvrocMachine, userData string, evrocCluster *infrav1.EvrocCluster, cloudClient cloud.ClientInterface) (*computetypes.VirtualMachineRequest, error) {
	// Merge and apply labels from cluster and machine
	labels := buildLabels(machine, evrocCluster)

	// Use SDK builder to create request with correct API version
	builder := compute.NewVirtualMachineBuilder(machine.Name).
		WithVMInstanceType(machine.Spec.ComputeProfile).
		WithRunning(true)

	// Add labels if present
	if len(labels) > 0 {
		builder = builder.WithLabels(labels)
	}

	// Build the request - this sets ApiVersion and Kind correctly
	request := builder.Build()

	// Set remaining fields using our helpers
	request.Spec.Disks = r.buildDisks(machine)
	request.Spec.Placement = r.buildPlacement(machine)
	osSettings, err := r.buildOSSettings(machine, userData)
	if err != nil {
		return nil, err
	}
	request.Spec.OsSettings = osSettings
	request.Spec.Networking = *buildNetworking(machine, evrocCluster)

	if evrocCluster != nil {
		subnetName := resolveSubnetName(evrocCluster, machine.Status.AvailabilityZone)
		request.Spec.Networking.SubnetRef = cloud.SubnetRef(cloudClient.SDKClient(), subnetName)
	}

	return request, nil
}

// buildLabels merges cluster-level and machine-level labels using the
// already-resolved EvrocCluster (no API call).
func buildLabels(machine *infrav1.EvrocMachine, evrocCluster *infrav1.EvrocCluster) map[string]string {
	if evrocCluster == nil {
		return helpers.MergeLabels(nil, machine, "")
	}

	// Determine role from machine labels.
	// CAPI sets the control-plane label key on CP machines (value may be empty
	// or "true" depending on the CAPI version), so check for key existence.
	var role string
	if _, isCP := machine.Labels[clusterv1.MachineControlPlaneLabel]; isCP {
		role = "control-plane"
	} else {
		role = machine.Labels["cluster.x-k8s.io/deployment-name"]
		if role == "" {
			role = "worker"
		}
	}

	return helpers.MergeLabels(evrocCluster, machine, role)
}

// buildDisks builds disk references for the VM
func (r *EvrocMachineReconciler) buildDisks(machine *infrav1.EvrocMachine) *[]computetypes.VirtualMachineSpecDisksItem {
	disks := &[]computetypes.VirtualMachineSpecDisksItem{}

	if machine.Spec.RootDiskSize > 0 {
		bootDiskName := fmt.Sprintf("%s-boot-disk", machine.Name)
		bootFrom := true
		*disks = append(*disks, computetypes.VirtualMachineSpecDisksItem{
			DiskRef:  bootDiskName,
			BootFrom: &bootFrom,
		})
	}

	for _, disk := range machine.Spec.AdditionalDisks {
		diskName := additionalDiskName(machine.Name, disk.Name)
		*disks = append(*disks, computetypes.VirtualMachineSpecDisksItem{
			DiskRef: diskName,
		})
	}

	return disks
}

// buildPlacement builds placement configuration (zone and placement group)
func (r *EvrocMachineReconciler) buildPlacement(machine *infrav1.EvrocMachine) computetypes.VirtualMachineSpecPlacement {
	placement := computetypes.VirtualMachineSpecPlacement{}

	if machine.Status.AvailabilityZone != "" {
		zone := machine.Status.AvailabilityZone
		placement.Zone = &zone
	}

	// Reference an existing placement group if configured
	if machine.Spec.PlacementConfig != nil && machine.Spec.PlacementConfig.ExistingGroupID != nil {
		placement.PlacementGroupRef = machine.Spec.PlacementConfig.ExistingGroupID
	}

	return placement
}

// buildOSSettings builds OS settings (user data and SSH keys).
// userData is the cloud-init bootstrap data read from the CAPI bootstrap secret.
//
// SSH keys are always injected via cloud-init (even when userData is empty)
// because the Evroc API ssh.authorizedKeys field is overridden when cloud-init
// runs, making it unreliable. Using cloud-init exclusively avoids confusion.
func (r *EvrocMachineReconciler) buildOSSettings(machine *infrav1.EvrocMachine, userData string) (*computetypes.VirtualMachineSpecOsSettings, error) {
	if userData == "" && machine.Spec.SSHKey == "" {
		return nil, nil
	}

	osSettings := &computetypes.VirtualMachineSpecOsSettings{}

	// Inject SSH key into userData (generates cloud-config when userData is empty)
	var err error
	userData, err = injectSSHKeyIntoCloudInit(userData, machine.Spec.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("failed to inject SSH key into cloud-init: %w", err)
	}
	osSettings.CloudInitUserData = &userData

	return osSettings, nil
}

// injectSSHKeyIntoCloudInit appends an ssh_authorized_keys entry and creates an evroc-user
// account to cloud-init user-data. This is necessary because the Evroc API's ssh.authorizedKeys
// field is overridden when cloud-init runs.
//
// If the userData already contains a "users:" section (e.g. from CAPI bootstrap), the users
// block is not injected to avoid duplicate YAML keys (last key wins in YAML, which would
// override the bootstrap-provided users). Similarly, ssh_authorized_keys is skipped if present.
func injectSSHKeyIntoCloudInit(userData, sshKey string) (string, error) {
	// Skip injection if SSH key is empty
	if sshKey == "" {
		return userData, nil
	}

	// Build the SSH config, skipping sections that already exist in userData
	var sshConfig string
	if !strings.Contains(userData, "ssh_authorized_keys:") {
		sshConfig += fmt.Sprintf("ssh_authorized_keys:\n- %q\n", sshKey)
	}
	if !strings.Contains(userData, "users:") {
		sshConfig += fmt.Sprintf(`users:
  - name: evroc-user
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_authorized_keys:
      - %q
`, sshKey)
	}

	if userData == "" {
		return "#cloud-config\n" + sshConfig, nil
	}
	// Handle cloud-config (with or without jinja template directive)
	// RKE2 bootstrap uses "## template: jinja\n#cloud-config" so we use Contains instead of HasPrefix
	if strings.Contains(userData, "#cloud-config") {
		if sshConfig == "" {
			return userData, nil
		}
		return userData + "\n" + sshConfig, nil
	}
	// Shell scripts and other formats don't support ssh_authorized_keys
	return "", fmt.Errorf("unsupported cloud-init format: expected #cloud-config, cannot inject SSH key")
}

// buildNetworking builds networking configuration (public IP and security groups).
// When inheritFromCluster is true, the machine inherits common + role-specific SGs
// from the EvrocCluster, and (for CP machines) the managed PublicIP.
func buildNetworking(machine *infrav1.EvrocMachine, evrocCluster *infrav1.EvrocCluster) *computetypes.VirtualMachineSpecNetworking {
	networking := &computetypes.VirtualMachineSpecNetworking{}

	if evrocCluster != nil && evrocCluster.Spec.Network.StackType != nil {
		st := computetypes.VirtualMachineSpecNetworkingStackType(*evrocCluster.Spec.Network.StackType)
		networking.StackType = &st
	}

	// Resolve PublicIP: explicit config takes priority, then cluster inheritance.
	// Cluster public IP inheritance is independent of security group inheritance —
	// any CP machine gets the cluster's managed public IP if one exists.
	publicIPRef := resolvePublicIPRef(machine, evrocCluster)
	if publicIPRef != "" {
		networking.PublicIPv4Address = &struct {
			Static *computetypes.VirtualMachineSpecNetworkingStatic `json:"static,omitempty"`
		}{
			Static: &computetypes.VirtualMachineSpecNetworkingStatic{
				PublicIPRef: &publicIPRef,
			},
		}
	}

	// Handle SecurityGroups
	if machine.Spec.NetworkingConfig != nil && machine.Spec.NetworkingConfig.SecurityGroups != nil {
		sgConfig := machine.Spec.NetworkingConfig.SecurityGroups
		var sgRefs []string

		if sgConfig.InheritFromCluster {
			_, isCP := machine.Labels[clusterv1.MachineControlPlaneLabel]
			sgRefs = append(sgRefs, clusterSecurityGroupNamesForRole(evrocCluster, isCP)...)
		}

		for _, inlineSG := range sgConfig.InlineSecurityGroups {
			sgRefs = append(sgRefs, fmt.Sprintf("%s-%s", machine.Name, inlineSG.Name))
		}

		sgRefs = append(sgRefs, sgConfig.ExistingIDs...)

		if len(sgRefs) > 0 {
			networking.SecurityGroupSettings = &struct {
				SecurityGroupMemberRefs *[]string `json:"securityGroupMemberRefs,omitempty"`
			}{
				SecurityGroupMemberRefs: &sgRefs,
			}
		}
	}

	return networking
}

// resolvePublicIPRef determines the PublicIP reference for this VM using the
// already-resolved EvrocCluster (no API call).
// Priority: explicit machine config > cluster managed PublicIP (for CP machines).
// The evroc API prevents double-attach of static IPs, so no client-side
// deduplication is needed.
func resolvePublicIPRef(machine *infrav1.EvrocMachine, evrocCluster *infrav1.EvrocCluster) string {
	// Explicit machine-level public IP config takes priority
	if machine.Spec.NetworkingConfig != nil && machine.Spec.NetworkingConfig.PublicIP != nil {
		if machine.Spec.NetworkingConfig.PublicIP.ExistingID != nil {
			return *machine.Spec.NetworkingConfig.PublicIP.ExistingID
		}
		if machine.Spec.NetworkingConfig.PublicIP.Enabled {
			return managedPublicIPName(machine.Name)
		}
	}

	return ""
}

// reconcileMachineSecurityGroups creates machine-level inline security groups
// in the evroc cloud. This must run before VM creation so the groups can be
// referenced in the VM's networking spec.
func (r *EvrocMachineReconciler) reconcileMachineSecurityGroups(ctx context.Context, machine *infrav1.EvrocMachine, cloudClient cloud.ClientInterface, clusterInfo clusterLabelInfo) error {
	if machine.Spec.NetworkingConfig == nil || machine.Spec.NetworkingConfig.SecurityGroups == nil {
		return nil
	}

	sgConfig := machine.Spec.NetworkingConfig.SecurityGroups
	lg := log.FromContext(ctx)

	for _, inlineSG := range sgConfig.InlineSecurityGroups {
		sgName := fmt.Sprintf("%s-%s", machine.Name, inlineSG.Name)

		// Convert API rules to SDK rules (needed for both create and update)
		sdkRules := helpers.ConvertSecurityGroupRulesToSDK(inlineSG.Rules)

		exists, err := cloudClient.SecurityGroups().Exists(ctx, sgName)
		if err != nil {
			return fmt.Errorf("failed to check security group %s: %w", sgName, err)
		}

		if !exists {
			lg.Info("Creating inline security group for machine", "name", sgName, "rules", len(inlineSG.Rules))
			if _, err := cloudClient.SecurityGroups().Create(ctx, sgName, sdkRules, machineResourceLabels(machine, clusterInfo.ID, clusterInfo.AdditionalLabels), clusterInfo.VPCName); err != nil {
				return fmt.Errorf("failed to create security group %s: %w", sgName, err)
			}
			lg.Info("Created inline security group for machine", "name", sgName)
		} else {
			// Update existing security group with new rules
			lg.V(1).Info("Updating inline security group for machine", "name", sgName, "rules", len(inlineSG.Rules))

			// Get the existing security group
			existingSG, err := cloudClient.SecurityGroups().Get(ctx, sgName)
			if err != nil {
				return fmt.Errorf("failed to get security group %s: %w", sgName, err)
			}

			// Update the rules
			existingSG.Spec.Rules = &sdkRules

			// Apply the update
			_, err = cloudClient.SecurityGroups().Update(ctx, sgName, existingSG)
			if err != nil {
				return fmt.Errorf("failed to update security group %s: %w", sgName, err)
			}
			lg.V(1).Info("Updated inline security group for machine", "name", sgName)
		}
	}

	return nil
}

// reconcileVMSecurityGroups updates security groups on an existing VM if they changed.
// This runs after VM creation to handle security group updates.
// When the machine has SG configuration but all groups resolve to empty, the VM's
// security groups are cleared (not left stale).
func (r *EvrocMachineReconciler) reconcileVMSecurityGroups(ctx context.Context, machine *infrav1.EvrocMachine, cloudClient cloud.ClientInterface, evrocCluster *infrav1.EvrocCluster) error {
	lg := log.FromContext(ctx)

	// Only reconcile if VM already exists
	if machine.Status.MachineID == "" {
		return nil
	}

	// Build list of desired security group names.
	// When config is nil the desired list is empty, which detaches all SGs
	// and triggers cleanup of any managed SGs tracked in status.
	var desiredSGNames []string

	if machine.Spec.NetworkingConfig != nil && machine.Spec.NetworkingConfig.SecurityGroups != nil {
		sgConfig := machine.Spec.NetworkingConfig.SecurityGroups

		if sgConfig.InheritFromCluster {
			_, isCP := machine.Labels[clusterv1.MachineControlPlaneLabel]
			desiredSGNames = append(desiredSGNames, clusterSecurityGroupNamesForRole(evrocCluster, isCP)...)
		}

		for _, inlineSG := range sgConfig.InlineSecurityGroups {
			sgName := fmt.Sprintf("%s-%s", machine.Name, inlineSG.Name)
			desiredSGNames = append(desiredSGNames, sgName)
		}
		desiredSGNames = append(desiredSGNames, sgConfig.ExistingIDs...)
	}

	lg.V(1).Info("Reconciling VM security groups", "machine", machine.Name, "securityGroups", desiredSGNames)

	if err := cloudClient.VirtualMachines().UpdateSecurityGroups(ctx, machine.Name, desiredSGNames); err != nil {
		return fmt.Errorf("failed to update security groups on VM %s: %w", machine.Name, err)
	}

	// Delete managed SGs that are no longer desired (zombie cleanup).
	desiredSet := make(map[string]bool, len(desiredSGNames))
	for _, name := range desiredSGNames {
		desiredSet[name] = true
	}
	if machine.Status.Resources != nil {
		for _, tracked := range machine.Status.Resources.SecurityGroups {
			if tracked.Managed && !desiredSet[tracked.ID] {
				lg.Info("Deleting stale managed security group", "name", tracked.ID)
				if err := cloudClient.SecurityGroups().Delete(ctx, tracked.ID); err != nil {
					if !helpers.IsNotFoundError(err) {
						return fmt.Errorf("failed to delete stale security group %s: %w", tracked.ID, err)
					}
				}
			}
		}
	}

	return nil
}

// reconcileLBBackend registers this control plane machine as a backend in the cluster's
// load balancer. For worker nodes or clusters without an LB, this is a no-op.
func (r *EvrocMachineReconciler) reconcileLBBackend(ctx context.Context, machine *infrav1.EvrocMachine, cloudClient cloud.ClientInterface, evrocCluster *infrav1.EvrocCluster) error {
	// Only control plane machines are LB backends.
	if _, isCP := machine.Labels[clusterv1.MachineControlPlaneLabel]; !isCP {
		return nil
	}

	if evrocCluster == nil {
		return fmt.Errorf("EvrocCluster not yet available, will retry")
	}

	// Derive the LB name from spec (deterministic naming), not from status.
	lbID, err := resolveLoadBalancerName(evrocCluster)
	if err != nil {
		return fmt.Errorf("failed to resolve LB name: %w", err)
	}

	lg := log.FromContext(ctx)
	lg.V(1).Info("Registering CP machine as LB backend",
		"machine", machine.Name,
		"loadBalancer", lbID)

	backend := cloud.Backend{
		Name: machine.Name,
	}

	if err := cloudClient.LoadBalancers().AddBackend(ctx, lbID, backend); err != nil {
		return fmt.Errorf("failed to register machine %s as LB backend: %w", machine.Name, err)
	}

	return nil
}

// reconcilePublicIPAttachment attaches a public IP on an existing VM when the spec requests one.
// It does not detach via reconciliation (empty desired name is a no-op); detach happens in deleteMachineFromCloud.
func (r *EvrocMachineReconciler) reconcilePublicIPAttachment(ctx context.Context, machine *infrav1.EvrocMachine, cloudClient cloud.ClientInterface, evrocCluster *infrav1.EvrocCluster) error {
	lg := log.FromContext(ctx)

	// Only reconcile if VM already exists
	if machine.Status.MachineID == "" {
		return nil
	}

	// Determine desired public IP state.
	// resolvePublicIPRef handles explicit machine config and cluster-level CP inheritance.
	desiredPublicIPName := resolvePublicIPRef(machine, evrocCluster)

	lg.V(1).Info("Reconciling VM public IP", "machine", machine.Name, "publicIP", desiredPublicIPName)

	// No public IP configured for this machine — nothing to attach.
	// Detachment is handled by the finalizer in deleteMachineFromCloud.
	if desiredPublicIPName == "" {
		return nil
	}

	if err := cloudClient.VirtualMachines().UpdatePublicIP(ctx, machine.Name, desiredPublicIPName); err != nil {
		return fmt.Errorf("failed to update public IP on VM %s: %w", machine.Name, err)
	}

	return nil
}

// reconcileVMDisks updates the disk list on an existing VM when additionalDisks change.
// This allows users to add or remove data disks without recreating the VM.
// reconcileVMDisks updates the disk list on an existing VM when additionalDisks change.
// Passes disk names to the cloud client which resolves them to FQIDs.
func (r *EvrocMachineReconciler) reconcileVMDisks(ctx context.Context, machine *infrav1.EvrocMachine, cloudClient cloud.ClientInterface) error {
	if machine.Status.MachineID == "" {
		return nil
	}

	// Build the desired disk name list: boot disk + additional disks.
	var diskNames []string
	if machine.Spec.RootDiskSize > 0 {
		diskNames = append(diskNames, fmt.Sprintf("%s-boot-disk", machine.Name))
	}
	for _, disk := range machine.Spec.AdditionalDisks {
		diskNames = append(diskNames, additionalDiskName(machine.Name, disk.Name))
	}

	if err := cloudClient.VirtualMachines().UpdateDisks(ctx, machine.Name, diskNames); err != nil {
		return fmt.Errorf("failed to update disks on VM %s: %w", machine.Name, err)
	}

	return nil
}

// reconcileVMPlacement updates the placement group on an existing VM.
//
// Only the placement group is patched: placement.zone is immutable on an
// existing VM, so including it made every reconcile fail with a 422 and
// requeue forever. The patch is skipped entirely when the placement group
// already matches, which keeps steady-state reconciles free of API writes.
func (r *EvrocMachineReconciler) reconcileVMPlacement(ctx context.Context, machine *infrav1.EvrocMachine, evrocVM *computetypes.VirtualMachine, cloudClient cloud.ClientInterface) error {
	if machine.Status.MachineID == "" {
		return nil
	}

	desired := r.buildPlacement(machine)
	if desired.PlacementGroupRef == nil {
		// No placement group requested — nothing to reconcile. Detaching an
		// existing group is not supported by the API.
		return nil
	}

	var current *string
	if evrocVM != nil {
		current = evrocVM.Spec.Placement.PlacementGroupRef
	}
	if current != nil && *current == *desired.PlacementGroupRef {
		return nil
	}

	if err := cloudClient.VirtualMachines().UpdatePlacement(ctx, machine.Name, computetypes.VirtualMachineSpecPlacement{
		PlacementGroupRef: desired.PlacementGroupRef,
	}); err != nil {
		return fmt.Errorf("failed to update placement on VM %s: %w", machine.Name, err)
	}

	return nil
}

func (r *EvrocMachineReconciler) reconcilePublicIPCreation(
	ctx context.Context,
	machine *infrav1.EvrocMachine,
	cloudClient cloud.ClientInterface,
	clusterInfo clusterLabelInfo,
) (pending bool, managedPublicIP *infrav1.ManagedPublicIP, err error) {
	if machine.Spec.NetworkingConfig == nil || machine.Spec.NetworkingConfig.PublicIP == nil {
		return false, nil, nil
	}

	config := machine.Spec.NetworkingConfig.PublicIP
	if config.ExistingID != nil {
		ip, getErr := cloudClient.PublicIPs().Get(ctx, *config.ExistingID)
		if getErr != nil {
			return false, nil, fmt.Errorf("failed to get existing public IP %s: %w", *config.ExistingID, getErr)
		}
		address := ""
		if ip.Status.PublicIPv4Address != nil {
			address = *ip.Status.PublicIPv4Address
		}
		return false, &infrav1.ManagedPublicIP{
			UID:     ip.Metadata.Uid.String(),
			ID:      *config.ExistingID,
			Address: address,
			Managed: false,
		}, nil
	}

	if !config.Enabled {
		return false, nil, nil
	}

	name := managedPublicIPName(machine.Name)
	exists, existsErr := cloudClient.PublicIPs().Exists(ctx, name)
	if existsErr != nil {
		return false, nil, fmt.Errorf("failed to check public IP existence: %w", existsErr)
	}
	if !exists {
		if _, createErr := cloudClient.PublicIPs().Create(ctx, name, machineResourceLabels(machine, clusterInfo.ID, clusterInfo.AdditionalLabels)); createErr != nil {
			return false, nil, fmt.Errorf("failed to create managed public IP %s: %w", name, createErr)
		}
		return true, &infrav1.ManagedPublicIP{
			ID:      name,
			Address: "",
			Managed: true,
		}, nil
	}

	ip, getErr := cloudClient.PublicIPs().Get(ctx, name)
	if getErr != nil {
		return false, nil, fmt.Errorf("failed to get managed public IP %s: %w", name, getErr)
	}
	if ip.Status.PublicIPv4Address == nil || *ip.Status.PublicIPv4Address == "" {
		// Surface a warning if the IP has been pending for too long.
		// This catches quota exhaustion or cloud allocation failures that
		// would otherwise cause the machine to hang silently.
		const publicIPTimeout = 5 * time.Minute
		if time.Since(machine.CreationTimestamp.Time) > publicIPTimeout {
			lg := log.FromContext(ctx)
			msg := fmt.Sprintf("Public IP %s has not been allocated after %s — possible quota exhaustion or cloud failure", name, publicIPTimeout)
			lg.Error(nil, msg, "machine", machine.Name)
			setMachineCondition(machine, "PublicIPReady", corev1.ConditionFalse, "PublicIPAllocationTimeout", msg)
			r.emitMachineWarningEvent(machine, "PublicIPAllocationTimeout", msg)
		}
		return true, &infrav1.ManagedPublicIP{
			UID:     ip.Metadata.Uid.String(),
			ID:      name,
			Address: "",
			Managed: true,
		}, nil
	}

	return false, &infrav1.ManagedPublicIP{
		UID:     ip.Metadata.Uid.String(),
		ID:      name,
		Address: *ip.Status.PublicIPv4Address,
		Managed: true,
	}, nil
}

func (r *EvrocMachineReconciler) reconcilePlacementGroup(
	ctx context.Context,
	machine *infrav1.EvrocMachine,
	cloudClient cloud.ClientInterface,
	_ clusterLabelInfo,
) (pending bool, placement *infrav1.ManagedPlacementGroup, err error) {
	if machine.Spec.PlacementConfig == nil || machine.Spec.PlacementConfig.ExistingGroupID == nil {
		return false, nil, nil
	}

	name := *machine.Spec.PlacementConfig.ExistingGroupID
	_, getErr := cloudClient.PlacementGroups().Get(ctx, name)
	if getErr != nil {
		return false, nil, fmt.Errorf("failed to get existing placement group %s: %w", name, getErr)
	}
	return false, &infrav1.ManagedPlacementGroup{
		ID:      name,
		Managed: false,
	}, nil
}

func (r *EvrocMachineReconciler) reconcileAdditionalDisks(
	ctx context.Context,
	machine *infrav1.EvrocMachine,
	cloudClient cloud.ClientInterface,
	clusterInfo clusterLabelInfo,
) (pending bool, disks []infrav1.ManagedDisk, err error) {
	if len(machine.Spec.AdditionalDisks) == 0 {
		// Clean up any managed disks that were previously created but are
		// no longer in the spec (zombie cleanup).
		if machine.Status.Resources != nil {
			lg := log.FromContext(ctx)
			for _, tracked := range machine.Status.Resources.AdditionalDisks {
				if !tracked.Managed {
					continue
				}
				lg.Info("Deleting stale managed additional disk", "name", tracked.ID)
				if delErr := cloudClient.Disks().Delete(ctx, tracked.ID); delErr != nil {
					if !helpers.IsNotFoundError(delErr) {
						return false, nil, fmt.Errorf("failed to delete stale additional disk %s: %w", tracked.ID, delErr)
					}
				}
			}
		}
		return false, nil, nil
	}

	managedDisks := make([]infrav1.ManagedDisk, 0, len(machine.Spec.AdditionalDisks))
	anyPending := false
	for _, diskSpec := range machine.Spec.AdditionalDisks {
		name := additionalDiskName(machine.Name, diskSpec.Name)
		disk, getErr := cloudClient.Disks().Get(ctx, name)
		if errors.Is(getErr, evroc.ErrNotFound) {
			image := ""
			if diskSpec.Image != nil {
				image = *diskSpec.Image
			}
			if _, createErr := cloudClient.Disks().Create(ctx, name, diskSpec.SizeGB, image, machine.Status.AvailabilityZone, machineResourceLabels(machine, clusterInfo.ID, clusterInfo.AdditionalLabels)); createErr != nil {
				return false, nil, fmt.Errorf("failed to create additional disk %s: %w", name, createErr)
			}
			managedDisks = append(managedDisks, infrav1.ManagedDisk{
				ID:      name,
				SizeGB:  diskSpec.SizeGB,
				Managed: true,
			})
			anyPending = true
			continue
		}
		if getErr != nil {
			return false, nil, fmt.Errorf("failed to get additional disk %s: %w", name, getErr)
		}
		if !compute.IsDiskReady(disk) {
			if reason, message := getDiskErrorCondition(disk); reason != "" {
				return false, nil, fmt.Errorf("additional disk %q failed: %s: %s", name, reason, message)
			}
			anyPending = true
		}
		managedDisks = append(managedDisks, infrav1.ManagedDisk{
			ID:      disk.Metadata.Id,
			UID:     disk.Metadata.Uid.String(),
			SizeGB:  diskSpec.SizeGB,
			Managed: true,
		})
	}
	if anyPending {
		return true, managedDisks, nil
	}

	// Delete managed disks that are tracked in status but no longer in spec.
	desiredDiskNames := make(map[string]bool, len(machine.Spec.AdditionalDisks))
	for _, diskSpec := range machine.Spec.AdditionalDisks {
		desiredDiskNames[additionalDiskName(machine.Name, diskSpec.Name)] = true
	}
	if machine.Status.Resources != nil {
		lg := log.FromContext(ctx)
		for _, tracked := range machine.Status.Resources.AdditionalDisks {
			if tracked.Managed && !desiredDiskNames[tracked.ID] {
				lg.Info("Deleting stale managed additional disk", "name", tracked.ID)
				if delErr := cloudClient.Disks().Delete(ctx, tracked.ID); delErr != nil {
					if !helpers.IsNotFoundError(delErr) {
						return false, nil, fmt.Errorf("failed to delete stale additional disk %s: %w", tracked.ID, delErr)
					}
				}
			}
		}
	}

	return false, managedDisks, nil
}

func (r *EvrocMachineReconciler) ensureResources(machine *infrav1.EvrocMachine) {
	if machine.Status.Resources == nil {
		machine.Status.Resources = &infrav1.MachineResources{}
	}
}

func (r *EvrocMachineReconciler) persistResourcesStatus(ctx context.Context, machine *infrav1.EvrocMachine) error {
	if machine.Status.Resources == nil {
		return nil
	}
	if err := r.Status().Update(ctx, machine); err != nil {
		return err
	}
	return r.Get(ctx, client.ObjectKeyFromObject(machine), machine)
}

func managedPublicIPName(machineName string) string {
	return fmt.Sprintf("%s-public-ip", machineName)
}

func additionalDiskName(machineName, diskName string) string {
	return fmt.Sprintf("%s-%s", machineName, diskName)
}

func mergeUserData(bootstrapUserData, machineUserData string) string {
	bootstrapUserData = strings.TrimSpace(bootstrapUserData)
	machineUserData = strings.TrimSpace(machineUserData)

	if bootstrapUserData == "" {
		return machineUserData
	}
	if machineUserData == "" {
		return bootstrapUserData
	}

	if strings.HasPrefix(bootstrapUserData, "#cloud-config") && strings.HasPrefix(machineUserData, "#cloud-config") {
		machineBody := strings.TrimPrefix(machineUserData, "#cloud-config")
		machineBody = strings.TrimLeft(machineBody, "\n")
		if machineBody == "" {
			return bootstrapUserData
		}
		return bootstrapUserData + "\n\n# merged machine userData\n" + machineBody
	}

	return buildMultipartUserData(bootstrapUserData, machineUserData)
}

func buildMultipartUserData(parts ...string) string {
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)

	for _, part := range parts {
		content := strings.TrimSpace(part)
		if content == "" {
			continue
		}

		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", cloudInitContentType(content))
		header.Set("MIME-Version", "1.0")
		header.Set("Content-Transfer-Encoding", "7bit")

		partWriter, err := writer.CreatePart(header)
		if err != nil {
			return strings.Join(parts, "\n")
		}
		if _, err := partWriter.Write([]byte(content + "\n")); err != nil {
			return strings.Join(parts, "\n")
		}
	}

	if err := writer.Close(); err != nil {
		return strings.Join(parts, "\n")
	}

	return fmt.Sprintf("MIME-Version: 1.0\nContent-Type: multipart/mixed; boundary=%q\n\n%s", writer.Boundary(), payload.String())
}

func cloudInitContentType(content string) string {
	switch {
	case strings.HasPrefix(content, "#cloud-config"):
		return "text/cloud-config"
	case strings.HasPrefix(content, "#!"):
		return "text/x-shellscript"
	default:
		return "text/plain"
	}
}

// getDiskErrorCondition returns the reason and message from a disk's Ready=False
// condition. If the disk has no error condition (e.g. still provisioning with no
// explicit failure), both return values are empty strings.
// This prevents the controller from silently requeueing forever when the platform
// reports a terminal disk error (e.g. "Disk is too small to contain disk image").
//
// The API reports reasons as free-form strings and briefly holds Ready=False with
// a non-failure reason (notably "DiskImageImportCompleted") before flipping the
// disk to Ready=True. Treating every unrecognised reason as terminal therefore
// failed healthy machines whenever a reconcile landed in that window, so only
// reasons that actually denote failure are surfaced here. Readiness itself is
// gated separately by compute.IsDiskReady, so anything not matched below simply
// keeps the caller requeueing.
func getDiskErrorCondition(disk *computetypes.Disk) (reason, message string) {
	if disk == nil || disk.Status.Conditions == nil {
		return "", ""
	}
	for _, cond := range *disk.Status.Conditions {
		if cond.Type != "Ready" || cond.Status != "False" || cond.Reason == "" {
			continue
		}
		if !isTerminalDiskReason(cond.Reason) {
			continue
		}
		return cond.Reason, cond.Message
	}
	return "", ""
}

// isTerminalDiskReason reports whether a disk's Ready=False reason denotes a
// failure the controller cannot recover from by waiting.
func isTerminalDiskReason(reason string) bool {
	lowered := strings.ToLower(reason)
	for _, marker := range []string{"failed", "failure", "error", "invalid", "unavailable", "denied", "exceeded", "toosmall"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// setMachineCondition sets or updates a condition on the machine's status.
func setMachineCondition(machine *infrav1.EvrocMachine, condType string, status corev1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	newCondition := clusterv1.Condition{
		Type:               clusterv1.ConditionType(condType),
		Status:             status,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	}
	if status == corev1.ConditionFalse {
		newCondition.Severity = clusterv1.ConditionSeverityError
	}

	for i, c := range machine.Status.Conditions {
		if c.Type == newCondition.Type {
			if c.Status == newCondition.Status {
				newCondition.LastTransitionTime = c.LastTransitionTime
			}
			machine.Status.Conditions[i] = newCondition
			return
		}
	}
	machine.Status.Conditions = append(machine.Status.Conditions, newCondition)
}

func extractMachineAddresses(vm *computetypes.VirtualMachine) []corev1.NodeAddress {
	var addresses []corev1.NodeAddress

	if vm.Status.Networking != nil {
		// Internal IP (private)
		if vm.Status.Networking.PrivateIPv4Address != nil && *vm.Status.Networking.PrivateIPv4Address != "" {
			addresses = append(addresses, corev1.NodeAddress{
				Type:    corev1.NodeInternalIP,
				Address: *vm.Status.Networking.PrivateIPv4Address,
			})
		}

		// External IP (public)
		if vm.Status.Networking.PublicIPv4Address != nil && *vm.Status.Networking.PublicIPv4Address != "" {
			addresses = append(addresses, corev1.NodeAddress{
				Type:    corev1.NodeExternalIP,
				Address: *vm.Status.Networking.PublicIPv4Address,
			})
		}

		// IPv6 address (dual-stack or ipv6-only)
		if vm.Status.Networking.Ipv6Address != nil && *vm.Status.Networking.Ipv6Address != "" {
			addresses = append(addresses, corev1.NodeAddress{
				Type:    corev1.NodeInternalIP,
				Address: *vm.Status.Networking.Ipv6Address,
			})
		}
	}

	// Hostname (use machine name as hostname)
	if vm.Metadata.Id != "" {
		addresses = append(addresses, corev1.NodeAddress{
			Type:    corev1.NodeHostName,
			Address: vm.Metadata.Id,
		})
	}

	return addresses
}
