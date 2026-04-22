// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package controller

import (
	"context"
	"net/http"
	"testing"
	"time"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/evroc-oss/evroc-go-sdk/config"
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
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
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud/mocks"
)

// mockHTTPTransport is a mock HTTP transport that doesn't make real requests
type mockHTTPTransport struct{}

func (m *mockHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       http.NoBody,
	}, nil
}

// testSDKClientForCluster creates a test SDK client for cluster tests.
func testSDKClientForCluster() *evroc.Client {
	client, err := evroc.New(context.Background(), config.Config{
		Auth: config.AuthConfig{
			Token: "test-token",
		},
		API: config.APIConfig{
			BaseURL: "https://api.test.evroc.com",
		},
		Context: config.ContextConfig{
			Project: "test-project",
			Region:  "se-sto",
		},
	}, evroc.WithHTTPClient(&http.Client{Transport: &mockHTTPTransport{}}))
	if err != nil {
		panic(err)
	}
	return client
}

func TestEvrocClusterReconciler_CreateWithEndpoint(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	clusterName := "test-cluster"

	// Create CAPI Cluster
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "default",
		},
	}

	// Create test object with control plane endpoint
	evrocCluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "Cluster",
					Name:       clusterName,
					UID:        types.UID("test-uid"),
				},
			},
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: clusterName,
			},
		},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: "10.0.0.100",
				Port: 6443,
			},
			FailureDomains: []string{"a", "b", "c"},
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, evrocCluster).
		WithStatusSubresource(evrocCluster).
		Build()

	// Create mock cloud client
	mockClient := new(mocks.MockClient)
	mockClient.On("SDKClient").Return(testSDKClientForCluster())

	// Create reconciler
	reconciler := &EvrocClusterReconciler{
		Client:      fakeClient,
		Scheme:      scheme,
		CloudClient: mockClient,
	}

	// Reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      clusterName,
			Namespace: "default",
		},
	}

	// First reconcile adds finalizer
	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, result.RequeueAfter > 0)

	// Second reconcile sets up failure domains and marks ready
	result, err = reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	// Verify the cluster status
	var updatedCluster infrav1.EvrocCluster
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: "default"}, &updatedCluster)
	assert.NoError(t, err)
	assert.True(t, updatedCluster.Status.Ready)

	// Verify failure domains
	assert.NotNil(t, updatedCluster.Status.FailureDomains)
	assert.Len(t, updatedCluster.Status.FailureDomains, 3)

	// Check that all expected zones are present
	for _, zone := range []string{"a", "b", "c"} {
		found := false
		for _, fd := range updatedCluster.Status.FailureDomains {
			if fd.Name == zone {
				found = true
				assert.NotNil(t, fd.ControlPlane, "zone %s should have ControlPlane set", zone)
				assert.True(t, *fd.ControlPlane, "zone %s should be marked as ControlPlane", zone)
				break
			}
		}
		assert.True(t, found, "zone %s should be present", zone)
	}
}

func TestEvrocClusterReconciler_CreateWithoutEndpoint(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	clusterName := "test-cluster-no-endpoint"

	// Create CAPI Cluster
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "default",
		},
	}

	// Create test object without control plane endpoint
	evrocCluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "Cluster",
					Name:       clusterName,
					UID:        types.UID("test-uid"),
				},
			},
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: clusterName,
			},
		},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
			// No ControlPlaneEndpoint set
			FailureDomains: []string{"a", "b", "c"},
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, evrocCluster).
		WithStatusSubresource(evrocCluster).
		Build()

	// Create mock cloud client
	mockClient := new(mocks.MockClient)
	mockClient.On("SDKClient").Return(testSDKClientForCluster())

	// Create reconciler
	reconciler := &EvrocClusterReconciler{
		Client:      fakeClient,
		Scheme:      scheme,
		CloudClient: mockClient,
	}

	// Reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      clusterName,
			Namespace: "default",
		},
	}

	// First reconcile adds finalizer
	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, result.RequeueAfter > 0)

	// Second reconcile should wait for endpoint (10s requeue)
	result, err = reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, 10*time.Second, result.RequeueAfter)

	// Cluster is marked ready immediately (infrastructure needs no pre-provisioning),
	// even though the control plane endpoint has not been discovered yet.
	var updatedCluster infrav1.EvrocCluster
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: "default"}, &updatedCluster)
	assert.NoError(t, err)
	assert.True(t, updatedCluster.Status.Ready)

	// Verify failure domains are still set
	assert.NotNil(t, updatedCluster.Status.FailureDomains)
	assert.NotEmpty(t, updatedCluster.Status.FailureDomains)
	// Check that zone a is present
	foundZoneA := false
	for _, fd := range updatedCluster.Status.FailureDomains {
		if fd.Name == "a" {
			foundZoneA = true
			break
		}
	}
	assert.True(t, foundZoneA)
}

func TestEvrocClusterReconciler_Delete(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	clusterName := "test-cluster-delete"
	now := metav1.Now()

	// Create CAPI Cluster
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "default",
		},
	}

	// Create test object with deletion timestamp
	evrocCluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              clusterName,
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{clusterFinalizer},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "Cluster",
					Name:       clusterName,
					UID:        types.UID("test-uid"),
				},
			},
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: clusterName,
			},
		},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: "10.0.0.100",
				Port: 6443,
			},
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, evrocCluster).
		WithStatusSubresource(evrocCluster).
		Build()

	// Create mock cloud client
	mockClient := new(mocks.MockClient)
	mockClient.On("SDKClient").Return(testSDKClientForCluster())

	// Create reconciler
	reconciler := &EvrocClusterReconciler{
		Client:      fakeClient,
		Scheme:      scheme,
		CloudClient: mockClient,
	}

	// Reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      clusterName,
			Namespace: "default",
		},
	}

	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	// Note: In the fake client, once the finalizer is removed from an object with DeletionTimestamp,
	// the object is immediately deleted. So we don't verify the finalizer state, just that
	// reconciliation succeeded without error.
}

func TestEvrocClusterReconciler_AnyRegion(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	clusterName := "test-cluster-any-region"

	// Create CAPI Cluster
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "default",
		},
	}

	// Create test object with failure domains explicitly set
	evrocCluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "Cluster",
					Name:       clusterName,
					UID:        types.UID("test-uid"),
				},
			},
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: clusterName,
			},
		},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: "10.0.0.100",
				Port: 6443,
			},
			FailureDomains: []string{"a", "b", "c"}, // Explicitly set zones
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, evrocCluster).
		WithStatusSubresource(evrocCluster).
		Build()

	// Create mock cloud client
	mockClient := new(mocks.MockClient)
	mockClient.On("SDKClient").Return(testSDKClientForCluster())

	// Create reconciler
	reconciler := &EvrocClusterReconciler{
		Client:      fakeClient,
		Scheme:      scheme,
		CloudClient: mockClient,
	}

	// Reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      clusterName,
			Namespace: "default",
		},
	}

	// First reconcile adds finalizer
	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, result.RequeueAfter > 0)

	// Second reconcile should succeed with any region as long as FailureDomains are set
	result, err = reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	// Verify failure domains were created with simple zone format
	var updatedCluster infrav1.EvrocCluster
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: "default"}, &updatedCluster)
	assert.NoError(t, err)
	assert.Len(t, updatedCluster.Status.FailureDomains, 3)
	for _, zone := range []string{"a", "b", "c"} {
		found := false
		for _, fd := range updatedCluster.Status.FailureDomains {
			if fd.Name == zone {
				found = true
				break
			}
		}
		assert.True(t, found, "zone %s should be present", zone)
	}
}

func TestFirstUsableIP(t *testing.T) {
	tests := []struct {
		name     string
		addrs    []corev1.NodeAddress
		expected string
	}{
		{
			name:     "empty addresses",
			addrs:    []corev1.NodeAddress{},
			expected: "",
		},
		{
			name: "internal IP only",
			addrs: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
			},
			expected: "10.0.0.5",
		},
		{
			name: "external IP only",
			addrs: []corev1.NodeAddress{
				{Type: corev1.NodeExternalIP, Address: "1.2.3.4"},
			},
			expected: "1.2.3.4",
		},
		{
			name: "prefers internal over external",
			addrs: []corev1.NodeAddress{
				{Type: corev1.NodeExternalIP, Address: "1.2.3.4"},
				{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
			},
			expected: "10.0.0.5",
		},
		{
			name: "skips empty internal IP",
			addrs: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: ""},
				{Type: corev1.NodeExternalIP, Address: "1.2.3.4"},
			},
			expected: "1.2.3.4",
		},
		{
			name: "hostname type is ignored",
			addrs: []corev1.NodeAddress{
				{Type: corev1.NodeHostName, Address: "my-host"},
			},
			expected: "",
		},
		{
			name:     "nil addresses",
			addrs:    nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := firstUsableIP(tt.addrs)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCleanupResources(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	tests := []struct {
		name        string
		cluster     *infrav1.EvrocCluster
		setupMocks  func(*mocks.MockClient, *mocks.MockPublicIPService, *mocks.MockSecurityGroupService)
		expectError bool
	}{
		{
			name: "no managed resources",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			},
			setupMocks: func(mc *mocks.MockClient, pip *mocks.MockPublicIPService, sg *mocks.MockSecurityGroupService) {
			},
			expectError: false,
		},
		{
			name: "deletes managed public IP and security groups",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Status: infrav1.EvrocClusterStatus{
					Resources: &infrav1.ClusterResources{
						PublicIP: &infrav1.ManagedPublicIP{
							ID:      "test-cp-ip",
							Name:    "test-cp-ip",
							Managed: true,
						},
						SecurityGroups: []infrav1.ManagedSecurityGroup{
							{ID: "test-sg", Name: "test-sg", Managed: true},
							{ID: "external-sg", Name: "external-sg", Managed: false},
						},
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, pip *mocks.MockPublicIPService, sg *mocks.MockSecurityGroupService) {
				mc.On("PublicIPs").Return(pip)
				mc.On("SecurityGroups").Return(sg)
				pip.On("Delete", mock.Anything, "test-cp-ip").Return(nil)
				pip.On("Exists", mock.Anything, "test-cp-ip").Return(false, nil)
				sg.On("Delete", mock.Anything, "test-sg").Return(nil)
				sg.On("Exists", mock.Anything, "test-sg").Return(false, nil)
				// external-sg should NOT be deleted
			},
			expectError: false,
		},
		{
			name: "skips already-deleted resources",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Status: infrav1.EvrocClusterStatus{
					Resources: &infrav1.ClusterResources{
						PublicIP: &infrav1.ManagedPublicIP{
							ID:      "gone-ip",
							Name:    "gone-ip",
							Managed: true,
						},
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, pip *mocks.MockPublicIPService, sg *mocks.MockSecurityGroupService) {
				mc.On("PublicIPs").Return(pip)
				pip.On("Delete", mock.Anything, "gone-ip").Return(evroc.ErrNotFound)
				pip.On("Exists", mock.Anything, "gone-ip").Return(false, nil)
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mocks.MockClient)
			mockPIP := new(mocks.MockPublicIPService)
			mockSG := new(mocks.MockSecurityGroupService)
			tt.setupMocks(mockClient, mockPIP, mockSG)

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

			err := reconciler.cleanupResources(context.Background(), tt.cluster, mockClient)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			mockClient.AssertExpectations(t)
			mockPIP.AssertExpectations(t)
			mockSG.AssertExpectations(t)
		})
	}
}

func TestMachineToCluster(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	// The machineToCluster mapper resolves the CAPI Cluster's InfrastructureRef
	// to find the EvrocCluster name, so we need a CAPI Cluster in the fake client.
	capiCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: clusterv1.ClusterSpec{
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				Kind:     "EvrocCluster",
				Name:     "my-evroc-cluster",
				APIGroup: "infrastructure.cluster.x-k8s.io",
			},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(capiCluster).Build()
	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	tests := []struct {
		name         string
		obj          client.Object
		expectedLen  int
		expectedName string
	}{
		{
			name: "machine with cluster label",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Labels:    map[string]string{clusterv1.ClusterNameLabel: "my-cluster"},
				},
			},
			expectedLen:  1,
			expectedName: "my-evroc-cluster",
		},
		{
			name: "machine without cluster label",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
			},
			expectedLen: 0,
		},
		{
			name: "machine with empty cluster label",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Labels:    map[string]string{clusterv1.ClusterNameLabel: ""},
				},
			},
			expectedLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.machineToCluster(context.Background(), tt.obj)
			assert.Len(t, result, tt.expectedLen)
			if tt.expectedLen > 0 {
				assert.Equal(t, tt.expectedName, result[0].Name)
			}
		})
	}
}

func TestReconcileSecurityGroups_NoConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}
	mockClient := new(mocks.MockClient)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       infrav1.EvrocClusterSpec{Project: "test"},
	}

	err := reconciler.reconcileSecurityGroups(context.Background(), cluster, mockClient)
	assert.NoError(t, err)
}

func TestReconcileSecurityGroups_CreateInline(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	port := int32(6443)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default", UID: types.UID("abcd1234-5678-9012-3456-789012345678")},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test",
			Region:  "se-sto",
			SecurityGroups: &infrav1.ClusterSecurityGroupsConfig{
				ControlPlane: &infrav1.SecurityGroupsConfig{
					InlineSecurityGroups: []infrav1.InlineSecurityGroup{
						{
							Name: "api-server",
							Rules: []infrav1.SecurityGroupRule{
								{
									Name:       "k8s-api",
									Direction:  "Ingress",
									Protocol:   "TCP",
									Port:       &port,
									RemoteCIDR: "0.0.0.0/0",
								},
							},
						},
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	mockClient := new(mocks.MockClient)
	mockSG := new(mocks.MockSecurityGroupService)
	mockVM := new(mocks.MockVirtualMachineService)

	mockClient.On("SecurityGroups").Return(mockSG)
	mockClient.On("VirtualMachines").Return(mockVM)

	// SG doesn't exist yet — name now includes UID prefix
	mockSG.On("Exists", mock.Anything, "test-cluster-abcd1234-api-server").Return(false, nil)
	mockSG.On("Create", mock.Anything, "test-cluster-abcd1234-api-server", mock.Anything, mock.Anything).Return(&networkingtypes.SecurityGroup{}, nil)

	err := reconciler.reconcileSecurityGroups(context.Background(), cluster, mockClient)
	assert.NoError(t, err)

	// Verify role is set in status
	assert.Len(t, cluster.Status.Resources.SecurityGroups, 1)
	assert.Equal(t, "controlPlane", cluster.Status.Resources.SecurityGroups[0].Role)

	mockSG.AssertExpectations(t)
}

func TestReconcileAutoCreatedPublicIP_NewIP(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	mockClient := new(mocks.MockClient)
	mockPIP := new(mocks.MockPublicIPService)
	mockClient.On("PublicIPs").Return(mockPIP)

	// IP not found in cloud → create it → address not yet allocated
	mockPIP.On("Get", mock.Anything, "test-cluster-cp-ip").Return(nil, evroc.ErrNotFound)
	mockPIP.On("Create", mock.Anything, "test-cluster-cp-ip", mock.Anything).Return(&networkingtypes.PublicIP{
		Metadata: networkingtypes.RegionalMetadataResponse{Id: "test-cluster-cp-ip"},
	}, nil)

	result, err := reconciler.reconcileAutoCreatedPublicIP(context.Background(), cluster, "test-cluster-cp-ip", mockClient)
	assert.NoError(t, err)
	// Should requeue because address not yet allocated
	assert.True(t, result.RequeueAfter > 0)
	mockPIP.AssertExpectations(t)
}

func TestReconcileAutoCreatedPublicIP_AlreadyCreatedInStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	addr := "1.2.3.4"
	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
		Status: infrav1.EvrocClusterStatus{
			Resources: &infrav1.ClusterResources{
				PublicIP: &infrav1.ManagedPublicIP{
					ID:      "test-cluster-cp-ip",
					Name:    "test-cluster-cp-ip",
					Managed: true,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	mockClient := new(mocks.MockClient)
	mockPIP := new(mocks.MockPublicIPService)
	mockClient.On("PublicIPs").Return(mockPIP)

	// Already in status, verify it still exists → found with address
	mockPIP.On("Get", mock.Anything, "test-cluster-cp-ip").Return(&networkingtypes.PublicIP{
		Metadata: networkingtypes.RegionalMetadataResponse{Id: "test-cluster-cp-ip"},
		Status:   networkingtypes.PublicIPStatus{PublicIPv4Address: &addr},
	}, nil)

	result, err := reconciler.reconcileAutoCreatedPublicIP(context.Background(), cluster, "test-cluster-cp-ip", mockClient)
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	// Verify endpoint was set on in-memory object (deferred patch persists it)
	assert.Equal(t, "1.2.3.4", cluster.Spec.ControlPlaneEndpoint.Host)
	assert.Equal(t, int32(6443), cluster.Spec.ControlPlaneEndpoint.Port)

	mockPIP.AssertExpectations(t)
}

func TestReconcileAutoCreatedPublicIP_DeletedOutsideCAPI(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
		Status: infrav1.EvrocClusterStatus{
			Resources: &infrav1.ClusterResources{
				PublicIP: &infrav1.ManagedPublicIP{
					ID:      "test-cluster-cp-ip",
					Name:    "test-cluster-cp-ip",
					Managed: true,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	mockClient := new(mocks.MockClient)
	mockPIP := new(mocks.MockPublicIPService)
	mockClient.On("PublicIPs").Return(mockPIP)

	// Cloud resource deleted outside CAPI
	mockPIP.On("Get", mock.Anything, "test-cluster-cp-ip").Return(nil, evroc.ErrNotFound)

	result, err := reconciler.reconcileAutoCreatedPublicIP(context.Background(), cluster, "test-cluster-cp-ip", mockClient)
	assert.NoError(t, err)
	// Should requeue to recreate
	assert.True(t, result.RequeueAfter > 0)

	// PublicIP status should be cleared on in-memory object (deferred patch persists it)
	assert.Nil(t, cluster.Status.Resources.PublicIP)

	mockPIP.AssertExpectations(t)
}

func TestReconcileAutoCreatedPublicIP_ExistsInCloudWithAddress(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	addr := "5.6.7.8"
	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	mockClient := new(mocks.MockClient)
	mockPIP := new(mocks.MockPublicIPService)
	mockClient.On("PublicIPs").Return(mockPIP)

	// IP exists in cloud (from previous reconciliation before status was lost)
	mockPIP.On("Get", mock.Anything, "test-cluster-cp-ip").Return(&networkingtypes.PublicIP{
		Metadata: networkingtypes.RegionalMetadataResponse{Id: "test-cluster-cp-ip"},
		Status:   networkingtypes.PublicIPStatus{PublicIPv4Address: &addr},
	}, nil)

	result, err := reconciler.reconcileAutoCreatedPublicIP(context.Background(), cluster, "test-cluster-cp-ip", mockClient)
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	// Verify endpoint was set on in-memory object (deferred patch persists it)
	assert.Equal(t, "5.6.7.8", cluster.Spec.ControlPlaneEndpoint.Host)

	mockPIP.AssertExpectations(t)
}

func TestReconcileExistingPublicIP_Success(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	addr := "9.10.11.12"
	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	mockClient := new(mocks.MockClient)
	mockPIP := new(mocks.MockPublicIPService)
	mockClient.On("PublicIPs").Return(mockPIP)

	mockPIP.On("Get", mock.Anything, "my-existing-ip").Return(&networkingtypes.PublicIP{
		Metadata: networkingtypes.RegionalMetadataResponse{Id: "my-existing-ip"},
		Status:   networkingtypes.PublicIPStatus{PublicIPv4Address: &addr},
	}, nil)

	result, err := reconciler.reconcileExistingPublicIP(context.Background(), cluster, "my-existing-ip", mockClient)
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	// Verify endpoint was set on in-memory object (deferred patch persists it)
	assert.Equal(t, "9.10.11.12", cluster.Spec.ControlPlaneEndpoint.Host)

	// Verify managed=false in status
	assert.NotNil(t, cluster.Status.Resources)
	assert.NotNil(t, cluster.Status.Resources.PublicIP)
	assert.False(t, cluster.Status.Resources.PublicIP.Managed)

	mockPIP.AssertExpectations(t)
}

func TestReconcileExistingPublicIP_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	mockClient := new(mocks.MockClient)
	mockPIP := new(mocks.MockPublicIPService)
	mockClient.On("PublicIPs").Return(mockPIP)

	mockPIP.On("Get", mock.Anything, "nonexistent-ip").Return(nil, evroc.ErrNotFound)

	_, err := reconciler.reconcileExistingPublicIP(context.Background(), cluster, "nonexistent-ip", mockClient)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent-ip")

	mockPIP.AssertExpectations(t)
}

func TestReconcileExistingPublicIP_WaitingForAddress(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	mockClient := new(mocks.MockClient)
	mockPIP := new(mocks.MockPublicIPService)
	mockClient.On("PublicIPs").Return(mockPIP)

	// IP exists but no address yet
	mockPIP.On("Get", mock.Anything, "waiting-ip").Return(&networkingtypes.PublicIP{
		Metadata: networkingtypes.RegionalMetadataResponse{Id: "waiting-ip"},
		Status:   networkingtypes.PublicIPStatus{},
	}, nil)

	result, err := reconciler.reconcileExistingPublicIP(context.Background(), cluster, "waiting-ip", mockClient)
	assert.NoError(t, err)
	assert.Equal(t, 10*time.Second, result.RequeueAfter)

	mockPIP.AssertExpectations(t)
}

func TestUsePublicIP_SetsEndpointAndStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	addr := "13.14.15.16"
	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	cloudIP := &networkingtypes.PublicIP{
		Metadata: networkingtypes.RegionalMetadataResponse{Id: "test-cluster-cp-ip"},
		Status:   networkingtypes.PublicIPStatus{PublicIPv4Address: &addr},
	}

	result, err := reconciler.usePublicIP(context.Background(), cluster, cloudIP, true)
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	// Verify spec and status on in-memory object (deferred patch persists it)
	assert.Equal(t, "13.14.15.16", cluster.Spec.ControlPlaneEndpoint.Host)
	assert.Equal(t, int32(6443), cluster.Spec.ControlPlaneEndpoint.Port)
	assert.NotNil(t, cluster.Status.Resources)
	assert.NotNil(t, cluster.Status.Resources.PublicIP)
	assert.Equal(t, "test-cluster-cp-ip", cluster.Status.Resources.PublicIP.ID)
	assert.Equal(t, "13.14.15.16", cluster.Status.Resources.PublicIP.Address)
	assert.True(t, cluster.Status.Resources.PublicIP.Managed)
}

func TestUsePublicIP_AddressNotAllocated(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	// No address allocated
	cloudIP := &networkingtypes.PublicIP{
		Metadata: networkingtypes.RegionalMetadataResponse{Id: "test-ip"},
		Status:   networkingtypes.PublicIPStatus{},
	}

	result, err := reconciler.usePublicIP(context.Background(), cluster, cloudIP, false)
	assert.NoError(t, err)
	assert.True(t, result.RequeueAfter > 0)
}

func TestUsePublicIP_ExistingEndpointPreserved(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	addr := "17.18.19.20"
	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: "existing-ip",
				Port: 6443,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	cloudIP := &networkingtypes.PublicIP{
		Metadata: networkingtypes.RegionalMetadataResponse{Id: "test-ip"},
		Status:   networkingtypes.PublicIPStatus{PublicIPv4Address: &addr},
	}

	result, err := reconciler.usePublicIP(context.Background(), cluster, cloudIP, false)
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	// Verify original endpoint is preserved (not overwritten)
	assert.Equal(t, "existing-ip", cluster.Spec.ControlPlaneEndpoint.Host)
}

func TestReconcileSecurityGroups_UpdateExisting(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	port := int32(6443)
	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default", UID: types.UID("abcd1234-5678-9012-3456-789012345678")},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test",
			Region:  "se-sto",
			SecurityGroups: &infrav1.ClusterSecurityGroupsConfig{
				ControlPlane: &infrav1.SecurityGroupsConfig{
					InlineSecurityGroups: []infrav1.InlineSecurityGroup{
						{
							Name: "api-server",
							Rules: []infrav1.SecurityGroupRule{
								{
									Name:       "k8s-api",
									Direction:  "Ingress",
									Protocol:   "TCP",
									Port:       &port,
									RemoteCIDR: "0.0.0.0/0",
								},
							},
						},
					},
					ExistingNames: []string{"external-sg"},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	mockClient := new(mocks.MockClient)
	mockSG := new(mocks.MockSecurityGroupService)
	mockVM := new(mocks.MockVirtualMachineService)

	mockClient.On("SecurityGroups").Return(mockSG)
	mockClient.On("VirtualMachines").Return(mockVM)

	// Inline SG already exists → update — name now includes UID prefix
	mockSG.On("Exists", mock.Anything, "test-cluster-abcd1234-api-server").Return(true, nil)
	mockSG.On("Get", mock.Anything, "test-cluster-abcd1234-api-server").Return(&networkingtypes.SecurityGroup{}, nil)
	mockSG.On("Update", mock.Anything, "test-cluster-abcd1234-api-server", mock.Anything).Return(&networkingtypes.SecurityGroup{}, nil)

	// External SG exists
	mockSG.On("Exists", mock.Anything, "external-sg").Return(true, nil)

	err := reconciler.reconcileSecurityGroups(context.Background(), cluster, mockClient)
	assert.NoError(t, err)

	// Verify status has both inline (managed) and external (not managed) on in-memory object
	assert.NotNil(t, cluster.Status.Resources)
	assert.Len(t, cluster.Status.Resources.SecurityGroups, 2)
	// First is the inline (managed)
	assert.True(t, cluster.Status.Resources.SecurityGroups[0].Managed)
	assert.Equal(t, "controlPlane", cluster.Status.Resources.SecurityGroups[0].Role)
	// Second is external (not managed)
	assert.False(t, cluster.Status.Resources.SecurityGroups[1].Managed)
	assert.Equal(t, "controlPlane", cluster.Status.Resources.SecurityGroups[1].Role)

	mockSG.AssertExpectations(t)
}

func TestReconcileControlPlaneEndpointFromMachines(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	t.Run("discovers endpoint from CP machine", func(t *testing.T) {
		cluster := &infrav1.EvrocCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
			Spec: infrav1.EvrocClusterSpec{
				Project: "test",
				Region:  "se-sto",
			},
		}

		cpMachine := &infrav1.EvrocMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cp-0",
				Namespace: "default",
				Labels: map[string]string{
					clusterv1.ClusterNameLabel:         "test-cluster",
					clusterv1.MachineControlPlaneLabel: "true",
				},
			},
			Status: infrav1.EvrocMachineStatus{
				Addresses: []corev1.NodeAddress{
					{Type: corev1.NodeInternalIP, Address: "10.0.1.5"},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cluster, cpMachine).
			Build()

		reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

		result, err := reconciler.reconcileControlPlaneEndpointFromMachines(context.Background(), cluster)
		assert.NoError(t, err)
		assert.Equal(t, time.Duration(0), result.RequeueAfter)
		assert.Equal(t, "10.0.1.5", cluster.Spec.ControlPlaneEndpoint.Host)
		assert.Equal(t, int32(6443), cluster.Spec.ControlPlaneEndpoint.Port)
	})

	t.Run("no CP machines yet - requeues", func(t *testing.T) {
		cluster := &infrav1.EvrocCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
			Spec: infrav1.EvrocClusterSpec{
				Project: "test",
				Region:  "se-sto",
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cluster).
			Build()

		reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

		result, err := reconciler.reconcileControlPlaneEndpointFromMachines(context.Background(), cluster)
		assert.NoError(t, err)
		assert.Equal(t, 10*time.Second, result.RequeueAfter)
	})

	t.Run("CP machine without addresses - requeues", func(t *testing.T) {
		cluster := &infrav1.EvrocCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
			Spec: infrav1.EvrocClusterSpec{
				Project: "test",
				Region:  "se-sto",
			},
		}

		cpMachine := &infrav1.EvrocMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cp-0",
				Namespace: "default",
				Labels: map[string]string{
					clusterv1.ClusterNameLabel:         "test-cluster",
					clusterv1.MachineControlPlaneLabel: "true",
				},
			},
			Status: infrav1.EvrocMachineStatus{},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cluster, cpMachine).
			Build()

		reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

		result, err := reconciler.reconcileControlPlaneEndpointFromMachines(context.Background(), cluster)
		assert.NoError(t, err)
		assert.Equal(t, 10*time.Second, result.RequeueAfter)
	})

	t.Run("skips worker machines", func(t *testing.T) {
		cluster := &infrav1.EvrocCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
			Spec: infrav1.EvrocClusterSpec{
				Project: "test",
				Region:  "se-sto",
			},
		}

		workerMachine := &infrav1.EvrocMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "worker-0",
				Namespace: "default",
				Labels: map[string]string{
					clusterv1.ClusterNameLabel: "test-cluster",
				},
			},
			Status: infrav1.EvrocMachineStatus{
				Addresses: []corev1.NodeAddress{
					{Type: corev1.NodeInternalIP, Address: "10.0.1.10"},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cluster, workerMachine).
			Build()

		reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

		result, err := reconciler.reconcileControlPlaneEndpointFromMachines(context.Background(), cluster)
		assert.NoError(t, err)
		// Should requeue since no CP machine was found
		assert.Equal(t, 10*time.Second, result.RequeueAfter)
	})
}

func TestReconcileClusterSecurityGroupsOnVMs(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	t.Run("annotates VMs that inherit from cluster", func(t *testing.T) {
		capiCluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		}

		cluster := &infrav1.EvrocCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default", UID: types.UID("abcd1234-5678-9012-3456-789012345678"), ResourceVersion: "42"},
			Spec: infrav1.EvrocClusterSpec{
				Project: "test",
				Region:  "se-sto",
			},
			Status: infrav1.EvrocClusterStatus{
				Resources: &infrav1.ClusterResources{
					SecurityGroups: []infrav1.ManagedSecurityGroup{
						{Name: "test-cluster-abcd1234-api-server", Managed: true, Role: "controlPlane"},
						{Name: "external-sg", Managed: false, Role: "controlPlane"},
					},
				},
			},
		}

		// CP machine that inherits cluster SGs and has a VM
		inheritingMachine := &infrav1.EvrocMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cp-0",
				Namespace: "default",
				Labels: map[string]string{
					clusterv1.ClusterNameLabel:         "test-cluster",
					clusterv1.MachineControlPlaneLabel: "true",
				},
			},
			Spec: infrav1.EvrocMachineSpec{
				NetworkingConfig: &infrav1.MachineNetworkingConfig{
					SecurityGroups: &infrav1.MachineSecurityGroupsConfig{
						InheritFromCluster: true,
						InlineSecurityGroups: []infrav1.InlineSecurityGroup{
							{Name: "node-ssh"},
						},
					},
				},
			},
			Status: infrav1.EvrocMachineStatus{
				MachineID: "vm-123",
			},
		}

		// Machine that does NOT inherit (should be skipped)
		nonInheritingMachine := &infrav1.EvrocMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "worker-0",
				Namespace: "default",
				Labels:    map[string]string{clusterv1.ClusterNameLabel: "test-cluster"},
			},
			Status: infrav1.EvrocMachineStatus{
				MachineID: "vm-456",
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(capiCluster, cluster, inheritingMachine, nonInheritingMachine).
			Build()

		reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

		mockClient := new(mocks.MockClient)
		// No UpdateSecurityGroups call expected — function now annotates machines instead.

		err := reconciler.reconcileClusterSecurityGroupsOnVMs(context.Background(), cluster, mockClient)
		assert.NoError(t, err)

		// Verify the inheriting machine was annotated to trigger reconcile.
		updated := &infrav1.EvrocMachine{}
		assert.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: "cp-0", Namespace: "default"}, updated))
		assert.Equal(t, "42", updated.Annotations["infrastructure.cluster.x-k8s.io/sg-sync"])

		// Verify the non-inheriting machine was NOT annotated.
		skipped := &infrav1.EvrocMachine{}
		assert.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: "worker-0", Namespace: "default"}, skipped))
		assert.Empty(t, skipped.Annotations)
	})

	t.Run("skips machines without VM", func(t *testing.T) {
		capiCluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		}

		cluster := &infrav1.EvrocCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default", UID: types.UID("abcd1234-5678-9012-3456-789012345678")},
			Spec: infrav1.EvrocClusterSpec{
				Project: "test",
				Region:  "se-sto",
			},
			Status: infrav1.EvrocClusterStatus{
				Resources: &infrav1.ClusterResources{
					SecurityGroups: []infrav1.ManagedSecurityGroup{
						{Name: "test-cluster-abcd1234-api-server", Managed: true, Role: "controlPlane"},
					},
				},
			},
		}

		// Machine with no VM yet
		machine := &infrav1.EvrocMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cp-0",
				Namespace: "default",
				Labels:    map[string]string{clusterv1.ClusterNameLabel: "test-cluster"},
			},
			Spec: infrav1.EvrocMachineSpec{
				NetworkingConfig: &infrav1.MachineNetworkingConfig{
					SecurityGroups: &infrav1.MachineSecurityGroupsConfig{
						InheritFromCluster: true,
					},
				},
			},
			Status: infrav1.EvrocMachineStatus{
				MachineID: "", // VM not yet created
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(capiCluster, cluster, machine).
			Build()

		reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

		mockClient := new(mocks.MockClient)
		// No expectations - should not call UpdateSecurityGroups

		err := reconciler.reconcileClusterSecurityGroupsOnVMs(context.Background(), cluster, mockClient)
		assert.NoError(t, err)

		mockClient.AssertExpectations(t)
	})

	t.Run("no machines in cluster", func(t *testing.T) {
		cluster := &infrav1.EvrocCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "empty-cluster", Namespace: "default", UID: types.UID("abcd1234-5678-9012-3456-789012345678")},
			Spec: infrav1.EvrocClusterSpec{
				Project: "test",
				Region:  "se-sto",
			},
			Status: infrav1.EvrocClusterStatus{
				Resources: &infrav1.ClusterResources{
					SecurityGroups: []infrav1.ManagedSecurityGroup{
						{Name: "empty-cluster-abcd1234-api-server", Managed: true, Role: "controlPlane"},
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cluster).
			Build()

		reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}
		mockClient := new(mocks.MockClient)

		err := reconciler.reconcileClusterSecurityGroupsOnVMs(context.Background(), cluster, mockClient)
		assert.NoError(t, err)
	})
}

func TestReconcileSecurityGroups_ExternalNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test",
			Region:  "se-sto",
			SecurityGroups: &infrav1.ClusterSecurityGroupsConfig{
				ControlPlane: &infrav1.SecurityGroupsConfig{
					ExistingNames: []string{"missing-sg"},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	mockClient := new(mocks.MockClient)
	mockSG := new(mocks.MockSecurityGroupService)
	mockClient.On("SecurityGroups").Return(mockSG)

	mockSG.On("Exists", mock.Anything, "missing-sg").Return(false, nil)

	err := reconciler.reconcileSecurityGroups(context.Background(), cluster, mockClient)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing-sg")

	mockSG.AssertExpectations(t)
}

func TestResolvePublicIPConfig(t *testing.T) {
	existingName := "my-existing-ip"

	tests := []struct {
		name         string
		cluster      *infrav1.EvrocCluster
		expectedMode PublicIPMode
		expectedName string
	}{
		{
			name: "no control plane config returns None",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec:       infrav1.EvrocClusterSpec{},
			},
			expectedMode: PublicIPModeAutoDiscover,
		},
		{
			name: "nil public IP returns None",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: infrav1.EvrocClusterSpec{
					ControlPlaneConfig: &infrav1.ControlPlaneConfig{},
				},
			},
			expectedMode: PublicIPModeAutoDiscover,
		},
		{
			name: "enabled returns AutoCreate with derived name including UID",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", UID: types.UID("abcd1234-5678-9012-3456-789012345678")},
				Spec: infrav1.EvrocClusterSpec{
					ControlPlaneConfig: &infrav1.ControlPlaneConfig{
						PublicIP: &infrav1.PublicIPConfig{
							Enabled: true,
						},
					},
				},
			},
			expectedMode: PublicIPModeAutoCreate,
			expectedName: "my-cluster-abcd1234-cp-ip",
		},
		{
			name: "existingName returns UseExisting",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cluster"},
				Spec: infrav1.EvrocClusterSpec{
					ControlPlaneConfig: &infrav1.ControlPlaneConfig{
						PublicIP: &infrav1.PublicIPConfig{
							ExistingName: &existingName,
						},
					},
				},
			},
			expectedMode: PublicIPModeUseExisting,
			expectedName: "my-existing-ip",
		},
		{
			name: "disabled with no existingName returns None",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: infrav1.EvrocClusterSpec{
					ControlPlaneConfig: &infrav1.ControlPlaneConfig{
						PublicIP: &infrav1.PublicIPConfig{
							Enabled: false,
						},
					},
				},
			},
			expectedMode: PublicIPModeAutoDiscover,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := resolvePublicIPConfig(tt.cluster)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedMode, result.Mode)
			if tt.expectedName != "" {
				assert.Equal(t, tt.expectedName, result.ResourceName)
			}
		})
	}
}

// TestEvrocClusterReconciler_PausedSkipsDeletion verifies that a paused
// EvrocCluster being deleted does NOT trigger infrastructure cleanup.
// This is critical for clusterctl move: objects are paused then deleted
// from the source cluster, and we must not destroy real cloud resources.
func TestEvrocClusterReconciler_PausedSkipsDeletion(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	clusterName := "test-cluster-pause-delete"
	now := metav1.Now()

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "default",
		},
	}

	evrocCluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              clusterName,
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{clusterFinalizer},
			Annotations: map[string]string{
				"cluster.x-k8s.io/paused": "",
			},
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: clusterName,
			},
		},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, evrocCluster).
		WithStatusSubresource(evrocCluster).
		Build()

	// No mock cloud client expectations — cleanup must NOT be called.
	mockClient := new(mocks.MockClient)

	reconciler := &EvrocClusterReconciler{
		Client:      fakeClient,
		Scheme:      scheme,
		CloudClient: mockClient,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: clusterName, Namespace: "default"},
	}

	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	// Verify the finalizer is still present (not removed during pause).
	updated := &infrav1.EvrocCluster{}
	err = fakeClient.Get(context.Background(), client.ObjectKeyFromObject(evrocCluster), updated)
	assert.NoError(t, err)
	assert.Contains(t, updated.Finalizers, clusterFinalizer,
		"Finalizer must remain while paused — clusterctl move strips it separately")

	// No cloud API calls should have been made.
	mockClient.AssertNotCalled(t, "PublicIPs")
	mockClient.AssertNotCalled(t, "SecurityGroups")
}
