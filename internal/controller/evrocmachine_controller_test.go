// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"fmt"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	computetypes "github.com/evroc-oss/evroc-go-sdk/types/compute"
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud/mocks"
)

// testScheme builds a scheme with all types needed by the machine controller tests.
func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return scheme
}

// testClusterObjects returns a CAPI Cluster and EvrocCluster pair for use in
// tests that exercise reconcileNormal (which needs to resolve the AZ from the
// cluster's failureDomains).
func testClusterObjects(namespace string) (*clusterv1.Cluster, *infrav1.EvrocCluster) {
	evrocCluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: namespace,
		},
		Spec: infrav1.EvrocClusterSpec{
			Project:        "test-project",
			Region:         "se-sto",
			FailureDomains: []string{"a"},
			CredentialsRef: &infrav1.SecretReference{
				Name: "test-creds",
			},
		},
	}
	capiCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: namespace,
		},
		Spec: clusterv1.ClusterSpec{
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				Kind: "EvrocCluster",
				Name: "test-cluster",
			},
		},
	}
	return capiCluster, evrocCluster
}

func TestEvrocMachineReconciler_Create(t *testing.T) {
	scheme := testScheme()

	vmID := uuid.New()
	vmName := "test-machine"
	diskName := vmName + "-boot-disk"
	privateIP := "10.0.1.10"
	publicIP := "194.14.80.138"
	bootstrapSecretName := "bootstrap-secret"

	// Create mock services
	mockClient := new(mocks.MockClient)
	mockVMService := new(mocks.MockVirtualMachineService)
	mockDiskService := new(mocks.MockDiskService)

	mockClient.On("Disks").Return(mockDiskService)
	mockClient.On("VirtualMachines").Return(mockVMService)
	mockClient.On("SDKClient").Return(testSDKClientForCluster())

	// Disk lifecycle: first Get → not found, then Create, then Get → ready (two more times)
	mockDiskService.On("Get", mock.Anything, diskName).Return(nil, evroc.ErrNotFound).Once()
	mockDiskService.On("Create", mock.Anything, diskName, 50, "ubuntu.22-04.1", "a", mock.Anything).
		Return(&computetypes.Disk{}, nil).Once()
	readyDisk := &computetypes.Disk{
		Status: computetypes.DiskStatus{
			Conditions: &[]computetypes.DiskStatusConditionsItem{
				{Type: "Ready", Status: "True"},
			},
		},
	}
	mockDiskService.On("Get", mock.Anything, diskName).Return(readyDisk, nil)

	// VM lifecycle: early cloud check returns not found on each reconcile
	// until the VM is created, then returns the ready VM.
	mockVMService.On("Get", mock.Anything, vmName).Return(nil, evroc.ErrNotFound).Times(2)
	mockVMService.On("Create", mock.Anything, mock.AnythingOfType("*compute.VirtualMachineRequest")).
		Return(&computetypes.VirtualMachine{
			Metadata: computetypes.RegionalMetadataResponse{
				Uid: vmID,
				Id:  vmName,
			},
		}, nil).Once()
	mockVMService.On("Get", mock.Anything, vmName).Return(&computetypes.VirtualMachine{
		Metadata: computetypes.RegionalMetadataResponse{
			Uid: vmID,
			Id:  vmName,
		},
		Status: computetypes.VirtualMachineStatus{
			Conditions: &[]computetypes.VirtualMachineStatusConditionsItem{
				{Type: "Ready", Status: "True"},
			},
			Networking: &computetypes.VirtualMachineStatusNetworking{
				PrivateIPv4Address: &privateIP,
				PublicIPv4Address:  &publicIP,
			},
			VirtualMachineStatus: func() *string { s := "Running"; return &s }(),
		},
	}, nil).Once()

	// Parent CAPI Machine with bootstrap data
	capiMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-capi-machine",
			Namespace: "default",
			UID:       "capi-machine-uid",
		},
		Spec: clusterv1.MachineSpec{
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: func() *string { s := bootstrapSecretName; return &s }(),
			},
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				Kind: "EvrocMachineTemplate",
				Name: "test-template",
			},
		},
	}

	// Bootstrap cloud-init secret
	bootstrapSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootstrapSecretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			"value": []byte("#!/bin/bash\necho hello"),
		},
	}

	capiCluster, evrocCluster := testClusterObjects("default")

	// EvrocMachine with owner reference pointing to the CAPI Machine
	evrocMachine := &infrav1.EvrocMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vmName,
			Namespace: "default",
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: "test-cluster",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "Machine",
					Name:       capiMachine.Name,
					UID:        capiMachine.UID,
				},
			},
		},
		Spec: infrav1.EvrocMachineSpec{
			Project:        "test-project",
			Region:         "se-sto",
			ComputeProfile: "a1a.s",
			Image:          "ubuntu.22-04.1",
			RootDiskSize:   50,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(capiCluster, evrocCluster, capiMachine, bootstrapSecret, evrocMachine).
		WithStatusSubresource(evrocMachine).
		Build()

	reconciler := &EvrocMachineReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: vmName, Namespace: "default"},
	}

	// Reconcile 1: adds finalizer (1s requeue)
	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, 1*time.Second, result.RequeueAfter)

	// Reconcile 2: disk not found → create disk (30s requeue)
	result, err = reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter)

	// Reconcile 3: disk ready, VM not found → create VM (30s requeue)
	result, err = reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter)

	// Reconcile 4: VM ready → status updated, providerID patching pending (15s requeue
	// because no workload cluster kubeconfig exists in this test)
	result, err = reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, requeueMedium, result.RequeueAfter)

	// Verify mock expectations
	mockDiskService.AssertExpectations(t)
	mockVMService.AssertExpectations(t)
	mockClient.AssertExpectations(t)

	// Verify status was updated
	var updatedMachine infrav1.EvrocMachine
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: vmName, Namespace: "default"}, &updatedMachine)
	assert.NoError(t, err)
	assert.NotEmpty(t, updatedMachine.Status.MachineID)
	assert.Contains(t, updatedMachine.Status.MachineID, vmID.String())
}

func TestEvrocMachineReconciler_CreateError(t *testing.T) {
	scheme := testScheme()

	vmName := "test-machine-error"
	diskName := vmName + "-boot-disk"
	bootstrapSecretName := "bootstrap-secret-err"

	mockClient := new(mocks.MockClient)
	mockVMService := new(mocks.MockVirtualMachineService)
	mockDiskService := new(mocks.MockDiskService)

	mockClient.On("Disks").Return(mockDiskService)
	mockClient.On("VirtualMachines").Return(mockVMService)
	mockClient.On("SDKClient").Return(testSDKClientForCluster())

	// Disk exists and is ready (skip disk creation phase)
	readyDisk := &computetypes.Disk{
		Status: computetypes.DiskStatus{
			Conditions: &[]computetypes.DiskStatusConditionsItem{
				{Type: "Ready", Status: "True"},
			},
		},
	}
	mockDiskService.On("Get", mock.Anything, diskName).Return(readyDisk, nil)

	// VM not found → create fails
	mockVMService.On("Get", mock.Anything, vmName).Return(nil, evroc.ErrNotFound)
	mockVMService.On("Create", mock.Anything, mock.AnythingOfType("*compute.VirtualMachineRequest")).
		Return(nil, evroc.ErrBadRequest)

	capiMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-capi-machine-err",
			Namespace: "default",
			UID:       "capi-machine-err-uid",
		},
		Spec: clusterv1.MachineSpec{
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: func() *string { s := bootstrapSecretName; return &s }(),
			},
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				Kind: "EvrocMachineTemplate",
				Name: "test-template",
			},
		},
	}

	bootstrapSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootstrapSecretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			"value": []byte("#!/bin/bash\necho hello"),
		},
	}

	capiCluster, evrocCluster := testClusterObjects("default")

	evrocMachine := &infrav1.EvrocMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vmName,
			Namespace: "default",
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: "test-cluster",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "Machine",
					Name:       capiMachine.Name,
					UID:        capiMachine.UID,
				},
			},
		},
		Spec: infrav1.EvrocMachineSpec{
			Project:        "test-project",
			Region:         "se-sto",
			ComputeProfile: "a1a.s",
			Image:          "ubuntu.22-04.1",
			RootDiskSize:   50,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(capiCluster, evrocCluster, capiMachine, bootstrapSecret, evrocMachine).
		WithStatusSubresource(evrocMachine).
		Build()

	reconciler := &EvrocMachineReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: vmName, Namespace: "default"},
	}

	// Reconcile 1: adds finalizer
	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, result.RequeueAfter > 0)

	// Reconcile 2: disk ready, VM creation fails
	result, err = reconciler.Reconcile(context.Background(), req)
	assert.Error(t, err)
	assert.ErrorIs(t, err, evroc.ErrBadRequest)

	mockDiskService.AssertExpectations(t)
	mockVMService.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestEvrocMachineReconciler_Delete(t *testing.T) {
	scheme := testScheme()

	vmName := "test-machine-delete"
	diskName := vmName + "-boot-disk"
	now := metav1.Now()

	mockClient := new(mocks.MockClient)
	mockVMService := new(mocks.MockVirtualMachineService)
	mockDiskService := new(mocks.MockDiskService)
	mockSGService := new(mocks.MockSecurityGroupService)
	mockPIPService := new(mocks.MockPublicIPService)

	mockClient.On("VirtualMachines").Return(mockVMService)
	mockClient.On("Disks").Return(mockDiskService)
	mockClient.On("SecurityGroups").Return(mockSGService)
	mockClient.On("PublicIPs").Return(mockPIPService)
	// SDKClient is not called during delete operations

	mockVMService.On("Exists", mock.Anything, vmName).Return(false, nil)
	mockSGService.On("ListByMachineOwner", mock.Anything, "machine-owner-delete").Return([]string{}, nil)
	mockPIPService.On("ListByOwner", mock.Anything, "machine-owner-delete").Return([]string{}, nil)
	mockDiskService.On("ListByOwner", mock.Anything, "machine-owner-delete").Return([]string{diskName}, nil)
	mockDiskService.On("Delete", mock.Anything, diskName).Return(nil)

	capiCluster, evrocCluster := testClusterObjects("default")

	evrocMachine := &infrav1.EvrocMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:              vmName,
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{machineFinalizer},
			Annotations: map[string]string{
				machineOwnershipIDAnnotation: "machine-owner-delete",
			},
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: "test-cluster",
			},
		},
		Spec: infrav1.EvrocMachineSpec{
			Project:        "test-project",
			Region:         "se-sto",
			ComputeProfile: "a1a.s",
			Image:          "ubuntu.22-04.1",

			RootDiskSize: 50,
		},
		Status: infrav1.EvrocMachineStatus{
			Resources: &infrav1.MachineResources{
				BootDisk: &infrav1.ManagedDisk{
					ID:      diskName,
					SizeGB:  50,
					Managed: true,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(capiCluster, evrocCluster, evrocMachine).
		WithStatusSubresource(evrocMachine).
		Build()

	reconciler := &EvrocMachineReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: vmName, Namespace: "default"},
	}

	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	mockVMService.AssertExpectations(t)
	mockDiskService.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestExtractMachineAddresses(t *testing.T) {
	vmName := "test-vm"
	privateIP := "10.0.1.10"
	publicIP := "194.14.80.138"

	vm := &computetypes.VirtualMachine{
		Metadata: computetypes.RegionalMetadataResponse{
			Id: vmName,
		},
		Status: computetypes.VirtualMachineStatus{
			Networking: &computetypes.VirtualMachineStatusNetworking{
				PrivateIPv4Address: &privateIP,
				PublicIPv4Address:  &publicIP,
			},
		},
	}

	addresses := extractMachineAddresses(vm)

	// Should have 3 addresses: InternalIP, ExternalIP, Hostname
	assert.Len(t, addresses, 3)

	// Check each address type
	var hasInternal, hasExternal, hasHostname bool
	for _, addr := range addresses {
		switch addr.Type {
		case corev1.NodeInternalIP:
			assert.Equal(t, privateIP, addr.Address)
			hasInternal = true
		case corev1.NodeExternalIP:
			assert.Equal(t, publicIP, addr.Address)
			hasExternal = true
		case corev1.NodeHostName:
			assert.Equal(t, vmName, addr.Address)
			hasHostname = true
		}
	}

	assert.True(t, hasInternal, "Should have InternalIP")
	assert.True(t, hasExternal, "Should have ExternalIP")
	assert.True(t, hasHostname, "Should have Hostname")
}

func TestMergeUserData_CloudConfig(t *testing.T) {
	bootstrap := "#cloud-config\npackages:\n  - curl"
	machine := "#cloud-config\nwrite_files:\n  - path: /etc/example\n    content: hello"

	merged := mergeUserData(bootstrap, machine)

	assert.True(t, strings.HasPrefix(merged, "#cloud-config"))
	assert.Contains(t, merged, "packages:")
	assert.Contains(t, merged, "write_files:")
	assert.Contains(t, merged, "merged machine userData")
}

func TestMergeUserData_MultipartFallback(t *testing.T) {
	bootstrap := "#cloud-config\npackages:\n  - jq"
	machine := "#!/bin/bash\necho hello"

	merged := mergeUserData(bootstrap, machine)

	assert.Contains(t, merged, "MIME-Version: 1.0")
	assert.Contains(t, merged, "Content-Type: multipart/mixed;")
	assert.Contains(t, merged, "text/cloud-config")
	assert.Contains(t, merged, "text/x-shellscript")
	assert.Contains(t, merged, "#!/bin/bash")
}

func TestReconcile_PersistsResourcesBeforePendingRequeue(t *testing.T) {
	scheme := testScheme()

	vmName := "test-machine-pending-managed"
	additionalDiskName := vmName + "-data"
	bootstrapSecretName := "bootstrap-secret-pending-managed"

	mockClient := new(mocks.MockClient)
	mockDiskService := new(mocks.MockDiskService)
	mockVMService := new(mocks.MockVirtualMachineService)
	mockClient.On("Disks").Return(mockDiskService)
	mockClient.On("VirtualMachines").Return(mockVMService)
	mockClient.On("SDKClient").Maybe().Return(testSDKClientForCluster())

	// Early cloud check: VM does not exist yet.
	mockVMService.On("Get", mock.Anything, vmName).Return(nil, evroc.ErrNotFound)

	// Additional disk does not exist yet, so controller creates it and requeues.
	mockDiskService.On("Get", mock.Anything, additionalDiskName).Return(nil, evroc.ErrNotFound).Once()
	mockDiskService.On("Create", mock.Anything, additionalDiskName, 20, "", "a", mock.Anything).
		Return(&computetypes.Disk{}, nil).Once()

	capiMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-capi-machine-pending-managed",
			Namespace: "default",
			UID:       "capi-machine-pending-managed-uid",
		},
		Spec: clusterv1.MachineSpec{
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: func() *string { s := bootstrapSecretName; return &s }(),
			},
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				Kind: "EvrocMachineTemplate",
				Name: "test-template",
			},
		},
	}

	bootstrapSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootstrapSecretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			"value": []byte("#cloud-config\nruncmd:\n- echo hello"),
		},
	}

	capiCluster, evrocCluster := testClusterObjects("default")

	evrocMachine := &infrav1.EvrocMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vmName,
			Namespace: "default",
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: "test-cluster",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "Machine",
					Name:       capiMachine.Name,
					UID:        capiMachine.UID,
				},
			},
		},
		Spec: infrav1.EvrocMachineSpec{
			Project:        "test-project",
			Region:         "se-sto",
			ComputeProfile: "a1a.s",
			Image:          "ubuntu.22-04.1",
			RootDiskSize:   0,
			AdditionalDisks: []infrav1.AdditionalDiskSpec{
				{
					Name:   "data",
					SizeGB: 20,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(capiCluster, evrocCluster, capiMachine, bootstrapSecret, evrocMachine).
		WithStatusSubresource(evrocMachine).
		Build()

	reconciler := &EvrocMachineReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: vmName, Namespace: "default"},
	}

	// Reconcile 1: add finalizer.
	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, 1*time.Second, result.RequeueAfter)

	// Reconcile 2: additional disk created, pending -> requeue.
	result, err = reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter)

	// Managed resource tracking must already be persisted before requeue.
	var updatedMachine infrav1.EvrocMachine
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: vmName, Namespace: "default"}, &updatedMachine)
	assert.NoError(t, err)
	if assert.NotNil(t, updatedMachine.Status.Resources) {
		if assert.Len(t, updatedMachine.Status.Resources.AdditionalDisks, 1) {
			d := updatedMachine.Status.Resources.AdditionalDisks[0]
			assert.Equal(t, additionalDiskName, d.ID)
			assert.True(t, d.Managed)
		}
	}

	mockDiskService.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

// TestBuildOSSettings_SSHKeyInjection tests that SSH keys are always injected via cloud-init.
// The Evroc API ssh.authorizedKeys is never used because cloud-init overrides it.
func TestBuildOSSettings_SSHKeyInjection(t *testing.T) {
	reconciler := &EvrocMachineReconciler{}

	tests := []struct {
		name           string
		sshKey         string
		userData       string
		expectedInUser bool
		expectError    bool
	}{
		{
			name:           "SSH key with cloud-config userData",
			sshKey:         "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFeENOwB0QwUEicJGrFxt44yiShgBWzANhpE/5gNw041",
			userData:       "#cloud-config\npackages:\n  - curl",
			expectedInUser: true,
		},
		{
			name:           "SSH key with empty userData generates cloud-config",
			sshKey:         "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFeENOwB0QwUEicJGrFxt44yiShgBWzANhpE/5gNw041",
			userData:       "",
			expectedInUser: true,
		},
		{
			name:           "No SSH key preserves userData",
			sshKey:         "",
			userData:       "#cloud-config\npackages:\n  - curl",
			expectedInUser: true,
		},
		{
			name:        "SSH key with shell script userData returns error",
			sshKey:      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFeENOwB0QwUEicJGrFxt44yiShgBWzANhpE/5gNw041",
			userData:    "#!/bin/bash\necho hello",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machine := &infrav1.EvrocMachine{
				Spec: infrav1.EvrocMachineSpec{
					SSHKey: tt.sshKey,
				},
			}

			osSettings, err := reconciler.buildOSSettings(machine, tt.userData)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, osSettings)
				return
			}
			assert.NoError(t, err)

			if tt.sshKey == "" && tt.userData == "" {
				assert.Nil(t, osSettings)
				return
			}

			assert.NotNil(t, osSettings)
			assert.Nil(t, osSettings.Ssh, "SSH keys are always via cloud-init, never via API Ssh field")

			if tt.expectedInUser {
				assert.NotNil(t, osSettings.CloudInitUserData)
				if tt.sshKey != "" {
					assert.Contains(t, *osSettings.CloudInitUserData, tt.sshKey)
					assert.Contains(t, *osSettings.CloudInitUserData, "ssh_authorized_keys")
				}
			}
		})
	}
}

// TestInjectSSHKeyIntoCloudInit tests the SSH key injection logic
func TestInjectSSHKeyIntoCloudInit(t *testing.T) {
	tests := []struct {
		name     string
		userData string
		sshKey   string
		expected string
	}{
		{
			name:     "Empty userData with SSH key",
			userData: "",
			sshKey:   "ssh-ed25519 AAAAC3test",
			expected: "#cloud-config\nssh_authorized_keys:\n- \"ssh-ed25519 AAAAC3test\"\nusers:\n  - name: evroc-user\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    shell: /bin/bash\n    ssh_authorized_keys:\n      - \"ssh-ed25519 AAAAC3test\"\n",
		},
		{
			name:     "Cloud-config userData with SSH key",
			userData: "#cloud-config\npackages:\n  - curl",
			sshKey:   "ssh-ed25519 AAAAC3test",
			expected: "#cloud-config\npackages:\n  - curl\nssh_authorized_keys:\n- \"ssh-ed25519 AAAAC3test\"\nusers:\n  - name: evroc-user\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    shell: /bin/bash\n    ssh_authorized_keys:\n      - \"ssh-ed25519 AAAAC3test\"\n",
		},
		{
			name:     "Shell script userData with SSH key",
			userData: "#!/bin/bash\necho hello",
			sshKey:   "ssh-ed25519 AAAAC3test",
			expected: "#!/bin/bash\necho hello",
		},
		{
			name:     "Empty SSH key",
			userData: "#cloud-config\npackages:\n  - curl",
			sshKey:   "",
			expected: "#cloud-config\npackages:\n  - curl",
		},
		{
			name:     "RKE2 jinja template with SSH key",
			userData: "## template: jinja\n#cloud-config\nwrite_files:\n  - path: /etc/test",
			sshKey:   "ssh-ed25519 AAAAC3test",
			expected: "## template: jinja\n#cloud-config\nwrite_files:\n  - path: /etc/test\nssh_authorized_keys:\n- \"ssh-ed25519 AAAAC3test\"\nusers:\n  - name: evroc-user\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    shell: /bin/bash\n    ssh_authorized_keys:\n      - \"ssh-ed25519 AAAAC3test\"\n",
		},
		{
			name:     "Existing users section with ssh_authorized_keys - skip both",
			userData: "#cloud-config\nwrite_files:\n  - path: /etc/test\nusers:\n  - name: myuser\n    ssh_authorized_keys:\n      - ssh-ed25519 existing-key\n",
			sshKey:   "ssh-ed25519 AAAAC3test",
			expected: "#cloud-config\nwrite_files:\n  - path: /etc/test\nusers:\n  - name: myuser\n    ssh_authorized_keys:\n      - ssh-ed25519 existing-key\n",
		},
		{
			name:     "Existing users without ssh_authorized_keys - inject only ssh_authorized_keys",
			userData: "#cloud-config\nwrite_files:\n  - path: /etc/test\nusers:\n  - name: myuser\n",
			sshKey:   "ssh-ed25519 AAAAC3test",
			expected: "#cloud-config\nwrite_files:\n  - path: /etc/test\nusers:\n  - name: myuser\n\nssh_authorized_keys:\n- \"ssh-ed25519 AAAAC3test\"\n",
		},
		{
			name:     "Existing users and ssh_authorized_keys - skip both",
			userData: "#cloud-config\nssh_authorized_keys:\n  - ssh-ed25519 existing-key\nusers:\n  - name: myuser\n",
			sshKey:   "ssh-ed25519 AAAAC3test",
			expected: "#cloud-config\nssh_authorized_keys:\n  - ssh-ed25519 existing-key\nusers:\n  - name: myuser\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := injectSSHKeyIntoCloudInit(tt.userData, tt.sshKey)

			// Shell script format should return an error
			if tt.name == "Shell script userData with SSH key" {
				assert.Error(t, err, "Should return error for unsupported format")
				assert.Contains(t, err.Error(), "unsupported cloud-init format")
			} else {
				assert.NoError(t, err, "Should not return error for cloud-config format")
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// TestBuildOSSettings_CRDParameterCoverage tests all EvrocMachineSpec fields are properly translated
func TestBuildOSSettings_CRDParameterCoverage(t *testing.T) {
	reconciler := &EvrocMachineReconciler{}

	tests := []struct {
		name     string
		spec     infrav1.EvrocMachineSpec
		userData string
		validate func(*testing.T, *computetypes.VirtualMachineSpecOsSettings)
	}{
		{
			name: "All parameters set",
			spec: infrav1.EvrocMachineSpec{
				Project:        "prj_test",
				Region:         "se-sto",
				ComputeProfile: "a1a.m",
				Image:          "ubuntu.24-04.1",
				RootDiskSize:   100,
				SSHKey:         "ssh-ed25519 AAAAC3test",
			},
			userData: "#cloud-config\nwrite_files:\n  - path: /test",
			validate: func(t *testing.T, os *computetypes.VirtualMachineSpecOsSettings) {
				assert.NotNil(t, os)
				assert.NotNil(t, os.CloudInitUserData)
				assert.Contains(t, *os.CloudInitUserData, "ssh-ed25519 AAAAC3test")
				assert.Contains(t, *os.CloudInitUserData, "write_files")
				assert.Nil(t, os.Ssh, "SSH keys are injected via cloud-init")
			},
		},
		{
			name: "Only userData no SSH key",
			spec: infrav1.EvrocMachineSpec{
				Project:        "prj_test",
				Region:         "se-sto",
				ComputeProfile: "a1a.m",
				Image:          "ubuntu.24-04.1",
			},
			userData: "#cloud-config\nruncmd:\n  - echo test",
			validate: func(t *testing.T, os *computetypes.VirtualMachineSpecOsSettings) {
				assert.NotNil(t, os)
				assert.NotNil(t, os.CloudInitUserData)
				assert.Contains(t, *os.CloudInitUserData, "runcmd")
				assert.NotContains(t, *os.CloudInitUserData, "ssh_authorized_keys")
			},
		},
		{
			name: "Only SSH key no userData",
			spec: infrav1.EvrocMachineSpec{
				Project:        "prj_test",
				Region:         "se-sto",
				ComputeProfile: "a1a.m",
				Image:          "ubuntu.24-04.1",
				SSHKey:         "ssh-rsa AAAAB3test",
			},
			userData: "",
			validate: func(t *testing.T, os *computetypes.VirtualMachineSpecOsSettings) {
				assert.NotNil(t, os)
				assert.NotNil(t, os.CloudInitUserData)
				assert.Contains(t, *os.CloudInitUserData, "ssh-rsa AAAAB3test")
				assert.Contains(t, *os.CloudInitUserData, "#cloud-config")
				assert.Nil(t, os.Ssh, "SSH keys are injected via cloud-init")
			},
		},
		{
			name: "Neither SSH key nor userData",
			spec: infrav1.EvrocMachineSpec{
				Project:        "prj_test",
				Region:         "se-sto",
				ComputeProfile: "a1a.m",
				Image:          "ubuntu.24-04.1",
			},
			userData: "",
			validate: func(t *testing.T, os *computetypes.VirtualMachineSpecOsSettings) {
				assert.Nil(t, os, "OS settings should be nil when no SSH key and no userData")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machine := &infrav1.EvrocMachine{
				Spec: tt.spec,
			}
			osSettings, err := reconciler.buildOSSettings(machine, tt.userData)
			assert.NoError(t, err, "buildOSSettings should not return error")
			tt.validate(t, osSettings)
		})
	}
}

// TestResolveTemplateSSHKey tests the SSH key fallback from template
func TestResolveTemplateSSHKey(t *testing.T) {
	scheme := testScheme()

	tests := []struct {
		name           string
		machine        *infrav1.EvrocMachine
		template       *infrav1.EvrocMachineTemplate
		expectedSSHKey string
		expectError    bool
	}{
		{
			name: "SSH key resolved from template",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Annotations: map[string]string{
						"cluster.x-k8s.io/cloned-from-name":      "test-template",
						"cluster.x-k8s.io/cloned-from-groupkind": "EvrocMachineTemplate.infrastructure.cluster.x-k8s.io",
					},
				},
				Spec: infrav1.EvrocMachineSpec{
					SSHKey: "", // Empty, should be resolved from template
				},
			},
			template: &infrav1.EvrocMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-template",
					Namespace: "default",
				},
				Spec: infrav1.EvrocMachineTemplateSpec{
					Template: infrav1.EvrocMachineTemplateResource{
						Spec: infrav1.EvrocMachineSpec{
							SSHKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFeENOwB0QwUEicJGrFxt44yiShgBWzANhpE/5gNw041",
						},
					},
				},
			},
			expectedSSHKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFeENOwB0QwUEicJGrFxt44yiShgBWzANhpE/5gNw041",
			expectError:    false,
		},
		{
			name: "No annotation returns empty",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine-no-annotation",
					Namespace: "default",
				},
				Spec: infrav1.EvrocMachineSpec{
					SSHKey: "",
				},
			},
			expectedSSHKey: "",
			expectError:    false,
		},
		{
			name: "Wrong groupkind returns empty",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine-wrong-kind",
					Namespace: "default",
					Annotations: map[string]string{
						"cluster.x-k8s.io/cloned-from-name":      "test-template",
						"cluster.x-k8s.io/cloned-from-groupkind": "SomeOtherTemplate.infrastructure.cluster.x-k8s.io",
					},
				},
				Spec: infrav1.EvrocMachineSpec{
					SSHKey: "",
				},
			},
			expectedSSHKey: "",
			expectError:    false,
		},
		{
			name: "Template not found returns error",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine-missing-template",
					Namespace: "default",
					Annotations: map[string]string{
						"cluster.x-k8s.io/cloned-from-name":      "nonexistent-template",
						"cluster.x-k8s.io/cloned-from-groupkind": "EvrocMachineTemplate.infrastructure.cluster.x-k8s.io",
					},
				},
				Spec: infrav1.EvrocMachineSpec{
					SSHKey: "",
				},
			},
			expectedSSHKey: "",
			expectError:    true,
		},
		{
			name: "Template with empty SSH key returns empty",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine-empty-template-key",
					Namespace: "default",
					Annotations: map[string]string{
						"cluster.x-k8s.io/cloned-from-name":      "test-template-empty",
						"cluster.x-k8s.io/cloned-from-groupkind": "EvrocMachineTemplate.infrastructure.cluster.x-k8s.io",
					},
				},
				Spec: infrav1.EvrocMachineSpec{
					SSHKey: "",
				},
			},
			template: &infrav1.EvrocMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-template-empty",
					Namespace: "default",
				},
				Spec: infrav1.EvrocMachineTemplateSpec{
					Template: infrav1.EvrocMachineTemplateResource{
						Spec: infrav1.EvrocMachineSpec{
							SSHKey: "", // Template also has no SSH key
						},
					},
				},
			},
			expectedSSHKey: "",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			objects = append(objects, tt.machine)
			if tt.template != nil {
				objects = append(objects, tt.template)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			reconciler := &EvrocMachineReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			sshKey, err := reconciler.resolveTemplateSSHKey(context.Background(), tt.machine)

			if tt.expectError {
				assert.Error(t, err, "Expected an error but got none")
			} else {
				assert.NoError(t, err, "Expected no error")
				assert.Equal(t, tt.expectedSSHKey, sshKey, "SSH key should match expected value")
			}
		})
	}
}

func TestReconcileVMSecurityGroups(t *testing.T) {
	tests := []struct {
		name             string
		machine          *infrav1.EvrocMachine
		evrocCluster     *infrav1.EvrocCluster
		expectedSGNames  []string
		mockExpectations func(*mocks.MockClient, *mocks.MockVirtualMachineService)
		expectError      bool
	}{
		{
			name: "VM doesn't exist yet - should skip",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Status: infrav1.EvrocMachineStatus{
					MachineID: "", // VM not created yet
				},
			},
			evrocCluster: nil,
			mockExpectations: func(mockClient *mocks.MockClient, mockVMService *mocks.MockVirtualMachineService) {
				// No expectations - function returns early without calling SDK
			},
			expectError: false,
		},
		{
			name: "VM exists with machine-level security groups only",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						SecurityGroups: &infrav1.MachineSecurityGroupsConfig{
							InlineSecurityGroups: []infrav1.InlineSecurityGroup{
								{
									Name: "ssh",
								},
								{
									Name: "https",
								},
							},
						},
					},
				},
				Status: infrav1.EvrocMachineStatus{
					MachineID: "vm-123",
				},
			},
			evrocCluster:    nil,
			expectedSGNames: []string{"test-machine-ssh", "test-machine-https"},
			mockExpectations: func(mockClient *mocks.MockClient, mockVMService *mocks.MockVirtualMachineService) {
				mockClient.On("VirtualMachines").Return(mockVMService)
				mockVMService.On("UpdateSecurityGroups", mock.Anything, "test-machine",
					[]string{"test-machine-ssh", "test-machine-https"}).Return(nil)
			},
			expectError: false,
		},
		{
			name: "VM exists with cluster-level security groups inherited",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						SecurityGroups: &infrav1.MachineSecurityGroupsConfig{
							InheritFromCluster: true,
						},
					},
				},
				Status: infrav1.EvrocMachineStatus{
					MachineID: "vm-123",
				},
			},
			evrocCluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Status: infrav1.EvrocClusterStatus{
					Resources: &infrav1.ClusterResources{
						SecurityGroups: []infrav1.ManagedSecurityGroup{
							{
								UID:  "sg-123",
								ID:   "test-cluster-api-server",
								Role: "common",
							},
						},
					},
				},
			},
			expectedSGNames: []string{"test-cluster-api-server"},
			mockExpectations: func(mockClient *mocks.MockClient, mockVMService *mocks.MockVirtualMachineService) {
				mockClient.On("VirtualMachines").Return(mockVMService)
				mockVMService.On("UpdateSecurityGroups", mock.Anything, "test-machine",
					[]string{"test-cluster-api-server"}).Return(nil)
			},
			expectError: false,
		},
		{
			name: "VM exists with both cluster and machine security groups",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						SecurityGroups: &infrav1.MachineSecurityGroupsConfig{
							InheritFromCluster: true,
							InlineSecurityGroups: []infrav1.InlineSecurityGroup{
								{
									Name: "node-ports",
								},
							},
						},
					},
				},
				Status: infrav1.EvrocMachineStatus{
					MachineID: "vm-123",
				},
			},
			evrocCluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Status: infrav1.EvrocClusterStatus{
					Resources: &infrav1.ClusterResources{
						SecurityGroups: []infrav1.ManagedSecurityGroup{
							{
								UID:  "sg-123",
								ID:   "test-cluster-api-server",
								Role: "common",
							},
						},
					},
				},
			},
			expectedSGNames: []string{"test-cluster-api-server", "test-machine-node-ports"},
			mockExpectations: func(mockClient *mocks.MockClient, mockVMService *mocks.MockVirtualMachineService) {
				mockClient.On("VirtualMachines").Return(mockVMService)
				mockVMService.On("UpdateSecurityGroups", mock.Anything, "test-machine",
					[]string{"test-cluster-api-server", "test-machine-node-ports"}).Return(nil)
			},
			expectError: false,
		},
		{
			name: "UpdateSecurityGroups fails - should return error",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						SecurityGroups: &infrav1.MachineSecurityGroupsConfig{
							InlineSecurityGroups: []infrav1.InlineSecurityGroup{
								{
									Name: "ssh",
								},
							},
						},
					},
				},
				Status: infrav1.EvrocMachineStatus{
					MachineID: "vm-123",
				},
			},
			evrocCluster: nil,
			mockExpectations: func(mockClient *mocks.MockClient, mockVMService *mocks.MockVirtualMachineService) {
				mockClient.On("VirtualMachines").Return(mockVMService)
				mockVMService.On("UpdateSecurityGroups", mock.Anything, "test-machine",
					[]string{"test-machine-ssh"}).Return(evroc.ErrBadRequest)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mocks.MockClient)
			mockVMService := new(mocks.MockVirtualMachineService)

			if tt.mockExpectations != nil {
				tt.mockExpectations(mockClient, mockVMService)
			}

			scheme := testScheme()
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.evrocCluster != nil {
				builder = builder.WithObjects(tt.evrocCluster)
			}
			fakeK8sClient := builder.Build()

			reconciler := &EvrocMachineReconciler{
				Client:        fakeK8sClient,
				clientFactory: staticClientFactory(mockClient),
			}

			err := reconciler.reconcileVMSecurityGroups(context.Background(), tt.machine, mockClient, tt.evrocCluster)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockClient.AssertExpectations(t)
			mockVMService.AssertExpectations(t)
		})
	}
}

func TestReconcileVMPublicIP(t *testing.T) {
	tests := []struct {
		name             string
		machine          *infrav1.EvrocMachine
		expectedIPName   string
		mockExpectations func(*mocks.MockClient, *mocks.MockVirtualMachineService)
		expectError      bool
	}{
		{
			name: "VM doesn't exist yet - should skip",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Status: infrav1.EvrocMachineStatus{
					MachineID: "", // VM not created yet
				},
			},
			mockExpectations: func(mockClient *mocks.MockClient, mockVMService *mocks.MockVirtualMachineService) {
				// No expectations - function returns early without calling SDK
			},
			expectError: false,
		},
		{
			name: "VM exists with managed public IP enabled",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						PublicIP: &infrav1.PublicIPConfig{
							Enabled: true,
						},
					},
				},
				Status: infrav1.EvrocMachineStatus{
					MachineID: "vm-123",
				},
			},
			expectedIPName: "test-machine-public-ip",
			mockExpectations: func(mockClient *mocks.MockClient, mockVMService *mocks.MockVirtualMachineService) {
				mockClient.On("VirtualMachines").Return(mockVMService)
				mockVMService.On("UpdatePublicIP", mock.Anything, "test-machine", "test-machine-public-ip").Return(nil)
			},
			expectError: false,
		},
		{
			name: "VM exists with existing public IP",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						PublicIP: &infrav1.PublicIPConfig{
							ExistingID: func() *string { s := "my-existing-ip"; return &s }(),
						},
					},
				},
				Status: infrav1.EvrocMachineStatus{
					MachineID: "vm-123",
				},
			},
			expectedIPName: "my-existing-ip",
			mockExpectations: func(mockClient *mocks.MockClient, mockVMService *mocks.MockVirtualMachineService) {
				mockClient.On("VirtualMachines").Return(mockVMService)
				mockVMService.On("UpdatePublicIP", mock.Anything, "test-machine", "my-existing-ip").Return(nil)
			},
			expectError: false,
		},
		{
			name: "VM exists with public IP disabled - no UpdatePublicIP (detach only on machine delete)",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						PublicIP: &infrav1.PublicIPConfig{
							Enabled: false,
						},
					},
				},
				Status: infrav1.EvrocMachineStatus{
					MachineID: "vm-123",
				},
			},
			expectedIPName: "",
			mockExpectations: func(mockClient *mocks.MockClient, mockVMService *mocks.MockVirtualMachineService) {
				// Empty desired name → reconcilePublicIPAttachment returns without calling the API.
			},
			expectError: false,
		},
		{
			name: "UpdatePublicIP fails - should return error",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						PublicIP: &infrav1.PublicIPConfig{
							Enabled: true,
						},
					},
				},
				Status: infrav1.EvrocMachineStatus{
					MachineID: "vm-123",
				},
			},
			mockExpectations: func(mockClient *mocks.MockClient, mockVMService *mocks.MockVirtualMachineService) {
				mockClient.On("VirtualMachines").Return(mockVMService)
				mockVMService.On("UpdatePublicIP", mock.Anything, "test-machine", "test-machine-public-ip").Return(evroc.ErrBadRequest)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mocks.MockClient)
			mockVMService := new(mocks.MockVirtualMachineService)

			if tt.mockExpectations != nil {
				tt.mockExpectations(mockClient, mockVMService)
			}

			reconciler := &EvrocMachineReconciler{
				clientFactory: staticClientFactory(mockClient),
			}

			err := reconciler.reconcilePublicIPAttachment(context.Background(), tt.machine, mockClient, nil)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockClient.AssertExpectations(t)
			mockVMService.AssertExpectations(t)
		})
	}
}

func TestParseQuotaError(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		contains []string
	}{
		{
			name:   "full quota error message",
			errMsg: "not enough quota to perform request. Requested additional 4 vCPUs and 8GB memory. Only 2 vCPUs (out of 16 in quota) and 4GB memory (out of 32GB in quota) available",
			contains: []string{
				"Quota exceeded",
				"4 vCPUs",
				"8GB",
				"2/16",
				"4GB/32GB",
			},
		},
		{
			name:     "unrecognized quota message fallback",
			errMsg:   "some quota error without expected format",
			contains: []string{"Quota exceeded", "some quota error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseQuotaError(tt.errMsg)
			for _, c := range tt.contains {
				assert.Contains(t, result, c)
			}
		})
	}
}

func TestBuildLabels(t *testing.T) {
	tests := []struct {
		name           string
		machine        *infrav1.EvrocMachine
		cluster        *infrav1.EvrocCluster
		expectRole     string
		expectHasLabel bool
	}{
		{
			name: "no cluster label",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1", Namespace: "default"},
			},
		},
		{
			name: "control plane machine with cluster",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cp-0",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name":  "test-cluster",
						"cluster.x-k8s.io/control-plane": "true",
					},
				},
			},
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
				Spec: infrav1.EvrocClusterSpec{
					Project: "test",
					Region:  "se-sto",
				},
			},
			expectRole:     "control-plane",
			expectHasLabel: true,
		},
		{
			name: "control plane machine with empty label value (CAPI v1beta2)",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cp-1",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name":  "test-cluster",
						"cluster.x-k8s.io/control-plane": "",
					},
				},
			},
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
				Spec: infrav1.EvrocClusterSpec{
					Project: "test",
					Region:  "se-sto",
				},
			},
			expectRole:     "control-plane",
			expectHasLabel: true,
		},
		{
			name: "worker machine with deployment name",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "worker-0",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name":    "test-cluster",
						"cluster.x-k8s.io/deployment-name": "md-0",
					},
				},
			},
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
				Spec: infrav1.EvrocClusterSpec{
					Project: "test",
					Region:  "se-sto",
				},
			},
			expectHasLabel: true,
		},
		{
			name: "worker machine without deployment name",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "worker-0",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
			},
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
				Spec: infrav1.EvrocClusterSpec{
					Project: "test",
					Region:  "se-sto",
				},
			},
			expectHasLabel: true,
		},
		{
			name: "cluster not found - uses machine labels only",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vm-1",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "missing-cluster",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := buildLabels(tt.machine, tt.cluster)
			assert.NotNil(t, labels)

			if tt.expectRole != "" {
				assert.Equal(t, tt.expectRole, labels["capi_role"], "capi_role should match expected role")
			}
		})
	}
}

func TestBuildDisks(t *testing.T) {
	reconciler := &EvrocMachineReconciler{}

	tests := []struct {
		name          string
		machine       *infrav1.EvrocMachine
		expectedCount int
		checkBoot     bool
	}{
		{
			name: "boot disk only",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "vm-1",
					Annotations: map[string]string{machineOwnershipIDAnnotation: "machine-owner-1"},
				},
				Spec: infrav1.EvrocMachineSpec{
					RootDiskSize: 50,
				},
			},
			expectedCount: 1,
			checkBoot:     true,
		},
		{
			name: "no root disk",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					RootDiskSize: 0,
				},
			},
			expectedCount: 0,
		},
		{
			name: "boot disk plus additional disks",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					RootDiskSize: 100,
					AdditionalDisks: []infrav1.AdditionalDiskSpec{
						{Name: "data", SizeGB: 200},
						{Name: "logs", SizeGB: 50},
					},
				},
			},
			expectedCount: 3,
			checkBoot:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			disks := reconciler.buildDisks(tt.machine)
			assert.NotNil(t, disks)
			assert.Len(t, *disks, tt.expectedCount)
			if tt.checkBoot && tt.expectedCount > 0 {
				assert.NotNil(t, (*disks)[0].BootFrom)
				assert.True(t, *(*disks)[0].BootFrom)
			}
		})
	}
}

func TestBuildPlacement(t *testing.T) {
	reconciler := &EvrocMachineReconciler{}

	tests := []struct {
		name        string
		machine     *infrav1.EvrocMachine
		expectZone  bool
		expectPGRef bool
	}{
		{
			name: "with zone only",
			machine: &infrav1.EvrocMachine{
				Status: infrav1.EvrocMachineStatus{
					AvailabilityZone: "a",
				},
			},
			expectZone:  true,
			expectPGRef: false,
		},
		{
			name: "no placement config",
			machine: &infrav1.EvrocMachine{
				Spec: infrav1.EvrocMachineSpec{},
			},
			expectZone:  false,
			expectPGRef: false,
		},
		{
			name: "with existing placement group",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					PlacementConfig: &infrav1.PlacementConfig{
						ExistingGroupID: func() *string { s := "my-pg"; return &s }(),
					},
				},
				Status: infrav1.EvrocMachineStatus{
					AvailabilityZone: "a",
				},
			},
			expectZone:  true,
			expectPGRef: true,
		},
		{
			name: "with existing placement group",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					PlacementConfig: &infrav1.PlacementConfig{
						ExistingGroupID: func() *string { s := "shared-pg"; return &s }(),
					},
				},
			},
			expectZone:  false,
			expectPGRef: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placement := reconciler.buildPlacement(tt.machine)
			if tt.expectZone {
				assert.NotNil(t, placement.Zone)
			} else {
				assert.Nil(t, placement.Zone)
			}
			if tt.expectPGRef {
				assert.NotNil(t, placement.PlacementGroupRef)
			} else {
				assert.Nil(t, placement.PlacementGroupRef)
			}
		})
	}
}

func TestCloudInitContentType(t *testing.T) {
	tests := []struct {
		content  string
		expected string
	}{
		{"#cloud-config\npackages:\n  - curl", "text/cloud-config"},
		{"#!/bin/bash\necho hello", "text/x-shellscript"},
		{"some plain text", "text/plain"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, cloudInitContentType(tt.content))
		})
	}
}

func TestGetOwnerMachineRef(t *testing.T) {
	tests := []struct {
		name       string
		refs       []metav1.OwnerReference
		expectName string
		expectAPI  string
	}{
		{
			name: "finds CAPI machine owner",
			refs: []metav1.OwnerReference{
				{
					APIVersion: "cluster.x-k8s.io/v1beta2",
					Kind:       "Machine",
					Name:       "my-machine",
				},
			},
			expectName: "my-machine",
			expectAPI:  "cluster.x-k8s.io/v1beta2",
		},
		{
			name: "ignores non-machine owner",
			refs: []metav1.OwnerReference{
				{
					APIVersion: "cluster.x-k8s.io/v1beta2",
					Kind:       "Cluster",
					Name:       "my-cluster",
				},
			},
			expectName: "",
			expectAPI:  "",
		},
		{
			name:       "empty refs",
			refs:       []metav1.OwnerReference{},
			expectName: "",
			expectAPI:  "",
		},
		{
			name: "ignores non-CAPI machine",
			refs: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Machine",
					Name:       "different-machine",
				},
			},
			expectName: "",
			expectAPI:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, apiVersion := getOwnerMachineRef(tt.refs)
			assert.Equal(t, tt.expectName, name)
			assert.Equal(t, tt.expectAPI, apiVersion)
		})
	}
}

func TestExtractMachineAddresses_NoNetworking(t *testing.T) {
	vm := &computetypes.VirtualMachine{
		Metadata: computetypes.RegionalMetadataResponse{Id: "my-vm"},
		Status:   computetypes.VirtualMachineStatus{},
	}
	addresses := extractMachineAddresses(vm)
	// Should at least have hostname
	assert.Len(t, addresses, 1)
	assert.Equal(t, corev1.NodeHostName, addresses[0].Type)
}

func TestClusterSecurityGroupNamesForRole(t *testing.T) {
	cluster := &infrav1.EvrocCluster{
		Status: infrav1.EvrocClusterStatus{
			Resources: &infrav1.ClusterResources{
				SecurityGroups: []infrav1.ManagedSecurityGroup{
					{ID: "base-rules", Role: "common"},
					{ID: "cp-api", Role: "controlPlane"},
					{ID: "worker-nodeports", Role: "worker"},
					{ID: "external-sg", Role: "common", Managed: false},
				},
			},
		},
	}

	t.Run("control plane gets common + controlPlane", func(t *testing.T) {
		names := clusterSecurityGroupNamesForRole(cluster, true)
		assert.Equal(t, []string{"base-rules", "cp-api", "external-sg"}, names)
	})

	t.Run("worker gets common + worker", func(t *testing.T) {
		names := clusterSecurityGroupNamesForRole(cluster, false)
		assert.Equal(t, []string{"base-rules", "worker-nodeports", "external-sg"}, names)
	})

	t.Run("nil cluster returns nil", func(t *testing.T) {
		names := clusterSecurityGroupNamesForRole(nil, true)
		assert.Nil(t, names)
	})

	t.Run("no resources returns nil", func(t *testing.T) {
		empty := &infrav1.EvrocCluster{}
		names := clusterSecurityGroupNamesForRole(empty, false)
		assert.Nil(t, names)
	})
}

func TestResolvePublicIPRef(t *testing.T) {
	tests := []struct {
		name        string
		machine     *infrav1.EvrocMachine
		cluster     *infrav1.EvrocCluster
		expectedRef string
	}{
		{
			name: "explicit existing ID",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1", Namespace: "default"},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						PublicIP: &infrav1.PublicIPConfig{
							ExistingID: func() *string { s := "my-ip"; return &s }(),
						},
					},
				},
			},
			expectedRef: "my-ip",
		},
		{
			name: "managed enabled",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1", Namespace: "default"},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						PublicIP: &infrav1.PublicIPConfig{Enabled: true},
					},
				},
			},
			expectedRef: "vm-1-public-ip",
		},
		{
			name: "no config",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1", Namespace: "default"},
			},
			expectedRef: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := resolvePublicIPRef(tt.machine, tt.cluster)
			assert.Equal(t, tt.expectedRef, ref)
		})
	}
}

func TestBuildNetworking(t *testing.T) {
	tests := []struct {
		name        string
		machine     *infrav1.EvrocMachine
		expectSGs   bool
		expectPubIP bool
	}{
		{
			name: "no networking config",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1", Namespace: "default"},
			},
			expectSGs:   false,
			expectPubIP: false,
		},
		{
			name: "with inline security groups",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1", Namespace: "default"},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						SecurityGroups: &infrav1.MachineSecurityGroupsConfig{
							InlineSecurityGroups: []infrav1.InlineSecurityGroup{
								{Name: "ssh"},
							},
						},
					},
				},
			},
			expectSGs:   true,
			expectPubIP: false,
		},
		{
			name: "with public IP enabled",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1", Namespace: "default"},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						PublicIP: &infrav1.PublicIPConfig{Enabled: true},
					},
				},
			},
			expectSGs:   false,
			expectPubIP: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			networking := buildNetworking(tt.machine, nil)
			assert.NotNil(t, networking)

			if tt.expectSGs {
				assert.NotNil(t, networking.SecurityGroupSettings)
			}
			if tt.expectPubIP {
				assert.NotNil(t, networking.PublicIPv4Address)
			}
		})
	}
}

func TestReconcileMachineSecurityGroups(t *testing.T) {
	tests := []struct {
		name        string
		machine     *infrav1.EvrocMachine
		setupMocks  func(*mocks.MockClient, *mocks.MockSecurityGroupService)
		expectError bool
	}{
		{
			name: "no networking config",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
			},
			setupMocks:  func(mc *mocks.MockClient, sg *mocks.MockSecurityGroupService) {},
			expectError: false,
		},
		{
			name: "creates new SG",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						SecurityGroups: &infrav1.MachineSecurityGroupsConfig{
							InlineSecurityGroups: []infrav1.InlineSecurityGroup{
								{Name: "ssh", Rules: []infrav1.SecurityGroupRule{}},
							},
						},
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, sg *mocks.MockSecurityGroupService) {
				mc.On("SecurityGroups").Return(sg)
				sg.On("Exists", mock.Anything, "vm-1-ssh").Return(false, nil)
				sg.On("Create", mock.Anything, "vm-1-ssh", mock.Anything, mock.Anything, mock.Anything).Return(&networkingtypes.SecurityGroup{}, nil)
			},
			expectError: false,
		},
		{
			name: "updates existing SG",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						SecurityGroups: &infrav1.MachineSecurityGroupsConfig{
							InlineSecurityGroups: []infrav1.InlineSecurityGroup{
								{Name: "ssh", Rules: []infrav1.SecurityGroupRule{}},
							},
						},
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, sg *mocks.MockSecurityGroupService) {
				mc.On("SecurityGroups").Return(sg)
				sg.On("Exists", mock.Anything, "vm-1-ssh").Return(true, nil)
				sg.On("Get", mock.Anything, "vm-1-ssh").Return(&networkingtypes.SecurityGroup{}, nil)
				sg.On("Update", mock.Anything, "vm-1-ssh", mock.Anything).Return(&networkingtypes.SecurityGroup{}, nil)
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mocks.MockClient)
			mockSG := new(mocks.MockSecurityGroupService)
			tt.setupMocks(mockClient, mockSG)

			reconciler := &EvrocMachineReconciler{}
			err := reconciler.reconcileMachineSecurityGroups(context.Background(), tt.machine, mockClient, clusterLabelInfo{})
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			mockClient.AssertExpectations(t)
			mockSG.AssertExpectations(t)
		})
	}
}

func TestReconcileLBBackend(t *testing.T) {
	tests := []struct {
		name             string
		machine          *infrav1.EvrocMachine
		cluster          *infrav1.EvrocCluster
		expectAddBackend bool
		expectError      bool
	}{
		{
			name: "registers CP machine as LB backend",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cp-0",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/control-plane": "",
						"cluster.x-k8s.io/cluster-name":  "test-cluster",
					},
				},
			},
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
					UID:  types.UID("abcd1234-0000-0000-0000-000000000000"),
				},
			},
			expectAddBackend: true,
		},
		{
			name: "skips worker machines",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "worker-0",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
			},
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
					UID:  types.UID("abcd1234-0000-0000-0000-000000000000"),
				},
			},
			expectAddBackend: false,
		},
		{
			name: "returns error when cluster not available",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cp-0",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/control-plane": "",
						"cluster.x-k8s.io/cluster-name":  "test-cluster",
					},
				},
			},
			cluster:          nil,
			expectAddBackend: false,
			expectError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mocks.MockClient)
			mockLBService := new(mocks.MockLoadBalancerService)

			expectedLBName := "test-cluster-abcd1234-cp-lb"
			if tt.expectAddBackend {
				mockClient.On("LoadBalancers").Return(mockLBService)
				mockLBService.On("AddBackend", mock.Anything, expectedLBName, cloud.Backend{Name: tt.machine.Name}).Return(nil)
			}

			reconciler := &EvrocMachineReconciler{}
			err := reconciler.reconcileLBBackend(context.Background(), tt.machine, mockClient, tt.cluster)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.expectAddBackend {
				mockLBService.AssertCalled(t, "AddBackend", mock.Anything, expectedLBName, cloud.Backend{Name: tt.machine.Name})
			}
		})
	}
}

func TestReconcileMachinePublicIP(t *testing.T) {
	tests := []struct {
		name          string
		machine       *infrav1.EvrocMachine
		setupMocks    func(*mocks.MockClient, *mocks.MockPublicIPService)
		expectPending bool
		expectManaged bool
		expectError   bool
	}{
		{
			name: "no config",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
			},
			setupMocks:    func(mc *mocks.MockClient, pip *mocks.MockPublicIPService) {},
			expectPending: false,
			expectError:   false,
		},
		{
			name: "existing ID",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						PublicIP: &infrav1.PublicIPConfig{
							ExistingID: func() *string { s := "my-ip"; return &s }(),
						},
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, pip *mocks.MockPublicIPService) {
				mc.On("PublicIPs").Return(pip)
				addr := "1.2.3.4"
				pip.On("Get", mock.Anything, "my-ip").Return(&networkingtypes.PublicIP{
					Metadata: networkingtypes.RegionalMetadataResponse{Id: "my-ip"},
					Status:   networkingtypes.PublicIPStatus{PublicIPv4Address: &addr},
				}, nil)
			},
			expectPending: false,
			expectManaged: false,
			expectError:   false,
		},
		{
			name: "enabled - creates new",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						PublicIP: &infrav1.PublicIPConfig{Enabled: true},
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, pip *mocks.MockPublicIPService) {
				mc.On("PublicIPs").Return(pip)
				pip.On("Exists", mock.Anything, "vm-1-public-ip").Return(false, nil)
				pip.On("Create", mock.Anything, "vm-1-public-ip", mock.Anything).Return(&networkingtypes.PublicIP{}, nil)
			},
			expectPending: true,
			expectManaged: true,
			expectError:   false,
		},
		{
			name: "enabled - exists with address",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					NetworkingConfig: &infrav1.MachineNetworkingConfig{
						PublicIP: &infrav1.PublicIPConfig{Enabled: true},
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, pip *mocks.MockPublicIPService) {
				mc.On("PublicIPs").Return(pip)
				addr := "5.6.7.8"
				pip.On("Exists", mock.Anything, "vm-1-public-ip").Return(true, nil)
				pip.On("Get", mock.Anything, "vm-1-public-ip").Return(&networkingtypes.PublicIP{
					Metadata: networkingtypes.RegionalMetadataResponse{Id: "vm-1-public-ip"},
					Status:   networkingtypes.PublicIPStatus{PublicIPv4Address: &addr},
				}, nil)
			},
			expectPending: false,
			expectManaged: true,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mocks.MockClient)
			mockPIP := new(mocks.MockPublicIPService)
			tt.setupMocks(mockClient, mockPIP)

			reconciler := &EvrocMachineReconciler{}
			pending, managedIP, err := reconciler.reconcilePublicIPCreation(context.Background(), tt.machine, mockClient, clusterLabelInfo{})
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectPending, pending)
			if tt.expectManaged {
				assert.NotNil(t, managedIP)
			}
			mockClient.AssertExpectations(t)
			mockPIP.AssertExpectations(t)
		})
	}
}

func TestReconcilePlacementGroup(t *testing.T) {
	tests := []struct {
		name          string
		machine       *infrav1.EvrocMachine
		setupMocks    func(*mocks.MockClient, *mocks.MockPlacementGroupService)
		expectPending bool
		expectResult  bool
		expectError   bool
	}{
		{
			name: "no placement config",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
			},
			setupMocks:    func(mc *mocks.MockClient, pg *mocks.MockPlacementGroupService) {},
			expectPending: false,
			expectError:   false,
		},
		{
			name: "existing group",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					PlacementConfig: &infrav1.PlacementConfig{
						ExistingGroupID: func() *string { s := "my-pg"; return &s }(),
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, pg *mocks.MockPlacementGroupService) {
				mc.On("PlacementGroups").Return(pg)
				pg.On("Get", mock.Anything, "my-pg").Return(&computetypes.PlacementGroup{}, nil)
			},
			expectPending: false,
			expectResult:  true,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mocks.MockClient)
			mockPG := new(mocks.MockPlacementGroupService)
			tt.setupMocks(mockClient, mockPG)

			reconciler := &EvrocMachineReconciler{}
			pending, pg, err := reconciler.reconcilePlacementGroup(context.Background(), tt.machine, mockClient, clusterLabelInfo{})
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectPending, pending)
			if tt.expectResult {
				assert.NotNil(t, pg)
			}
			mockClient.AssertExpectations(t)
			mockPG.AssertExpectations(t)
		})
	}
}

func TestReconcileAdditionalDisks(t *testing.T) {
	tests := []struct {
		name          string
		machine       *infrav1.EvrocMachine
		setupMocks    func(*mocks.MockClient, *mocks.MockDiskService)
		expectPending bool
		expectCount   int
		expectError   bool
	}{
		{
			name: "no additional disks",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
			},
			setupMocks:    func(mc *mocks.MockClient, ds *mocks.MockDiskService) {},
			expectPending: false,
			expectCount:   0,
			expectError:   false,
		},
		{
			name: "creates missing disk",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					AdditionalDisks: []infrav1.AdditionalDiskSpec{
						{Name: "data", SizeGB: 100},
					},
				},
				Status: infrav1.EvrocMachineStatus{
					AvailabilityZone: "a",
				},
			},
			setupMocks: func(mc *mocks.MockClient, ds *mocks.MockDiskService) {
				mc.On("Disks").Return(ds)
				ds.On("Get", mock.Anything, "vm-1-data").Return(nil, evroc.ErrNotFound)
				ds.On("Create", mock.Anything, "vm-1-data", 100, "", "a", mock.Anything).Return(&computetypes.Disk{}, nil)
			},
			expectPending: true,
			expectCount:   1,
			expectError:   false,
		},
		{
			name: "disk exists and ready",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
				Spec: infrav1.EvrocMachineSpec{
					AdditionalDisks: []infrav1.AdditionalDiskSpec{
						{Name: "data", SizeGB: 100},
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, ds *mocks.MockDiskService) {
				mc.On("Disks").Return(ds)
				ds.On("Get", mock.Anything, "vm-1-data").Return(&computetypes.Disk{
					Metadata: computetypes.RegionalMetadataResponse{Id: "vm-1-data"},
					Status: computetypes.DiskStatus{
						Conditions: &[]computetypes.DiskStatusConditionsItem{
							{Type: "Ready", Status: "True"},
						},
					},
				}, nil)
			},
			expectPending: false,
			expectCount:   1,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mocks.MockClient)
			mockDisk := new(mocks.MockDiskService)
			tt.setupMocks(mockClient, mockDisk)

			reconciler := &EvrocMachineReconciler{}
			pending, disks, err := reconciler.reconcileAdditionalDisks(context.Background(), tt.machine, mockClient, clusterLabelInfo{})
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectPending, pending)
			assert.Len(t, disks, tt.expectCount)
			mockClient.AssertExpectations(t)
			mockDisk.AssertExpectations(t)
		})
	}
}

func TestDeleteMachineFromCloud(t *testing.T) {
	tests := []struct {
		name          string
		machine       *infrav1.EvrocMachine
		setupMocks    func(*mocks.MockClient, *mocks.MockVirtualMachineService, *mocks.MockDiskService, *mocks.MockSecurityGroupService, *mocks.MockPublicIPService)
		expectError   bool
		expectPending bool
	}{
		{
			name: "cleans up boot disk after VM deletion",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "vm-1",
					Annotations: map[string]string{machineOwnershipIDAnnotation: "machine-owner-1"},
				},
				Spec: infrav1.EvrocMachineSpec{
					RootDiskSize: 50,
				},
				Status: infrav1.EvrocMachineStatus{
					Resources: &infrav1.MachineResources{
						BootDisk: &infrav1.ManagedDisk{
							ID:      "vm-1-boot-disk",
							SizeGB:  50,
							Managed: true,
						},
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, vm *mocks.MockVirtualMachineService, ds *mocks.MockDiskService, sg *mocks.MockSecurityGroupService, pip *mocks.MockPublicIPService) {
				mc.On("VirtualMachines").Return(vm)
				mc.On("SecurityGroups").Return(sg)
				mc.On("PublicIPs").Return(pip)
				mc.On("Disks").Return(ds)
				vm.On("Exists", mock.Anything, "vm-1").Return(false, nil)
				sg.On("ListByMachineOwner", mock.Anything, "machine-owner-1").Return([]string{}, nil)
				pip.On("ListByOwner", mock.Anything, "machine-owner-1").Return([]string{}, nil)
				ds.On("ListByOwner", mock.Anything, "machine-owner-1").Return([]string{"vm-1-boot-disk"}, nil)
				ds.On("Delete", mock.Anything, "vm-1-boot-disk").Return(nil)
			},
			expectError: false,
		},
		{
			name: "submits VM deletion without blocking",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "vm-1",
					Annotations: map[string]string{machineOwnershipIDAnnotation: "machine-owner-1"},
				},
				Spec: infrav1.EvrocMachineSpec{
					RootDiskSize: 0,
				},
			},
			setupMocks: func(mc *mocks.MockClient, vm *mocks.MockVirtualMachineService, ds *mocks.MockDiskService, sg *mocks.MockSecurityGroupService, pip *mocks.MockPublicIPService) {
				mc.On("VirtualMachines").Return(vm)
				vm.On("Exists", mock.Anything, "vm-1").Return(true, nil)
				vm.On("Delete", mock.Anything, "vm-1").Return(nil)
			},
			expectError:   true,
			expectPending: true,
		},
		{
			name: "deletes label-owned SG and public IP without status",
			machine: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "vm-1",
					Annotations: map[string]string{machineOwnershipIDAnnotation: "machine-owner-1"},
				},
				Spec: infrav1.EvrocMachineSpec{
					RootDiskSize: 0,
				},
			},
			setupMocks: func(mc *mocks.MockClient, vm *mocks.MockVirtualMachineService, ds *mocks.MockDiskService, sg *mocks.MockSecurityGroupService, pip *mocks.MockPublicIPService) {
				mc.On("VirtualMachines").Return(vm)
				mc.On("SecurityGroups").Return(sg)
				mc.On("PublicIPs").Return(pip)
				mc.On("Disks").Return(ds)
				vm.On("Exists", mock.Anything, "vm-1").Return(false, nil)
				sg.On("ListByMachineOwner", mock.Anything, "machine-owner-1").Return([]string{"vm-1-ssh"}, nil)
				pip.On("ListByOwner", mock.Anything, "machine-owner-1").Return([]string{"vm-1-public-ip"}, nil)
				ds.On("ListByOwner", mock.Anything, "machine-owner-1").Return([]string{}, nil)
				sg.On("Delete", mock.Anything, "vm-1-ssh").Return(nil)
				pip.On("Delete", mock.Anything, "vm-1-public-ip").Return(nil)
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mocks.MockClient)
			mockVM := new(mocks.MockVirtualMachineService)
			mockDisk := new(mocks.MockDiskService)
			mockSG := new(mocks.MockSecurityGroupService)
			mockPIP := new(mocks.MockPublicIPService)
			tt.setupMocks(mockClient, mockVM, mockDisk, mockSG, mockPIP)

			reconciler := &EvrocMachineReconciler{}
			err := reconciler.deleteMachineFromCloud(context.Background(), tt.machine, mockClient)
			if tt.expectPending {
				assert.ErrorIs(t, err, errMachineDeletionPending)
			} else if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			mockClient.AssertExpectations(t)
			mockVM.AssertExpectations(t)
			mockDisk.AssertExpectations(t)
			mockSG.AssertExpectations(t)
			mockPIP.AssertExpectations(t)
		})
	}
}

func TestHandleMachineError(t *testing.T) {
	scheme := testScheme()

	machine := &infrav1.EvrocMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "vm-1", Namespace: "default"},
		Spec: infrav1.EvrocMachineSpec{
			Project:        "test",
			Region:         "se-sto",
			ComputeProfile: "a1a.m",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(machine).
		WithStatusSubresource(machine).
		Build()

	reconciler := &EvrocMachineReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// Test quota error
	quotaErr := fmt.Errorf("not enough quota to perform request. Requested additional 4 vCPUs and 8GB memory. Only 2 vCPUs (out of 16 in quota) and 4GB memory (out of 32GB in quota) available: %w", evroc.ErrForbidden)
	result, err := reconciler.handleMachineError(context.Background(), machine, quotaErr)
	assert.Error(t, err)                                   // Terminal errors are returned
	assert.Equal(t, time.Duration(0), result.RequeueAfter) // No explicit requeue for terminal errors

	// Verify Ready condition carries the failure reason
	var updated infrav1.EvrocMachine
	_ = fakeClient.Get(context.Background(), client.ObjectKeyFromObject(machine), &updated)
	var readyCond *clusterv1.Condition
	for i := range updated.Status.Conditions {
		if updated.Status.Conditions[i].Type == "Ready" {
			readyCond = &updated.Status.Conditions[i]
			break
		}
	}
	assert.NotNil(t, readyCond)
	assert.Equal(t, corev1.ConditionFalse, readyCond.Status)
	assert.Equal(t, "QuotaExceeded", readyCond.Reason)
}

func TestPersistResourcesStatus(t *testing.T) {
	scheme := testScheme()

	t.Run("nil managed resources returns nil", func(t *testing.T) {
		machine := &infrav1.EvrocMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "test-m", Namespace: "default"},
			Spec:       infrav1.EvrocMachineSpec{Project: "p", Region: "se-sto", ComputeProfile: "a1a.m"},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(machine).WithStatusSubresource(machine).Build()
		reconciler := &EvrocMachineReconciler{Client: fakeClient, Scheme: scheme}

		err := reconciler.persistResourcesStatus(context.Background(), machine)
		assert.NoError(t, err)
	})

	t.Run("updates status when managed resources set", func(t *testing.T) {
		machine := &infrav1.EvrocMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "test-m", Namespace: "default"},
			Spec:       infrav1.EvrocMachineSpec{Project: "p", Region: "se-sto", ComputeProfile: "a1a.m"},
			Status: infrav1.EvrocMachineStatus{
				Resources: &infrav1.MachineResources{
					PublicIP: &infrav1.ManagedPublicIP{ID: "pip-1", Managed: true},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(machine).WithStatusSubresource(machine).Build()
		reconciler := &EvrocMachineReconciler{Client: fakeClient, Scheme: scheme}

		err := reconciler.persistResourcesStatus(context.Background(), machine)
		assert.NoError(t, err)

		var updated infrav1.EvrocMachine
		_ = fakeClient.Get(context.Background(), client.ObjectKeyFromObject(machine), &updated)
		assert.NotNil(t, updated.Status.Resources)
		assert.Equal(t, "pip-1", updated.Status.Resources.PublicIP.ID)
	})
}

func TestEmitMachineWarningEvent(t *testing.T) {
	scheme := testScheme()

	t.Run("nil recorder does not panic", func(t *testing.T) {
		reconciler := &EvrocMachineReconciler{Scheme: scheme}
		machine := &infrav1.EvrocMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "default"},
		}
		// Should not panic when Recorder is nil
		reconciler.emitMachineWarningEvent(machine, "TestReason", "msg %s", "arg")
	})
}

func TestLeastUsedZone(t *testing.T) {
	t.Run("empty failureDomains returns empty", func(t *testing.T) {
		assert.Equal(t, "", leastUsedZone(nil, nil))
		assert.Equal(t, "", leastUsedZone([]string{}, nil))
	})

	t.Run("no existing machines picks first zone", func(t *testing.T) {
		zone := leastUsedZone([]string{"a", "b", "c"}, map[string]int{})
		assert.Equal(t, "a", zone)
	})

	t.Run("3 workers across 3 zones gives a b c", func(t *testing.T) {
		fds := []string{"a", "b", "c"}

		// First machine: all empty → picks "a"
		z1 := leastUsedZone(fds, map[string]int{})
		assert.Equal(t, "a", z1)

		// Second machine: a=1 → picks "b"
		z2 := leastUsedZone(fds, map[string]int{"a": 1})
		assert.Equal(t, "b", z2)

		// Third machine: a=1, b=1 → picks "c"
		z3 := leastUsedZone(fds, map[string]int{"a": 1, "b": 1})
		assert.Equal(t, "c", z3)
	})

	t.Run("5 workers across 3 zones gives 2-2-1", func(t *testing.T) {
		fds := []string{"a", "b", "c"}
		counts := map[string]int{}
		zones := make([]string, 5)
		for i := 0; i < 5; i++ {
			zones[i] = leastUsedZone(fds, counts)
			counts[zones[i]]++
		}
		assert.Equal(t, []string{"a", "b", "c", "a", "b"}, zones)
	})

	t.Run("picks least used when uneven", func(t *testing.T) {
		zone := leastUsedZone([]string{"a", "b", "c"}, map[string]int{"a": 3, "b": 3, "c": 1})
		assert.Equal(t, "c", zone)
	})

	t.Run("single zone always returns it", func(t *testing.T) {
		zone := leastUsedZone([]string{"a"}, map[string]int{"a": 5})
		assert.Equal(t, "a", zone)
	})
}

// TestEvrocMachineReconciler_PausedSkipsDeletion verifies that a paused
// EvrocMachine being deleted does NOT trigger infrastructure cleanup.
// This is critical for clusterctl move: objects are paused then deleted
// from the source cluster, and we must not destroy real cloud resources.
func TestEvrocMachineReconciler_PausedSkipsDeletion(t *testing.T) {
	scheme := testScheme()

	vmName := "test-machine-pause-delete"
	now := metav1.Now()

	// No mock expectations — cloud APIs must NOT be called.
	mockClient := new(mocks.MockClient)

	evrocMachine := &infrav1.EvrocMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:              vmName,
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{machineFinalizer},
			Annotations: map[string]string{
				"cluster.x-k8s.io/paused": "",
			},
		},
		Spec: infrav1.EvrocMachineSpec{
			Project:        "test-project",
			Region:         "se-sto",
			ComputeProfile: "a1a.s",
			Image:          "ubuntu.22-04.1",

			RootDiskSize: 50,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(evrocMachine).
		WithStatusSubresource(evrocMachine).
		Build()

	reconciler := &EvrocMachineReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: vmName, Namespace: "default"},
	}

	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	// Verify the finalizer is still present (not removed during pause).
	updated := &infrav1.EvrocMachine{}
	err = fakeClient.Get(context.Background(), client.ObjectKeyFromObject(evrocMachine), updated)
	assert.NoError(t, err)
	assert.Contains(t, updated.Finalizers, machineFinalizer,
		"Finalizer must remain while paused — clusterctl move strips it separately")

	// No cloud API calls should have been made.
	mockClient.AssertNotCalled(t, "VirtualMachines")
	mockClient.AssertNotCalled(t, "Disks")
}

// TestEvrocMachineReconciler_PostMoveRecovery verifies that after clusterctl move
// (status empty), the early cloud check finds the existing VM and rebuilds
// status without re-running bootstrap/disk/SG creation.
func TestEvrocMachineReconciler_PostMoveRecovery(t *testing.T) {
	scheme := testScheme()

	vmID := uuid.New()
	vmName := "moved-machine"
	privateIP := "10.0.1.42"
	providerID := fmt.Sprintf("evroc://%s", vmID.String())

	// Mock: only VM Get should be called — no disk, no create.
	mockClient := new(mocks.MockClient)
	mockVMService := new(mocks.MockVirtualMachineService)

	mockClient.On("VirtualMachines").Return(mockVMService)

	// The early cloud check calls Get; reconcileExistingVM also reconciles SG/IP.
	readyVM := &computetypes.VirtualMachine{
		Metadata: computetypes.RegionalMetadataResponse{
			Uid: vmID,
			Id:  vmName,
		},
		Status: computetypes.VirtualMachineStatus{
			Conditions: &[]computetypes.VirtualMachineStatusConditionsItem{
				{Type: "Ready", Status: "True"},
			},
			Networking: &computetypes.VirtualMachineStatusNetworking{
				PrivateIPv4Address: &privateIP,
			},
			VirtualMachineStatus: func() *string { s := "Running"; return &s }(),
		},
	}
	mockVMService.On("Get", mock.Anything, vmName).Return(readyVM, nil)

	capiMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "capi-moved",
			Namespace: "default",
			UID:       "capi-uid-1",
		},
		Spec: clusterv1.MachineSpec{
			FailureDomain: "a",
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				Kind: "EvrocMachineTemplate",
				Name: "test-template",
			},
		},
	}

	capiCluster, evrocCluster := testClusterObjects("default")

	// EvrocMachine: providerID set (from before move) but status is empty (move clears status).
	evrocMachine := &infrav1.EvrocMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:       vmName,
			Namespace:  "default",
			Finalizers: []string{machineFinalizer},
			Annotations: map[string]string{
				machineOwnershipIDAnnotation: "pre-move-machine-owner",
			},
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: "test-cluster",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "Machine",
					Name:       capiMachine.Name,
					UID:        capiMachine.UID,
				},
			},
		},
		Spec: infrav1.EvrocMachineSpec{
			Project:        "test-project",
			Region:         "se-sto",
			ComputeProfile: "a1a.s",
			Image:          "ubuntu.22-04.1",
			ProviderID:     &providerID,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(capiCluster, evrocCluster, capiMachine, evrocMachine).
		WithStatusSubresource(evrocMachine).
		Build()

	reconciler := &EvrocMachineReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: vmName, Namespace: "default"},
	}

	// Reconcile: early cloud check finds the VM, status gets reconstructed.
	// Requeue for workload cluster patching (no kubeconfig in test).
	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, requeueMedium, result.RequeueAfter, "should requeue for node providerID patching")

	// Verify status was reconstructed from cloud.
	var updated infrav1.EvrocMachine
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: vmName, Namespace: "default"}, &updated)
	assert.NoError(t, err)
	assert.True(t, updated.Status.Ready, "machine should be ready after post-move recovery")
	assert.Equal(t, vmID.String(), updated.Status.MachineID)
	assert.NotEmpty(t, updated.Status.Addresses)

	// Verify no disk operations were called (early cloud check skips them).
	mockClient.AssertNotCalled(t, "Disks")
	// reconcileVMSecurityGroups and reconcilePublicIPAttachment guard on MachineID which
	// is empty at call time (set later in the same reconcile), so these must not fire.
	mockVMService.AssertNotCalled(t, "UpdateSecurityGroups")
	mockVMService.AssertNotCalled(t, "UpdatePublicIP")
}
