// Package v1alpha1 contains API Schema definitions for the ipam.cluster.x-k8s.io v1alpha1 API group
// +kubebuilder:ac:generate=true
// +kubebuilder:ac:output:package=../applyconfiguration
// +kubebuilder:object:generate=true
// +groupName=ipam.cluster.x-k8s.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "ipam.cluster.x-k8s.io", Version: "v1alpha1"}

	// SchemeGroupVersion is the group version used by the generated apply configurations.
	SchemeGroupVersion = GroupVersion

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(
		GroupVersion,
		&InfobloxInstance{},
		&InfobloxInstanceList{},
		&InfobloxIPPool{},
		&InfobloxIPPoolList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
