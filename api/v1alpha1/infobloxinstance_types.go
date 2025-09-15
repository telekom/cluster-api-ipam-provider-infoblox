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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InfobloxInstanceSpec defines the desired state of InfobloxInstance.
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
	// DefaultDNSView is the default DNS view used when interacting with Infoblox.
	// InfobloxIPPools will inherit this value when not explicitly specifying a DNS view.
	// +optional
	DefaultDNSView string `json:"defaultDNSView,omitempty"`
	// DisableTLSVerification if set 'true', certificates for SSL commuunication with Infoblox instance will be not verified
	DisableTLSVerification bool `json:"disableTLSVerification,omitempty"`
	// CustomCAPath can be used to point Infoblox client to a file with a list of accepted certificate authorities. Only used if DisableTLSVerification is set to 'false'.
	// + optional
	CustomCAPath string `json:"customCAPath,omitempty"`
}

// InfobloxInstanceStatus defines the observed state of InfobloxInstance.
type InfobloxInstanceStatus struct {
	Conditions []metav1.Condition `json:"conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".spec.host",description="Infoblox host's address"
// +kubebuilder:printcolumn:name="Port",type="string",JSONPath=".spec.port",description="Networking port of the Infoblox host"
// +kubebuilder:printcolumn:name="WAPI ver.",type="string",JSONPath=".spec.wapiVersion",description="Version of web API to be used"

// InfobloxInstance is the Schema for the infobloxinstances API.
type InfobloxInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfobloxInstanceSpec   `json:"spec,omitempty"`
	Status InfobloxInstanceStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// InfobloxInstanceList contains a list of InfobloxInstance.
type InfobloxInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InfobloxInstance `json:"items"`
}

// GetConditions gets cluster conditions.
func (i *InfobloxInstance) GetConditions() []metav1.Condition {
	return i.Status.Conditions
}

// SetConditions sets cluster conditions.
func (i *InfobloxInstance) SetConditions(conditions []metav1.Condition) {
	i.Status.Conditions = conditions
}

func init() {
	SchemeBuilder.Register(&InfobloxInstance{}, &InfobloxInstanceList{})
}
