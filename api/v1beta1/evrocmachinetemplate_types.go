// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EvrocMachineTemplateSpec defines the desired state of EvrocMachineTemplate
type EvrocMachineTemplateSpec struct {
	Template EvrocMachineTemplateResource `json:"template"`
}

// EvrocMachineTemplateResource describes the data needed to create a EvrocMachine from a template
type EvrocMachineTemplateResource struct {
	// Spec is the specification of the desired behavior of the machine.
	Spec EvrocMachineSpec `json:"spec"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=evrocmachinetemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta1=v1beta1"
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1beta1"
// +kubebuilder:metadata:labels="clusterctl.cluster.x-k8s.io="
// +kubebuilder:storageversion

// EvrocMachineTemplate is the Schema for the evrocmachinetemplates API
type EvrocMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec EvrocMachineTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// EvrocMachineTemplateList contains a list of EvrocMachineTemplate
type EvrocMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EvrocMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EvrocMachineTemplate{}, &EvrocMachineTemplateList{})
}
