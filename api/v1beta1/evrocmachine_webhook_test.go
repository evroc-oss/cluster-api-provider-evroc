// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	"context"
	"testing"

	"github.com/evroc-oss/evroc-go-sdk/compute"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var (
	machineDefaulter = &EvrocMachineDefaulter{}
	machineValidator = &EvrocMachineValidator{}
)

func TestEvrocMachineDefault(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name     string
		machine  *EvrocMachine
		expected *EvrocMachine
	}{
		{
			name: "sets default region",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					ComputeProfile: "a1a.m",
				},
			},
			expected: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					Image:          string(compute.DiskImageUbuntu2404),
					NetworkingConfig: &MachineNetworkingConfig{
						SecurityGroups: &MachineSecurityGroupsConfig{InheritFromCluster: true},
					},
				},
			},
		},
		{
			name: "preserves existing region",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
				},
			},
			expected: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					Image:          string(compute.DiskImageUbuntu2404),
					NetworkingConfig: &MachineNetworkingConfig{
						SecurityGroups: &MachineSecurityGroupsConfig{InheritFromCluster: true},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := machineDefaulter.Default(context.Background(), tt.machine)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(tt.machine.Spec).To(Equal(tt.expected.Spec))
		})
	}
}

func TestEvrocMachineValidateCreate(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name            string
		machine         *EvrocMachine
		expectError     bool
		expectWarning   bool
		warningContains string
	}{
		{
			name: "valid machine",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					Image:          string(compute.DiskImageUbuntu2404),
					RootDiskSize:   100,
				},
			},
			expectError:   false,
			expectWarning: false,
		},
		{
			name: "control plane machine without inheritFromCluster",
			machine: &EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"cluster.x-k8s.io/control-plane": "",
					},
				},
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					Image:          string(compute.DiskImageUbuntu2404),
					RootDiskSize:   100,
				},
			},
			expectError:     false,
			expectWarning:   true,
			warningContains: "inheritFromCluster",
		},
		{
			name: "control plane machine with inheritFromCluster",
			machine: &EvrocMachine{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"cluster.x-k8s.io/control-plane": "",
					},
				},
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					Image:          string(compute.DiskImageUbuntu2404),
					RootDiskSize:   100,
					NetworkingConfig: &MachineNetworkingConfig{
						SecurityGroups: &MachineSecurityGroupsConfig{
							InheritFromCluster: true,
						},
					},
				},
			},
			expectError:   false,
			expectWarning: false,
		},
		{
			name: "missing project",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					RootDiskSize:   100,
				},
			},
			expectError: true,
		},
		{
			name: "missing region",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					ComputeProfile: "a1a.m",
					RootDiskSize:   100,
				},
			},
			expectError: true,
		},
		{
			name: "missing compute profile",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:      "test-project",
					Region:       "se-sto",
					RootDiskSize: 100,
				},
			},
			expectError: true,
		},
		{
			name: "invalid compute profile format",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "invalid-flavor",
					RootDiskSize:   100,
				},
			},
			expectError: true,
		},
		{
			name: "invalid region format",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "invalid",
					ComputeProfile: "a1a.m",
					RootDiskSize:   100,
				},
			},
			expectError: true,
		},
		{
			name: "disk too small",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					RootDiskSize:   5,
				},
			},
			expectError: true,
		},
		{
			name: "disk too large",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					RootDiskSize:   15000,
				},
			},
			expectError: true,
		},
		{
			name: "valid GPU compute profile",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "gn-l40s.m",
					Image:          string(compute.DiskImageUbuntu2404),
					RootDiskSize:   100,
				},
			},
			expectError:     false,
			expectWarning:   true,
			warningContains: "GPU-enabled",
		},
		{
			name: "missing image",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					RootDiskSize:   100,
				},
			},
			expectError: true,
		},
		{
			name: "invalid disk image",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					Image:          "not-a-real-image",
					RootDiskSize:   100,
				},
			},
			expectError: true,
		},
		{
			name: "small disk warning",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					Image:          string(compute.DiskImageUbuntu2404),
					RootDiskSize:   20,
				},
			},
			expectError:     false,
			expectWarning:   true,
			warningContains: "not recommended",
		},
		{
			name: "duplicate additional disk names",
			machine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					Image:          string(compute.DiskImageUbuntu2404),
					RootDiskSize:   100,
					AdditionalDisks: []AdditionalDiskSpec{
						{Name: "data", SizeGB: 100},
						{Name: "data", SizeGB: 200},
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := machineValidator.ValidateCreate(context.Background(), tt.machine)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			if tt.expectWarning {
				g.Expect(warnings).ToNot(BeEmpty())
				g.Expect(warnings[0]).To(ContainSubstring(tt.warningContains))
			} else {
				g.Expect(warnings).To(BeEmpty())
			}
		})
	}
}

func TestEvrocMachineValidateUpdate(t *testing.T) {
	g := NewWithT(t)

	oldMachine := &EvrocMachine{
		Spec: EvrocMachineSpec{
			Project:        "test-project",
			Region:         "se-sto",
			ComputeProfile: "a1a.m",
			Image:          string(compute.DiskImageUbuntu2404),
			RootDiskSize:   100,
		},
	}

	tests := []struct {
		name        string
		newMachine  *EvrocMachine
		expectError bool
	}{
		{
			name: "no changes",
			newMachine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					Image:          string(compute.DiskImageUbuntu2404),
					RootDiskSize:   100,
				},
			},
			expectError: false,
		},
		{
			name: "change project (immutable)",
			newMachine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "different-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.m",
					Image:          string(compute.DiskImageUbuntu2404),
					RootDiskSize:   100,
				},
			},
			expectError: true,
		},
		{
			name: "change region (immutable)",
			newMachine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "eu-sto",
					ComputeProfile: "a1a.m",
					Image:          string(compute.DiskImageUbuntu2404),
					RootDiskSize:   100,
				},
			},
			expectError: true,
		},
		{
			name: "change compute profile (immutable)",
			newMachine: &EvrocMachine{
				Spec: EvrocMachineSpec{
					Project:        "test-project",
					Region:         "se-sto",
					ComputeProfile: "a1a.l",
					Image:          string(compute.DiskImageUbuntu2404),
					RootDiskSize:   100,
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := machineValidator.ValidateUpdate(context.Background(), oldMachine, tt.newMachine)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestEvrocMachineBYOIValidation(t *testing.T) {
	g := NewWithT(t)

	validBase := func(image string) *EvrocMachine {
		return &EvrocMachine{
			Spec: EvrocMachineSpec{
				Project:        "test-project",
				Region:         "se-sto",
				ComputeProfile: "a1a.m",
				Image:          image,
				RootDiskSize:   100,
			},
		}
	}

	tests := []struct {
		name        string
		machine     *EvrocMachine
		expectError bool
	}{
		{
			name:        "valid ubuntu 24.04",
			machine:     validBase(string(compute.DiskImageUbuntu2404)),
			expectError: false,
		},
		{
			name:        "valid ubuntu-minimal 24.04",
			machine:     validBase(string(compute.DiskImageUbuntuMinimal2404)),
			expectError: false,
		},
		{
			name:        "valid SL Micro 6.1",
			machine:     validBase(string(compute.DiskImageSLMicro61)),
			expectError: false,
		},
		{
			name:        "valid Rocky 10.0",
			machine:     validBase(string(compute.DiskImageRocky100)),
			expectError: false,
		},
		{
			name:        "valid openSUSE 15.6",
			machine:     validBase(string(compute.DiskImageOpenSUSE156)),
			expectError: false,
		},
		{
			name:        "valid SLES 15.6",
			machine:     validBase(string(compute.DiskImageSLES156)),
			expectError: false,
		},
		{
			name:        "invalid custom image name",
			machine:     validBase("my-custom-image-v1"),
			expectError: true,
		},
		{
			name:        "invalid image with correct-looking format",
			machine:     validBase("ubuntu.99-99.1"),
			expectError: true,
		},
		{
			name:        "empty image",
			machine:     validBase(""),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := machineValidator.ValidateCreate(context.Background(), tt.machine)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
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

func TestEvrocMachineValidateDelete(t *testing.T) {
	machine := &EvrocMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
		},
		Spec: EvrocMachineSpec{
			Project:        "test-project",
			Region:         "se-sto",
			ComputeProfile: string(compute.VMSizeA1aXS),
			Image:          string(compute.DiskImageUbuntuMinimal2404),
		},
	}

	warnings, err := machineValidator.ValidateDelete(context.Background(), machine)
	assert.NoError(t, err)
	assert.Nil(t, warnings)
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
