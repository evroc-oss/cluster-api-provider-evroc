// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EvrocClusterTemplateSpec defines the desired state of EvrocClusterTemplate
type EvrocClusterTemplateSpec struct {
	Template EvrocClusterTemplateResource `json:"template"`
}

// EvrocClusterTemplateResource describes the data needed to create an EvrocCluster from a template
type EvrocClusterTemplateResource struct {
	// Spec is the specification of the desired behavior of the cluster.
	Spec EvrocClusterSpec `json:"spec"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=evrocclustertemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta1=v1beta1"
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1beta1"
// +kubebuilder:metadata:labels="clusterctl.cluster.x-k8s.io="
// +kubebuilder:storageversion

// EvrocClusterTemplate is the Schema for the evrocclustertemplates API
// This template is used by ClusterClass to create EvrocCluster instances
type EvrocClusterTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec EvrocClusterTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// EvrocClusterTemplateList contains a list of EvrocClusterTemplate
type EvrocClusterTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EvrocClusterTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EvrocClusterTemplate{}, &EvrocClusterTemplateList{})
}
