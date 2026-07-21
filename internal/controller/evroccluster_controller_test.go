// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package controller

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/evroc-oss/evroc-go-sdk/config"
	"github.com/evroc-oss/evroc-go-sdk/metrics"
	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud/mocks"
)

// staticClientFactory returns a clusterClientFactory that always yields c,
// bypassing credentialsRef secret lookup in unit tests.
func staticClientFactory(c cloud.ClientInterface) clusterClientFactory {
	return func(context.Context, client.Reader, string, string, cloud.ClusterContext, *metrics.Manager) (cloud.ClientInterface, error) {
		return c, nil
	}
}

func TestClusterResourcePrefixPreservedAcrossClusterctlMove(t *testing.T) {
	sourceCluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "moved-cluster",
			UID:  types.UID("olduid00-0000-0000-0000-000000000000"),
		},
	}

	prefix, err := clusterResourcePrefix(sourceCluster)
	assert.NoError(t, err)
	assert.Equal(t, "moved-cluster-olduid00", prefix)
	assert.Equal(t, prefix, sourceCluster.Annotations[clusterResourcePrefixAnnotation])

	// clusterctl recreates the object with a new UID and without status, but
	// preserves metadata such as annotations.
	targetCluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        sourceCluster.Name,
			UID:         types.UID("newuid00-0000-0000-0000-000000000000"),
			Annotations: sourceCluster.Annotations,
		},
	}

	lbName, err := resolveLoadBalancerName(targetCluster)
	assert.NoError(t, err)
	assert.Equal(t, "moved-cluster-olduid00-cp-lb", lbName)
}

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

// A missing credentials secret must surface on the object. Without a condition
// the controller retries silently and, from the outside, a permanently broken
// cluster is indistinguishable from one that is still provisioning.
func TestEvrocClusterReconciler_MissingCredentialsSecretSetsCondition(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	clusterName := "test-cluster-no-secret"
	evrocCluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "default",
			UID:       types.UID("abcd1234-0000-0000-0000-000000000000"),
			Labels:    map[string]string{clusterv1.ClusterNameLabel: clusterName},
		},
		Spec: infrav1.EvrocClusterSpec{
			Project:        "test-project",
			Region:         "se-sto",
			FailureDomains: []string{"a"},
			CredentialsRef: &infrav1.SecretReference{Name: "does-not-exist"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(evrocCluster).
		WithStatusSubresource(evrocCluster).
		Build()

	// No clientFactory override: exercise the real credential lookup so the
	// missing secret actually fails.
	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: clusterName, Namespace: "default"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolving cloud credentials")

	var updated infrav1.EvrocCluster
	assert.NoError(t, fakeClient.Get(context.Background(),
		types.NamespacedName{Name: clusterName, Namespace: "default"}, &updated))

	var found bool
	for _, c := range updated.Status.Conditions {
		if c.Type == infrav1.ClusterReadyCondition {
			found = true
			assert.Equal(t, corev1.ConditionFalse, c.Status)
			assert.Equal(t, infrav1.CredentialsNotFoundReason, c.Reason)
			assert.Contains(t, c.Message, "does-not-exist")
		}
	}
	assert.True(t, found, "Ready condition must report why credentials could not be resolved")
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
			UID:       types.UID("abcd1234-0000-0000-0000-000000000000"),
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
		Status: infrav1.EvrocClusterStatus{
			Resources: &infrav1.ClusterResources{
				LoadBalancer: &infrav1.ManagedLoadBalancer{
					ID: "test-lb", Address: "10.0.0.100",
				},
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

	mockLBService := new(mocks.MockLoadBalancerService)
	mockClient.On("LoadBalancers").Return(mockLBService)
	mockLBService.On("Get", mock.Anything, mock.AnythingOfType("string")).Return(&cloud.LoadBalancer{
		Name:    "test-cluster-abcd1234-cp-lb",
		ID:      "test-lb-uid",
		Address: "10.0.0.100",
		Status:  cloud.LoadBalancerStatusActive,
	}, nil)

	// Create reconciler
	reconciler := &EvrocClusterReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
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
			UID:       types.UID("abcd1234-0000-0000-0000-000000000000"),
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

	// Create mock cloud client — LB Get returns not-found, Create returns a pending LB
	mockClient := new(mocks.MockClient)
	mockClient.On("SDKClient").Return(testSDKClientForCluster())
	mockLB := new(mocks.MockLoadBalancerService)
	mockClient.On("LoadBalancers").Return(mockLB)
	pendingLB := &cloud.LoadBalancer{
		Name:   "test-cluster-no-endpoint-abcd1234-cp-lb",
		Status: cloud.LoadBalancerStatusCreating,
	}
	mockLB.On("Get", mock.Anything, mock.Anything).Return(nil, evroc.ErrNotFound)
	mockLB.On("Create", mock.Anything, mock.Anything).Return(pendingLB, nil)

	// Create reconciler
	reconciler := &EvrocClusterReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
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

	// Second reconcile starts LB creation — requeues waiting for LB to become active
	result, err = reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, result.RequeueAfter > 0, "should requeue while LB is provisioning")

	// Verify failure domains are set even while LB is pending
	var updatedCluster infrav1.EvrocCluster
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: "default"}, &updatedCluster)
	assert.NoError(t, err)

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
		Status: infrav1.EvrocClusterStatus{
			Resources: &infrav1.ClusterResources{
				LoadBalancer: &infrav1.ManagedLoadBalancer{
					ID: "test-lb", Address: "10.0.0.100",
				},
			},
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, evrocCluster).
		WithStatusSubresource(evrocCluster).
		Build()

	// Create mock cloud client with LB cleanup expectations
	mockClient := new(mocks.MockClient)
	mockClient.On("SDKClient").Return(testSDKClientForCluster())
	mockLB := new(mocks.MockLoadBalancerService)
	mockClient.On("LoadBalancers").Return(mockLB)
	mockLB.On("Delete", mock.Anything, "test-lb").Return(nil)
	mockLB.On("Exists", mock.Anything, "test-lb").Return(false, nil)

	// Create reconciler
	reconciler := &EvrocClusterReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
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
			UID:       types.UID("abcd1234-0000-0000-0000-000000000000"),
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
		Status: infrav1.EvrocClusterStatus{
			Resources: &infrav1.ClusterResources{
				LoadBalancer: &infrav1.ManagedLoadBalancer{
					ID: "test-lb", Address: "10.0.0.100",
				},
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

	mockLBService := new(mocks.MockLoadBalancerService)
	mockClient.On("LoadBalancers").Return(mockLBService)
	mockLBService.On("Get", mock.Anything, mock.AnythingOfType("string")).Return(&cloud.LoadBalancer{
		Name:    "test-cluster-abcd1234-cp-lb",
		ID:      "test-lb-uid",
		Address: "10.0.0.100",
		Status:  cloud.LoadBalancerStatusActive,
	}, nil)

	// Create reconciler
	reconciler := &EvrocClusterReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
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

func TestReconcileAutoCreatedLoadBalancer(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func(*mocks.MockLoadBalancerService)
		expectRequeue  bool
		expectError    bool
		expectEndpoint string
	}{
		{
			name: "creates LB when not found in cloud",
			mockSetup: func(m *mocks.MockLoadBalancerService) {
				m.On("Get", mock.Anything, mock.AnythingOfType("string")).
					Return((*cloud.LoadBalancer)(nil), evroc.ErrNotFound).Once()
				m.On("Create", mock.Anything, mock.AnythingOfType("*cloud.LoadBalancerCreateRequest")).
					Return(&cloud.LoadBalancer{
						Name:   "test-lb",
						ID:     "lb-uid",
						Status: cloud.LoadBalancerStatusCreating,
					}, nil)
				m.On("Get", mock.Anything, mock.AnythingOfType("string")).
					Return(&cloud.LoadBalancer{
						Name:    "test-lb",
						ID:      "lb-uid",
						Address: "",
						Status:  cloud.LoadBalancerStatusCreating,
					}, nil)
			},
			expectRequeue: true,
		},
		{
			name: "requeues when LB exists but not yet active",
			mockSetup: func(m *mocks.MockLoadBalancerService) {
				m.On("Get", mock.Anything, mock.AnythingOfType("string")).
					Return(&cloud.LoadBalancer{
						Name:   "test-lb",
						ID:     "lb-uid",
						Status: cloud.LoadBalancerStatusCreating,
					}, nil)
			},
			expectRequeue: true,
		},
		{
			name: "sets endpoint when LB is active with address",
			mockSetup: func(m *mocks.MockLoadBalancerService) {
				m.On("Get", mock.Anything, mock.AnythingOfType("string")).
					Return(&cloud.LoadBalancer{
						Name:    "test-lb",
						ID:      "lb-uid",
						Address: "1.2.3.4",
						Status:  cloud.LoadBalancerStatusActive,
					}, nil)
			},
			expectEndpoint: "1.2.3.4",
		},
		{
			name: "returns error on cloud API failure",
			mockSetup: func(m *mocks.MockLoadBalancerService) {
				m.On("Get", mock.Anything, mock.AnythingOfType("string")).
					Return((*cloud.LoadBalancer)(nil), fmt.Errorf("cloud API error"))
			},
			expectError: true,
		},
		{
			name: "requeues when LB active but no address yet",
			mockSetup: func(m *mocks.MockLoadBalancerService) {
				m.On("Get", mock.Anything, mock.AnythingOfType("string")).
					Return(&cloud.LoadBalancer{
						Name:   "test-lb",
						ID:     "lb-uid",
						Status: cloud.LoadBalancerStatusActive,
					}, nil)
			},
			expectRequeue: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = infrav1.AddToScheme(scheme)
			_ = clusterv1.AddToScheme(scheme)

			cluster := &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
					UID:       "abcd1234-0000-0000-0000-000000000000",
				},
				Spec: infrav1.EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
				},
			}

			mockClient := new(mocks.MockClient)
			mockLB := new(mocks.MockLoadBalancerService)
			mockClient.On("LoadBalancers").Return(mockLB)
			tt.mockSetup(mockLB)

			reconciler := &EvrocClusterReconciler{
				Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
				Scheme: scheme,
			}

			result, err := reconciler.reconcileAutoCreatedLoadBalancer(
				context.Background(), cluster, "test-cluster-abcd1234-cp-lb", mockClient)

			if tt.expectError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			if tt.expectRequeue {
				assert.True(t, result.RequeueAfter > 0, "expected requeue")
			}

			if tt.expectEndpoint != "" {
				assert.Equal(t, tt.expectEndpoint, cluster.Spec.ControlPlaneEndpoint.Host)
				assert.Equal(t, int32(6443), cluster.Spec.ControlPlaneEndpoint.Port)
			}
		})
	}
}

func TestCleanupResources_DeletesLB(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "abcd1234-0000-0000-0000-000000000000",
		},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
		Status: infrav1.EvrocClusterStatus{
			Resources: &infrav1.ClusterResources{
				LoadBalancer: &infrav1.ManagedLoadBalancer{
					ID: "test-cluster-abcd1234-cp-lb",
				},
			},
		},
	}

	mockClient := new(mocks.MockClient)
	mockLB := new(mocks.MockLoadBalancerService)
	mockClient.On("LoadBalancers").Return(mockLB)
	mockClient.On("SDKClient").Return(testSDKClientForCluster())

	// Expect delete and exists check
	mockLB.On("Delete", mock.Anything, "test-cluster-abcd1234-cp-lb").Return(nil)
	mockLB.On("Exists", mock.Anything, "test-cluster-abcd1234-cp-lb").Return(false, nil)

	reconciler := &EvrocClusterReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}

	err := reconciler.cleanupResources(context.Background(), cluster, mockClient)
	assert.NoError(t, err)
	mockLB.AssertCalled(t, "Delete", mock.Anything, "test-cluster-abcd1234-cp-lb")
}

func TestCleanupResources_LBDeleteError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "abcd1234-0000-0000-0000-000000000000",
		},
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
		Status: infrav1.EvrocClusterStatus{
			Resources: &infrav1.ClusterResources{
				LoadBalancer: &infrav1.ManagedLoadBalancer{
					ID: "test-cluster-abcd1234-cp-lb",
				},
			},
		},
	}

	mockClient := new(mocks.MockClient)
	mockLB := new(mocks.MockLoadBalancerService)
	mockClient.On("LoadBalancers").Return(mockLB)
	mockClient.On("SDKClient").Return(testSDKClientForCluster())

	mockLB.On("Delete", mock.Anything, "test-cluster-abcd1234-cp-lb").
		Return(fmt.Errorf("cloud API error"))
	mockLB.On("Exists", mock.Anything, "test-cluster-abcd1234-cp-lb").Return(true, nil)

	reconciler := &EvrocClusterReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}

	err := reconciler.cleanupResources(context.Background(), cluster, mockClient)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cloud API error")
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
			name: "deletes managed security groups",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Status: infrav1.EvrocClusterStatus{
					Resources: &infrav1.ClusterResources{
						SecurityGroups: []infrav1.ManagedSecurityGroup{
							{ID: "test-sg", Managed: true},
							{ID: "external-sg", Managed: false},
						},
					},
				},
			},
			setupMocks: func(mc *mocks.MockClient, pip *mocks.MockPublicIPService, sg *mocks.MockSecurityGroupService) {
				mc.On("SecurityGroups").Return(sg)
				sg.On("Delete", mock.Anything, "test-sg").Return(nil)
				sg.On("Exists", mock.Anything, "test-sg").Return(false, nil)
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
					ExistingIDs: []string{"external-sg"},
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
						{ID: "test-cluster-abcd1234-api-server", Role: "controlPlane"},
						{ID: "external-sg", Managed: false, Role: "controlPlane"},
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
						{ID: "test-cluster-abcd1234-api-server", Role: "controlPlane"},
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
						{ID: "empty-cluster-abcd1234-api-server", Role: "controlPlane"},
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
					ExistingIDs: []string{"missing-sg"},
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
		Client:        fakeClient,
		Scheme:        scheme,
		clientFactory: staticClientFactory(mockClient),
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

	// Verify the Paused condition was set.
	var pausedCond *clusterv1.Condition
	for i := range updated.Status.Conditions {
		if updated.Status.Conditions[i].Type == infrav1.PausedCondition {
			pausedCond = &updated.Status.Conditions[i]
			break
		}
	}
	assert.NotNil(t, pausedCond, "Paused condition should be set when reconciliation is paused")
	if pausedCond != nil {
		assert.Equal(t, corev1.ConditionTrue, pausedCond.Status)
	}

	// No cloud API calls should have been made.
	mockClient.AssertNotCalled(t, "PublicIPs")
	mockClient.AssertNotCalled(t, "SecurityGroups")
}

func TestEnsureCredentialSecretCopy(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	clusterUID := types.UID("cluster-uid-1")
	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default", UID: clusterUID},
		Spec: infrav1.EvrocClusterSpec{
			Project: "p", Region: "r",
			CredentialsRef: &infrav1.SecretReference{Name: "user-creds"},
		},
	}
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-creds", Namespace: "default"},
		Data:       map[string][]byte{"config.yaml": []byte("auth: {}")},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, userSecret).
		Build()
	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	assert.NoError(t, reconciler.ensureCredentialSecretCopy(context.Background(), cluster))

	// The copy exists, is owned by the cluster, and carries the same data.
	copySecret := &corev1.Secret{}
	assert.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{
		Name: "c1-evroc-credentials", Namespace: "default",
	}, copySecret))
	assert.Equal(t, userSecret.Data, copySecret.Data)

	var owned bool
	for _, ref := range copySecret.OwnerReferences {
		if ref.UID == clusterUID {
			assert.Equal(t, "EvrocCluster", ref.Kind)
			assert.Equal(t, infrav1.GroupVersion.String(), ref.APIVersion)
			owned = true
		}
	}
	assert.True(t, owned, "the copy must be owned by the cluster so clusterctl move carries it")

	// The user's own secret must NOT be owned: an OwnerReference would make
	// Kubernetes delete the user's credentials when the cluster goes away.
	updatedUser := &corev1.Secret{}
	assert.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{
		Name: "user-creds", Namespace: "default",
	}, updatedUser))
	assert.Empty(t, updatedUser.OwnerReferences,
		"the user's secret must never be owned by the cluster")
}

func TestEnsureCredentialSecretCopy_RefreshesWhenSourceChanges(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default", UID: types.UID("u1")},
		Spec: infrav1.EvrocClusterSpec{
			Project: "p", Region: "r",
			CredentialsRef: &infrav1.SecretReference{Name: "user-creds"},
		},
	}
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-creds", Namespace: "default"},
		Data:       map[string][]byte{"config.yaml": []byte("old")},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(cluster, userSecret).Build()
	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme}

	assert.NoError(t, reconciler.ensureCredentialSecretCopy(context.Background(), cluster))

	// Rotate the user's credentials.
	userSecret.Data = map[string][]byte{"config.yaml": []byte("new")}
	assert.NoError(t, fakeClient.Update(context.Background(), userSecret))
	assert.NoError(t, reconciler.ensureCredentialSecretCopy(context.Background(), cluster))

	copySecret := &corev1.Secret{}
	assert.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{
		Name: "c1-evroc-credentials", Namespace: "default",
	}, copySecret))
	assert.Equal(t, []byte("new"), copySecret.Data["config.yaml"],
		"the copy must track rotations of the source secret")
}

func TestCredentialSecretCopyUsedWhenSourceIsMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "moved", Namespace: "default", UID: types.UID("new-uid")},
		Spec: infrav1.EvrocClusterSpec{
			Project: "p", Region: "se-sto",
			CredentialsRef: &infrav1.SecretReference{Name: "source-creds"},
		},
	}
	copyKey := credentialSecretCopyKey(cluster)
	copySecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: copyKey.Name, Namespace: copyKey.Namespace}}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(copySecret).Build()

	var requested []types.NamespacedName
	expectedClient := new(mocks.MockClient)
	factory := func(_ context.Context, _ client.Reader, name, namespace string, _ cloud.ClusterContext, _ *metrics.Manager) (cloud.ClientInterface, error) {
		key := types.NamespacedName{Name: name, Namespace: namespace}
		requested = append(requested, key)
		if key.Name == cluster.Spec.CredentialsRef.Name {
			return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name)
		}
		return expectedClient, nil
	}
	reconciler := &EvrocClusterReconciler{Client: fakeClient, Scheme: scheme, clientFactory: factory}

	assert.NoError(t, reconciler.ensureCredentialSecretCopy(context.Background(), cluster))
	actualClient, err := reconciler.resolveCloudClient(context.Background(), cluster)
	assert.NoError(t, err)
	assert.Same(t, expectedClient, actualClient)
	assert.Equal(t, []types.NamespacedName{
		{Name: "source-creds", Namespace: "default"},
		copyKey,
	}, requested)
}
