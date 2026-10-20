// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
