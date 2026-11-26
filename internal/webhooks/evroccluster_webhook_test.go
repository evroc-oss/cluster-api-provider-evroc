// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package webhooks

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
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
		cluster  *infrav1.EvrocCluster
		expected *infrav1.EvrocCluster
	}{
		{
			name: "sets default region and failure domains",
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project: "test-project",
				},
			},
			expected: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
				},
			},
			expected: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
					},
				},
			},
			expected: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant-ns",
				},
				Spec: infrav1.EvrocClusterSpec{
					Project: "test-project",
					CredentialsRef: &infrav1.SecretReference{
						Name: "my-creds",
					},
				},
			},
			expected: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant-ns",
				},
				Spec: infrav1.EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					CredentialsRef: &infrav1.SecretReference{
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
		cluster         *infrav1.EvrocCluster
		expectError     bool
		expectWarning   bool
		warningContains string
	}{
		{
			name: "valid cluster",
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
						"b",
						"c",
					},
					CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
				},
			},
			expectError: false,
		},
		{
			name: "invalid delete-csi-disks annotation",
			cluster: &infrav1.EvrocCluster{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{infrav1.DeleteCSIDisksAnnotation: "yes"}},
				Spec: infrav1.EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a"},
					CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
				},
			},
			expectError: true,
		},
		{
			name: "missing credentialsRef",
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a"},
				},
			},
			expectError: true,
		},
		{
			name: "empty credentialsRef name",
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a"},
					CredentialsRef: &infrav1.SecretReference{Name: ""},
				},
			},
			expectError: true,
		},
		{
			name: "missing project",
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{},
				},
			},
			expectError: true,
		},
		{
			name: "invalid region format",
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			cluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
					},
					CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
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

	oldCluster := &infrav1.EvrocCluster{
		Spec: infrav1.EvrocClusterSpec{
			Project: "test-project",
			Region:  "se-sto",
			FailureDomains: []string{
				"a",
				"b",
			},
			Network: infrav1.NetworkSpec{
				SubnetRefs: map[string]string{"a": "subnet-a"},
			},
			CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
		},
	}

	tests := []struct {
		name        string
		newCluster  *infrav1.EvrocCluster
		expectError bool
	}{
		{
			name: "no changes",
			newCluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
						"b",
					},
					Network: infrav1.NetworkSpec{
						SubnetRefs: map[string]string{"a": "subnet-a"},
					},
					CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
				},
			},
			expectError: false,
		},
		{
			name: "change project (immutable)",
			newCluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			newCluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
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
			newCluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project: "test-project",
					Region:  "se-sto",
					FailureDomains: []string{
						"a",
						"b",
						"c",
					},
					Network: infrav1.NetworkSpec{
						SubnetRefs: map[string]string{"a": "subnet-a"},
					},
					CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
				},
			},
			expectError: false,
		},
		{
			name: "change subnet for existing zone (immutable)",
			newCluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a", "b"},
					Network: infrav1.NetworkSpec{
						SubnetRefs: map[string]string{"a": "subnet-a-changed"},
					},
					CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
				},
			},
			expectError: true,
		},
		{
			name: "remove subnet for existing zone (immutable)",
			newCluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a", "b"},
					Network:        infrav1.NetworkSpec{SubnetRefs: map[string]string{}},
					CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
				},
			},
			expectError: true,
		},
		{
			name: "add subnet for new zone (mutable)",
			newCluster: &infrav1.EvrocCluster{
				Spec: infrav1.EvrocClusterSpec{
					Project:        "test-project",
					Region:         "se-sto",
					FailureDomains: []string{"a", "b"},
					Network: infrav1.NetworkSpec{
						SubnetRefs: map[string]string{"a": "subnet-a", "b": "subnet-b"},
					},
					CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
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

func TestEvrocClusterValidateUpdate_ControlPlaneEndpointPolicy(t *testing.T) {
	clusterWithPolicy := func(policy infrav1.EndpointManagementPolicy) *infrav1.EvrocCluster {
		return &infrav1.EvrocCluster{Spec: infrav1.EvrocClusterSpec{
			Project:                    "test-project",
			Region:                     "se-sto",
			FailureDomains:             []string{"a"},
			CredentialsRef:             &infrav1.SecretReference{Name: "test-creds"},
			ControlPlaneEndpointPolicy: policy,
		}}
	}

	tests := []struct {
		name        string
		oldPolicy   infrav1.EndpointManagementPolicy
		newPolicy   infrav1.EndpointManagementPolicy
		expectError bool
	}{
		{
			name:      "default and Managed are equivalent",
			newPolicy: infrav1.EndpointManaged,
		},
		{
			name:        "Managed to External is rejected",
			oldPolicy:   infrav1.EndpointManaged,
			newPolicy:   infrav1.EndpointExternal,
			expectError: true,
		},
		{
			name:        "External to Managed is rejected",
			oldPolicy:   infrav1.EndpointExternal,
			newPolicy:   infrav1.EndpointManaged,
			expectError: true,
		},
		{
			name:      "External remains External",
			oldPolicy: infrav1.EndpointExternal,
			newPolicy: infrav1.EndpointExternal,
		},
		{
			name:        "unsupported policy is rejected",
			oldPolicy:   infrav1.EndpointManaged,
			newPolicy:   "Unsupported",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := clusterValidator.ValidateUpdate(
				context.Background(), clusterWithPolicy(tt.oldPolicy), clusterWithPolicy(tt.newPolicy))
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEvrocClusterValidateCreate_Endpoints(t *testing.T) {
	g := NewWithT(t)

	clusterWith := func(e *infrav1.EndpointsConfig) *infrav1.EvrocCluster {
		return &infrav1.EvrocCluster{
			Spec: infrav1.EvrocClusterSpec{
				Project:        "test-project",
				Region:         "se-sto",
				FailureDomains: []string{"a"},
				CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
				Endpoints:      e,
			},
		}
	}

	tests := []struct {
		name        string
		endpoints   *infrav1.EndpointsConfig
		expectError bool
	}{
		{
			name:        "omitted endpoints are valid",
			endpoints:   nil,
			expectError: false,
		},
		{
			name:        "empty endpoints block is valid",
			endpoints:   &infrav1.EndpointsConfig{},
			expectError: false,
		},
		{
			name: "valid https endpoints",
			endpoints: &infrav1.EndpointsConfig{
				APIBaseURL: "https://api.private.example.com",
				IssuerURL:  "https://authn.private.example.com/realms/evroc-customer",
			},
			expectError: false,
		},
		{
			name:        "apiBaseURL only is valid",
			endpoints:   &infrav1.EndpointsConfig{APIBaseURL: "https://api.private.example.com"},
			expectError: false,
		},
		{
			name:        "http scheme rejected",
			endpoints:   &infrav1.EndpointsConfig{APIBaseURL: "http://api.private.example.com"},
			expectError: true,
		},
		{
			name:        "missing scheme rejected",
			endpoints:   &infrav1.EndpointsConfig{APIBaseURL: "api.private.example.com"},
			expectError: true,
		},
		{
			name:        "missing host rejected",
			endpoints:   &infrav1.EndpointsConfig{APIBaseURL: "https://"},
			expectError: true,
		},
		{
			name:        "malformed issuerURL rejected",
			endpoints:   &infrav1.EndpointsConfig{IssuerURL: "https://authn.example.com/%zz"},
			expectError: true,
		},
		{
			// The token endpoint is derived; pasting the full token URL would
			// double-suffix it.
			name:        "full token URL as issuerURL rejected",
			endpoints:   &infrav1.EndpointsConfig{IssuerURL: "https://authn.example.com/realms/r/protocol/openid-connect/token"},
			expectError: true,
		},
		{
			name:        "trailing slash on issuerURL is accepted",
			endpoints:   &infrav1.EndpointsConfig{IssuerURL: "https://authn.example.com/realms/r/"},
			expectError: false,
		},
		{
			name:        "clientID is not URL-validated",
			endpoints:   &infrav1.EndpointsConfig{ClientID: "some-client"},
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

func TestEvrocClusterValidateUpdate_EndpointsImmutable(t *testing.T) {
	g := NewWithT(t)

	clusterWith := func(e *infrav1.EndpointsConfig) *infrav1.EvrocCluster {
		return &infrav1.EvrocCluster{
			Spec: infrav1.EvrocClusterSpec{
				Project:        "test-project",
				Region:         "se-sto",
				FailureDomains: []string{"a"},
				CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
				Endpoints:      e,
			},
		}
	}

	set := &infrav1.EndpointsConfig{
		APIBaseURL: "https://api.private.example.com",
		IssuerURL:  "https://authn.private.example.com/realms/evroc-customer",
	}

	tests := []struct {
		name        string
		old, new    *infrav1.EndpointsConfig
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
			new: &infrav1.EndpointsConfig{
				APIBaseURL: "https://api.other.example.com",
				IssuerURL:  set.IssuerURL,
			},
			expectError: true,
		},
		{
			name: "changing issuerURL rejected",
			old:  set,
			new: &infrav1.EndpointsConfig{
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
			old:         &infrav1.EndpointsConfig{ClientID: "client-a"},
			new:         &infrav1.EndpointsConfig{ClientID: "client-b"},
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

func TestEvrocClusterValidateDelete(t *testing.T) {
	cluster := &infrav1.EvrocCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: infrav1.EvrocClusterSpec{
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
		rule        infrav1.SecurityGroupRule
		expectError bool
	}{
		{
			name: "valid rule with CIDR",
			rule: infrav1.SecurityGroupRule{
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
			rule: infrav1.SecurityGroupRule{
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
			rule: infrav1.SecurityGroupRule{
				Name:                "bad-rule",
				Direction:           "Ingress",
				RemoteCIDR:          "10.0.0.0/8",
				RemoteSecurityGroup: "other-sg",
			},
			expectError: true,
		},
		{
			name: "port out of range (negative)",
			rule: infrav1.SecurityGroupRule{
				Name:      "bad-port",
				Direction: "Ingress",
				Port:      int32Ptr(-1),
			},
			expectError: true,
		},
		{
			name: "port out of range (too high)",
			rule: infrav1.SecurityGroupRule{
				Name:      "bad-port",
				Direction: "Ingress",
				Port:      int32Ptr(70000),
			},
			expectError: true,
		},
		{
			name: "endPort out of range",
			rule: infrav1.SecurityGroupRule{
				Name:      "bad-endport",
				Direction: "Ingress",
				EndPort:   int32Ptr(70000),
			},
			expectError: true,
		},
		{
			name: "endPort less than port",
			rule: infrav1.SecurityGroupRule{
				Name:      "bad-range",
				Direction: "Ingress",
				Port:      int32Ptr(8080),
				EndPort:   int32Ptr(80),
			},
			expectError: true,
		},
		{
			name: "valid rule no port",
			rule: infrav1.SecurityGroupRule{
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
		cluster := &infrav1.EvrocCluster{
			Spec: infrav1.EvrocClusterSpec{
				Project:        "test-project",
				Region:         "se-sto",
				FailureDomains: []string{"a", "b", "c"},
				CredentialsRef: &infrav1.SecretReference{Name: "test-creds"},
				SecurityGroups: &infrav1.ClusterSecurityGroupsConfig{
					ControlPlane: &infrav1.SecurityGroupsConfig{
						InlineSecurityGroups: []infrav1.InlineSecurityGroup{
							{
								Name: "cp-sg",
								Rules: []infrav1.SecurityGroupRule{
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
		cluster := &infrav1.EvrocCluster{
			Spec: infrav1.EvrocClusterSpec{
				Project:        "test-project",
				Region:         "se-sto",
				FailureDomains: []string{"a", "b", "c"},
				SecurityGroups: &infrav1.ClusterSecurityGroupsConfig{
					Common: &infrav1.SecurityGroupsConfig{
						InlineSecurityGroups: []infrav1.InlineSecurityGroup{
							{Name: "dup-sg", Rules: []infrav1.SecurityGroupRule{}},
						},
					},
					ControlPlane: &infrav1.SecurityGroupsConfig{
						InlineSecurityGroups: []infrav1.InlineSecurityGroup{
							{Name: "dup-sg", Rules: []infrav1.SecurityGroupRule{}},
						},
					},
				},
			},
		}
		_, err := clusterValidator.ValidateCreate(context.Background(), cluster)
		g.Expect(err).To(HaveOccurred())
	})
}
