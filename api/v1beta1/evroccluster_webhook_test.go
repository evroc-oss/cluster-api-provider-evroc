// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func int32Ptr(i int32) *int32 { return &i }

var (
	clusterDefaulter = &EvrocClusterDefaulter{}
	clusterValidator = &EvrocClusterValidator{}
)

func TestEvrocClusterDefault(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name     string
		cluster  *EvrocCluster
		expected *EvrocCluster
	}{
		{
			name: "sets default region and failure domains",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
				},
			},
			expected: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
						"b",
						"c",
					},
				},
			},
		},
		{
			name: "preserves existing region",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
				},
			},
			expected: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
						"b",
						"c",
					},
				},
			},
		},
		{
			name: "preserves existing failure domains",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
					},
				},
			},
			expected: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
					},
				},
			},
		},
		{
			name: "preserves credentialsRef name",
			cluster: &EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant-ns",
				},
				Spec: EvrocClusterSpec{
					Project: "test-project",
					CredentialsRef: &SecretReference{
						Name: "my-creds",
					},
				},
			},
			expected: &EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant-ns",
				},
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					CredentialsRef: &SecretReference{
						Name: "my-creds",
					},
					FailureDomains: []string{"a", "b", "c"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := clusterDefaulter.Default(context.Background(), tt.cluster)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(tt.cluster.Spec).To(Equal(tt.expected.Spec))
		})
	}
}

func TestEvrocClusterValidateCreate(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name            string
		cluster         *EvrocCluster
		expectError     bool
		expectWarning   bool
		warningContains string
	}{
		{
			name: "valid cluster",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
						"b",
						"c",
					},
					CredentialsRef: &SecretReference{Name: "test-creds"},
				},
			},
			expectError: false,
		},
		{
			name: "missing credentialsRef",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a"},
				},
			},
			expectError: true,
		},
		{
			name: "empty credentialsRef name",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a"},
					CredentialsRef: &SecretReference{Name: ""},
				},
			},
			expectError: true,
		},
		{
			name: "missing project",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Region: "se-sto",
					FailureDomains: []string{
						"a",
					},
				},
			},
			expectError: true,
		},
		{
			name: "missing region",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					FailureDomains: []string{
						"a",
					},
				},
			},
			expectError: true,
		},
		{
			name: "missing failure domains",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{},
				},
			},
			expectError: true,
		},
		{
			name: "invalid region format",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "invalid",
					FailureDomains: []string{
						"invalid-a",
					},
				},
			},
			expectError: true,
		},
		{
			name: "invalid failure domain format",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"invalid",
					},
				},
			},
			expectError: true,
		},
		{
			name: "failure domain not in region",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"d",
					},
				},
			},
			expectError: true,
		},
		{
			name: "duplicate failure domains",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
						"a",
					},
				},
			},
			expectError: true,
		},
		{
			name: "single failure domain (valid but warning)",
			cluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
					},
					CredentialsRef: &SecretReference{Name: "test-creds"},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := clusterValidator.ValidateCreate(context.Background(), tt.cluster)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			if tt.expectWarning {
				g.Expect(warnings).ToNot(BeEmpty())
				g.Expect(warnings[0]).To(ContainSubstring(tt.warningContains))
			}
		})
	}
}

func TestEvrocClusterValidateUpdate(t *testing.T) {
	g := NewWithT(t)

	oldCluster := &EvrocCluster{
		Spec: EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
			FailureDomains: []string{
				"a",
				"b",
			},
			Network: NetworkSpec{
				SubnetRefs: map[string]string{"a": "subnet-a"},
			},
			CredentialsRef: &SecretReference{Name: "test-creds"},
		},
	}

	tests := []struct {
		name        string
		newCluster  *EvrocCluster
		expectError bool
	}{
		{
			name: "no changes",
			newCluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
						"b",
					},
					Network: NetworkSpec{
						SubnetRefs: map[string]string{"a": "subnet-a"},
					},
					CredentialsRef: &SecretReference{Name: "test-creds"},
				},
			},
			expectError: false,
		},
		{
			name: "change project (immutable)",
			newCluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "different-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
						"b",
					},
				},
			},
			expectError: true,
		},
		{
			name: "change region (immutable)",
			newCluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "eu-sto",
					FailureDomains: []string{
						"a",
						"b",
					},
				},
			},
			expectError: true,
		},
		{
			name: "add failure domain (mutable)",
			newCluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
						"b",
						"c",
					},
					Network: NetworkSpec{
						SubnetRefs: map[string]string{"a": "subnet-a"},
					},
					CredentialsRef: &SecretReference{Name: "test-creds"},
				},
			},
			expectError: false,
		},
		{
			name: "change subnet for existing zone (immutable)",
			newCluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a", "b"},
					Network: NetworkSpec{
						SubnetRefs: map[string]string{"a": "subnet-a-changed"},
					},
					CredentialsRef: &SecretReference{Name: "test-creds"},
				},
			},
			expectError: true,
		},
		{
			name: "remove subnet for existing zone (immutable)",
			newCluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a", "b"},
					Network:        NetworkSpec{SubnetRefs: map[string]string{}},
					CredentialsRef: &SecretReference{Name: "test-creds"},
				},
			},
			expectError: true,
		},
		{
			name: "add subnet for new zone (mutable)",
			newCluster: &EvrocCluster{
				Spec: EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a", "b"},
					Network: NetworkSpec{
						SubnetRefs: map[string]string{"a": "subnet-a", "b": "subnet-b"},
					},
					CredentialsRef: &SecretReference{Name: "test-creds"},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := clusterValidator.ValidateUpdate(context.Background(), oldCluster, tt.newCluster)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestEvrocClusterValidateCreate_Endpoints(t *testing.T) {
	g := NewWithT(t)

	clusterWith := func(e *EndpointsConfig) *EvrocCluster {
		return &EvrocCluster{
			Spec: EvrocClusterSpec{
				Project:        "test-project",
				Region:         "se-sto",
				FailureDomains: []string{"a"},
				CredentialsRef: &SecretReference{Name: "test-creds"},
				Endpoints:      e,
			},
		}
	}

	tests := []struct {
		name        string
		endpoints   *EndpointsConfig
		expectError bool
	}{
		{
			name:        "omitted endpoints are valid",
			endpoints:   nil,
			expectError: false,
		},
		{
			name:        "empty endpoints block is valid",
			endpoints:   &EndpointsConfig{},
			expectError: false,
		},
		{
			name: "valid https endpoints",
			endpoints: &EndpointsConfig{
				APIBaseURL: "https://api.private.example.com",
				IssuerURL:  "https://authn.private.example.com/realms/evroc-customer",
			},
			expectError: false,
		},
		{
			name:        "apiBaseURL only is valid",
			endpoints:   &EndpointsConfig{APIBaseURL: "https://api.private.example.com"},
			expectError: false,
		},
		{
			name:        "http scheme rejected",
			endpoints:   &EndpointsConfig{APIBaseURL: "http://api.private.example.com"},
			expectError: true,
		},
		{
			name:        "missing scheme rejected",
			endpoints:   &EndpointsConfig{APIBaseURL: "api.private.example.com"},
			expectError: true,
		},
		{
			name:        "missing host rejected",
			endpoints:   &EndpointsConfig{APIBaseURL: "https://"},
			expectError: true,
		},
		{
			name:        "malformed issuerURL rejected",
			endpoints:   &EndpointsConfig{IssuerURL: "https://authn.example.com/%zz"},
			expectError: true,
		},
		{
			// The token endpoint is derived; pasting the full token URL would
			// double-suffix it.
			name:        "full token URL as issuerURL rejected",
			endpoints:   &EndpointsConfig{IssuerURL: "https://authn.example.com/realms/r/protocol/openid-connect/token"},
			expectError: true,
		},
		{
			name:        "trailing slash on issuerURL is accepted",
			endpoints:   &EndpointsConfig{IssuerURL: "https://authn.example.com/realms/r/"},
			expectError: false,
		},
		{
			name:        "clientID is not URL-validated",
			endpoints:   &EndpointsConfig{ClientID: "some-client"},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := clusterValidator.ValidateCreate(context.Background(), clusterWith(tt.endpoints))
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
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

func TestEvrocClusterValidateUpdate_EndpointsImmutable(t *testing.T) {
	g := NewWithT(t)

	clusterWith := func(e *EndpointsConfig) *EvrocCluster {
		return &EvrocCluster{
			Spec: EvrocClusterSpec{
				Project:        "test-project",
				Region:         "se-sto",
				FailureDomains: []string{"a"},
				CredentialsRef: &SecretReference{Name: "test-creds"},
				Endpoints:      e,
			},
		}
	}

	set := &EndpointsConfig{
		APIBaseURL: "https://api.private.example.com",
		IssuerURL:  "https://authn.private.example.com/realms/evroc-customer",
	}

	tests := []struct {
		name        string
		old, new    *EndpointsConfig
		expectError bool
	}{
		{
			name:        "unchanged endpoints",
			old:         set,
			new:         set,
			expectError: false,
		},
		{
			name:        "unset stays unset",
			old:         nil,
			new:         nil,
			expectError: false,
		},
		{
			// Adopting a private cloud endpoint on a cluster that has none is
			// allowed; the "once set" rule only guards an existing value.
			name:        "setting endpoints when previously unset is allowed",
			old:         nil,
			new:         set,
			expectError: false,
		},
		{
			name: "changing apiBaseURL rejected",
			old:  set,
			new: &EndpointsConfig{
				APIBaseURL: "https://api.other.example.com",
				IssuerURL:  set.IssuerURL,
			},
			expectError: true,
		},
		{
			name: "changing issuerURL rejected",
			old:  set,
			new: &EndpointsConfig{
				APIBaseURL: set.APIBaseURL,
				IssuerURL:  "https://authn.other.example.com/realms/evroc-customer",
			},
			expectError: true,
		},
		{
			name:        "clearing endpoints rejected",
			old:         set,
			new:         nil,
			expectError: true,
		},
		{
			name:        "changing clientID rejected",
			old:         &EndpointsConfig{ClientID: "client-a"},
			new:         &EndpointsConfig{ClientID: "client-b"},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := clusterValidator.ValidateUpdate(
				context.Background(), clusterWith(tt.old), clusterWith(tt.new))
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
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

func TestEvrocClusterValidateDelete(t *testing.T) {
	cluster := &EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: EvrocClusterSpec{
			Project:        "test-project",
			Region:         "se-sto",
			FailureDomains: []string{"a"},
		},
	}

	warnings, err := clusterValidator.ValidateDelete(context.Background(), cluster)
	assert.NoError(t, err)
	assert.Nil(t, warnings)
}

func TestValidateSecurityGroupRule(t *testing.T) {
	tests := []struct {
		name        string
		rule        SecurityGroupRule
		expectError bool
	}{
		{
			name: "valid rule with CIDR",
			rule: SecurityGroupRule{
				Name:       "ssh",
				Direction:  "Ingress",
				Protocol:   "TCP",
				Port:       int32Ptr(22),
				RemoteCIDR: "0.0.0.0/0",
			},
			expectError: false,
		},
		{
			name: "valid rule with port range",
			rule: SecurityGroupRule{
				Name:       "nodeports",
				Direction:  "Ingress",
				Protocol:   "TCP",
				Port:       int32Ptr(30000),
				EndPort:    int32Ptr(32767),
				RemoteCIDR: "0.0.0.0/0",
			},
			expectError: false,
		},
		{
			name: "mutually exclusive CIDR and security group",
			rule: SecurityGroupRule{
				Name:                "bad-rule",
				Direction:           "Ingress",
				RemoteCIDR:          "10.0.0.0/8",
				RemoteSecurityGroup: "other-sg",
			},
			expectError: true,
		},
		{
			name: "port out of range (negative)",
			rule: SecurityGroupRule{
				Name:      "bad-port",
				Direction: "Ingress",
				Port:      int32Ptr(-1),
			},
			expectError: true,
		},
		{
			name: "port out of range (too high)",
			rule: SecurityGroupRule{
				Name:      "bad-port",
				Direction: "Ingress",
				Port:      int32Ptr(70000),
			},
			expectError: true,
		},
		{
			name: "endPort out of range",
			rule: SecurityGroupRule{
				Name:      "bad-endport",
				Direction: "Ingress",
				EndPort:   int32Ptr(70000),
			},
			expectError: true,
		},
		{
			name: "endPort less than port",
			rule: SecurityGroupRule{
				Name:      "bad-range",
				Direction: "Ingress",
				Port:      int32Ptr(8080),
				EndPort:   int32Ptr(80),
			},
			expectError: true,
		},
		{
			name: "valid rule no port",
			rule: SecurityGroupRule{
				Name:       "allow-all",
				Direction:  "Egress",
				Protocol:   "All",
				RemoteCIDR: "0.0.0.0/0",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := field.NewPath("spec", "rules")
			errs := validateSecurityGroupRule(tt.rule, path)
			if tt.expectError {
				assert.NotEmpty(t, errs)
			} else {
				assert.Empty(t, errs)
			}
		})
	}
}

func TestEvrocClusterValidateCreate_SecurityGroupRules(t *testing.T) {
	g := NewWithT(t)

	t.Run("valid inline security group rules", func(t *testing.T) {
		cluster := &EvrocCluster{
			Spec: EvrocClusterSpec{
				Project:        "test-project",
				Region:         "se-sto",
				FailureDomains: []string{"a", "b", "c"},
				CredentialsRef: &SecretReference{Name: "test-creds"},
				SecurityGroups: &ClusterSecurityGroupsConfig{
					ControlPlane: &SecurityGroupsConfig{
						InlineSecurityGroups: []InlineSecurityGroup{
							{
								Name: "cp-sg",
								Rules: []SecurityGroupRule{
									{
										Name:       "ssh",
										Direction:  "Ingress",
										Protocol:   "TCP",
										Port:       int32Ptr(22),
										RemoteCIDR: "0.0.0.0/0",
									},
								},
							},
						},
					},
				},
			},
		}
		_, err := clusterValidator.ValidateCreate(context.Background(), cluster)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("duplicate security group names across sections", func(t *testing.T) {
		cluster := &EvrocCluster{
			Spec: EvrocClusterSpec{
				Project:        "test-project",
				Region:         "se-sto",
				FailureDomains: []string{"a", "b", "c"},
				SecurityGroups: &ClusterSecurityGroupsConfig{
					Common: &SecurityGroupsConfig{
						InlineSecurityGroups: []InlineSecurityGroup{
							{Name: "dup-sg", Rules: []SecurityGroupRule{}},
						},
					},
					ControlPlane: &SecurityGroupsConfig{
						InlineSecurityGroups: []InlineSecurityGroup{
							{Name: "dup-sg", Rules: []SecurityGroupRule{}},
						},
					},
				},
			},
		}
		_, err := clusterValidator.ValidateCreate(context.Background(), cluster)
		g.Expect(err).To(HaveOccurred())
	})
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
