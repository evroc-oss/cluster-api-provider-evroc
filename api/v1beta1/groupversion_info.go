// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

// Package v1beta1 contains API Schema definitions for the infrastructure v1beta1 API group
// +kubebuilder:object:generate=true
// +groupName=infrastructure.cluster.x-k8s.io
package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// addKnownTypes registers this group's types with the scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&EvrocCluster{}, &EvrocClusterList{},
		&EvrocMachine{}, &EvrocMachineList{},
		&EvrocClusterTemplate{}, &EvrocClusterTemplateList{},
		&EvrocMachineTemplate{}, &EvrocMachineTemplateList{},
	)
	// AddToGroupVersion allows the serialization of clientsets like k8s.io/client-go.
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
