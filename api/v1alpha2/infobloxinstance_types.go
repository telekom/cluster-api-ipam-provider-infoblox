/*
Copyright 2023 Deutsche Telekom AG.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// InfobloxInstanceSpec defines the desired state of InfobloxInstance
type InfobloxInstanceSpec struct {
	// Endpoint is the API endpoint of the Infoblox instance.
	Host string `json:"host"`
	// Port
	// +kubebuilder:default="443"
	Port string `json:"port"`
	// WAPIVersion
	WAPIVersion string `json:"wapiVersion"`
	// CredentialsSecretRef is a reference to a secret containing the username and password to be used for authentication.
	// Both `username`/`password` and `clientCert`/`clientKey` are supported and one of either combination is required to be present as keys in the secret.
	CredentialsSecretRef corev1.LocalObjectReference `json:"credentialsSecretRef"`
	// We can consider adding a allowedNamespacesSelector, similar to CAPV, for access control.

	// DefaultNetworkView is the default network view used when interacting with Infoblox.
	// InfobloxIPPools will inherit this value when not explicitly specifying a network view.
	// +optional
	DefaultNetworkView string `json:"defaultNetworkView,omitempty"`
	//
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`
	// maybe add a way to reference a custom CA?
}

// InfobloxInstanceStatus defines the observed state of InfobloxInstance
type InfobloxInstanceStatus struct {
	Conditions clusterv1.Conditions `json:"conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:subresource:status
//+kubebuilder:storageversion

// InfobloxInstance is the Schema for the infobloxinstances API
type InfobloxInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfobloxInstanceSpec   `json:"spec,omitempty"`
	Status InfobloxInstanceStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// InfobloxInstanceList contains a list of InfobloxInstance
type InfobloxInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InfobloxInstance `json:"items"`
}

func (i *InfobloxInstance) GetConditions() clusterv1.Conditions {
	return i.Status.Conditions
}

func (i *InfobloxInstance) SetConditions(conditions clusterv1.Conditions) {
	i.Status.Conditions = conditions
}

func init() {
	SchemeBuilder.Register(&InfobloxInstance{}, &InfobloxInstanceList{})
}
