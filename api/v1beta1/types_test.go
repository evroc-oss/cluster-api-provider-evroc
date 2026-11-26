// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func TestConditionConstants(t *testing.T) {
	assert.Equal(t, clusterv1.ConditionType("Ready"), ClusterReadyCondition)
	assert.Equal(t, "ReconciliationFailed", ClusterReconciliationFailedReason)
	assert.Equal(t, "WaitingForControlPlaneEndpoint", WaitingForControlPlaneEndpointReason)
}

func TestSecurityGroupRule(t *testing.T) {
	port := int32(22)
	endPort := int32(22)

	rule := SecurityGroupRule{
		Name:                "ssh-rule",
		Direction:           "Ingress",
		Protocol:            "TCP",
		Port:                &port,
		EndPort:             &endPort,
		RemoteCIDR:          "0.0.0.0/0",
		RemoteSecurityGroup: "",
	}

	assert.Equal(t, "ssh-rule", rule.Name)
	assert.Equal(t, "Ingress", rule.Direction)
	assert.Equal(t, "TCP", rule.Protocol)
	assert.NotNil(t, rule.Port)
	assert.Equal(t, int32(22), *rule.Port)
	assert.NotNil(t, rule.EndPort)
	assert.Equal(t, int32(22), *rule.EndPort)
	assert.Equal(t, "0.0.0.0/0", rule.RemoteCIDR)
	assert.Empty(t, rule.RemoteSecurityGroup)
}

func TestShouldManageControlPlaneEndpoint(t *testing.T) {
	tests := []struct {
		name   string
		policy EndpointManagementPolicy
		want   bool
	}{
		{name: "default empty policy is managed", policy: "", want: true},
		{name: "explicit Managed", policy: EndpointManaged, want: true},
		{name: "External policy is not managed", policy: EndpointExternal, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &EvrocCluster{}
			c.Spec.ControlPlaneEndpointPolicy = tt.policy
			assert.Equal(t, tt.want, c.ShouldManageControlPlaneEndpoint())
		})
	}

	// nil receiver defaults to managed (defensive).
	var nilCluster *EvrocCluster
	assert.True(t, nilCluster.ShouldManageControlPlaneEndpoint())
}

func TestEndpointsConfig_GetAuthTokenURL(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name      string
		endpoints *EndpointsConfig
		expected  string
	}{
		{
			name:      "nil endpoints yield no override",
			endpoints: nil,
			expected:  "",
		},
		{
			name:      "unset issuer yields no override",
			endpoints: &EndpointsConfig{APIBaseURL: "https://api.example.com"},
			expected:  "",
		},
		{
			// Shaped like an issuerURL from the evroc CLI config.
			name:      "issuer is suffixed with the token path",
			endpoints: &EndpointsConfig{IssuerURL: "https://authn.example.com/realms/evroc-customer"},
			expected:  "https://authn.example.com/realms/evroc-customer/protocol/openid-connect/token",
		},
		{
			name:      "trailing slash does not double up",
			endpoints: &EndpointsConfig{IssuerURL: "https://authn.example.com/realms/evroc-customer/"},
			expected:  "https://authn.example.com/realms/evroc-customer/protocol/openid-connect/token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g.Expect(tt.endpoints.GetAuthTokenURL()).To(Equal(tt.expected))
		})
	}
}

func TestEvrocClusterConditions(t *testing.T) {
	cluster := &EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	conditions := clusterv1.Conditions{
		{
			Type:   clusterv1.ConditionType("Ready"),
			Status: "True",
		},
	}

	cluster.SetConditions(conditions)
	result := cluster.GetConditions()

	assert.NotNil(t, result)
	assert.Len(t, result, 1)
	assert.Equal(t, clusterv1.ConditionType("Ready"), result[0].Type)
}

func TestEvrocClusterDeepCopy(t *testing.T) {
	original := &EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
	}

	copy := original.DeepCopy()
	assert.NotNil(t, copy)
	assert.Equal(t, original.Name, copy.Name)
	assert.Equal(t, original.Spec.Project, copy.Spec.Project)

	copy.Spec.Project = "modified"
	assert.NotEqual(t, original.Spec.Project, copy.Spec.Project)
}

func TestEvrocMachineConditions(t *testing.T) {
	machine := &EvrocMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
		},
	}

	conditions := clusterv1.Conditions{
		{
			Type:   clusterv1.ConditionType("Ready"),
			Status: "True",
		},
	}

	machine.SetConditions(conditions)
	result := machine.GetConditions()

	assert.NotNil(t, result)
	assert.Len(t, result, 1)
	assert.Equal(t, clusterv1.ConditionType("Ready"), result[0].Type)
}

func TestEvrocMachineDeepCopy(t *testing.T) {
	original := &EvrocMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
		},
		Spec: EvrocMachineSpec{
			Project: "test-project",
			Region:  "se-sto",
		},
	}

	copy := original.DeepCopy()
	assert.NotNil(t, copy)
	assert.Equal(t, original.Name, copy.Name)
	assert.Equal(t, original.Spec.Project, copy.Spec.Project)

	copy.Spec.Project = "other-project"
	assert.NotEqual(t, original.Spec.Project, copy.Spec.Project)
}
