// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = infrav1.AddToScheme(s)
	_ = clusterv1.AddToScheme(s)
	return s
}

func TestIsPaused(t *testing.T) {
	scheme := testScheme()

	tests := []struct {
		name     string
		obj      client.Object
		cluster  *clusterv1.Cluster
		expected bool
	}{
		{
			name: "not paused - no annotation, no cluster",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vm-1",
					Namespace: "default",
				},
			},
			expected: false,
		},
		{
			name: "paused via annotation with value true",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vm-1",
					Namespace: "default",
					Annotations: map[string]string{
						clusterv1.PausedAnnotation: "true",
					},
				},
			},
			expected: true,
		},
		{
			name: "paused via annotation with empty value",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vm-1",
					Namespace: "default",
					Annotations: map[string]string{
						clusterv1.PausedAnnotation: "",
					},
				},
			},
			expected: true,
		},
		{
			name: "paused via owning Cluster.Spec.Paused",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vm-1",
					Namespace: "default",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
				},
			},
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: clusterv1.ClusterSpec{
					Paused: ptr.To(true),
				},
			},
			expected: true,
		},
		{
			name: "not paused - Cluster.Spec.Paused is false",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vm-1",
					Namespace: "default",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
				},
			},
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: clusterv1.ClusterSpec{
					Paused: ptr.To(false),
				},
			},
			expected: false,
		},
		{
			name: "not paused - cluster not found",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vm-1",
					Namespace: "default",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "missing-cluster",
					},
				},
			},
			expected: false,
		},
		{
			name: "paused - EvrocCluster with annotation",
			obj: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
					Annotations: map[string]string{
						clusterv1.PausedAnnotation: "",
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.cluster != nil {
				builder = builder.WithObjects(tt.cluster)
			}
			c := builder.Build()

			result := IsPaused(context.Background(), c, tt.obj)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasPausedAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		obj      client.Object
		expected bool
	}{
		{
			name: "no annotations",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-1"},
			},
			expected: false,
		},
		{
			name: "annotation present with value true",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "vm-1",
					Annotations: map[string]string{
						clusterv1.PausedAnnotation: "true",
					},
				},
			},
			expected: true,
		},
		{
			name: "annotation present with empty value",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "vm-1",
					Annotations: map[string]string{
						clusterv1.PausedAnnotation: "",
					},
				},
			},
			expected: true,
		},
		{
			name: "other annotations but not paused",
			obj: &infrav1.EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "vm-1",
					Annotations: map[string]string{
						"some-other-annotation": "value",
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, HasPausedAnnotation(tt.obj))
		})
	}
}
