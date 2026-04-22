// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"testing"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMergeLabels(t *testing.T) {
	tests := []struct {
		name           string
		cluster        *infrav1.EvrocCluster
		machine        *infrav1.EvrocMachine
		role           string
		expectedLabels map[string]string
	}{
		{
			name: "cluster labels only",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
				Spec: infrav1.EvrocClusterSpec{
					AdditionalLabels: map[string]string{
						"environment": "production",
						"team":        "platform",
					},
				},
			},
			machine: &infrav1.EvrocMachine{},
			role:    "control-plane",
			expectedLabels: map[string]string{
				"environment":       "production",
				"team":              "platform",
				"capi_cluster-name": "test-cluster",
				"capi_role":         "control-plane",
				"capi_provider":     "evroc",
			},
		},
		{
			name:    "machine labels only",
			cluster: &infrav1.EvrocCluster{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"}},
			machine: &infrav1.EvrocMachine{
				Spec: infrav1.EvrocMachineSpec{
					AdditionalLabels: map[string]string{
						"workload-type": "cpu-intensive",
					},
				},
			},
			role: "worker",
			expectedLabels: map[string]string{
				"workload-type":     "cpu-intensive",
				"capi_cluster-name": "test-cluster",
				"capi_role":         "worker",
				"capi_provider":     "evroc",
			},
		},
		{
			name: "machine labels override cluster labels",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
				Spec: infrav1.EvrocClusterSpec{
					AdditionalLabels: map[string]string{
						"environment": "staging",
						"team":        "platform",
					},
				},
			},
			machine: &infrav1.EvrocMachine{
				Spec: infrav1.EvrocMachineSpec{
					AdditionalLabels: map[string]string{
						"environment": "production", // overrides cluster
						"backup":      "daily",
					},
				},
			},
			role: "control-plane",
			expectedLabels: map[string]string{
				"environment":       "production", // from machine, not cluster
				"team":              "platform",
				"backup":            "daily",
				"capi_cluster-name": "test-cluster",
				"capi_role":         "control-plane",
				"capi_provider":     "evroc",
			},
		},
		{
			name:    "no labels",
			cluster: &infrav1.EvrocCluster{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"}},
			machine: &infrav1.EvrocMachine{},
			role:    "",
			expectedLabels: map[string]string{
				"capi_cluster-name": "test-cluster",
				"capi_provider":     "evroc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeLabels(tt.cluster, tt.machine, tt.role)

			if len(result) != len(tt.expectedLabels) {
				t.Errorf("expected %d labels, got %d", len(tt.expectedLabels), len(result))
			}

			for k, v := range tt.expectedLabels {
				if result[k] != v {
					t.Errorf("label %s: expected %s, got %s", k, v, result[k])
				}
			}
		})
	}
}

func TestResourceLabels(t *testing.T) {
	tests := []struct {
		name             string
		clusterName      string
		clusterUID       string
		additionalLabels []map[string]string
		expectedLabels   map[string]string
	}{
		{
			name:        "ownership labels only",
			clusterName: "my-cluster",
			clusterUID:  "abc-123",
			expectedLabels: map[string]string{
				"capi_cluster-name": "my-cluster",
				"capi_cluster-uid":  "abc-123",
				"capi_managed-by":   "cluster-api-provider-evroc",
			},
		},
		{
			name:        "cluster-level additional labels",
			clusterName: "my-cluster",
			clusterUID:  "abc-123",
			additionalLabels: []map[string]string{
				{"department": "analytics", "cost-center": "42"},
			},
			expectedLabels: map[string]string{
				"department":        "analytics",
				"cost-center":       "42",
				"capi_cluster-name": "my-cluster",
				"capi_cluster-uid":  "abc-123",
				"capi_managed-by":   "cluster-api-provider-evroc",
			},
		},
		{
			name:        "machine labels override cluster labels",
			clusterName: "my-cluster",
			clusterUID:  "abc-123",
			additionalLabels: []map[string]string{
				{"department": "analytics", "env": "staging"}, // cluster
				{"department": "ml-team", "workload": "gpu"},  // machine overrides
			},
			expectedLabels: map[string]string{
				"department":        "ml-team", // machine wins
				"env":               "staging",
				"workload":          "gpu",
				"capi_cluster-name": "my-cluster",
				"capi_cluster-uid":  "abc-123",
				"capi_managed-by":   "cluster-api-provider-evroc",
			},
		},
		{
			name:        "ownership labels cannot be overridden",
			clusterName: "real-cluster",
			clusterUID:  "real-uid",
			additionalLabels: []map[string]string{
				{
					"capi_cluster-name": "hacked",
					"capi_cluster-uid":  "fake-uid",
					"capi_managed-by":   "terraform",
					"department":        "analytics",
				},
			},
			expectedLabels: map[string]string{
				"department":        "analytics",
				"capi_cluster-name": "real-cluster",               // ownership wins
				"capi_cluster-uid":  "real-uid",                   // ownership wins
				"capi_managed-by":   "cluster-api-provider-evroc", // ownership wins
			},
		},
		{
			name:        "nil maps in variadic args are safe",
			clusterName: "my-cluster",
			clusterUID:  "abc-123",
			additionalLabels: []map[string]string{
				nil,
				{"env": "prod"},
				nil,
			},
			expectedLabels: map[string]string{
				"env":               "prod",
				"capi_cluster-name": "my-cluster",
				"capi_cluster-uid":  "abc-123",
				"capi_managed-by":   "cluster-api-provider-evroc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResourceLabels(tt.clusterName, tt.clusterUID, tt.additionalLabels...)

			if len(result) != len(tt.expectedLabels) {
				t.Errorf("expected %d labels, got %d: %v", len(tt.expectedLabels), len(result), result)
			}

			for k, v := range tt.expectedLabels {
				if result[k] != v {
					t.Errorf("label %s: expected %q, got %q", k, v, result[k])
				}
			}
		})
	}
}

func TestValidateLabelKey(t *testing.T) {
	tests := []struct {
		key   string
		valid bool
	}{
		{"environment", true},
		{"team", true},
		{"cost-center", true},
		{"app.kubernetes.io/name", false},  // "/" not allowed by evroc
		{"app.kubernetes.io_name", true},   // "_" separator is ok
		{"", false},                        // empty
		{string(make([]byte, 254)), false}, // too long
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := ValidateLabelKey(tt.key)
			if result != tt.valid {
				t.Errorf("ValidateLabelKey(%q) = %v, want %v", tt.key, result, tt.valid)
			}
		})
	}
}

func TestValidateLabelValue(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"production", true},
		{"", true}, // empty is valid
		{"staging", true},
		{string(make([]byte, 64)), false}, // too long
		{string(make([]byte, 63)), true},  // max length
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			result := ValidateLabelValue(tt.value)
			if result != tt.valid {
				t.Errorf("ValidateLabelValue(%q) = %v, want %v", tt.value, result, tt.valid)
			}
		})
	}
}
