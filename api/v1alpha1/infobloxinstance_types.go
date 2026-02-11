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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InfobloxInstanceSpec defines the desired state of InfobloxInstance.
type InfobloxInstanceSpec struct {

	// +kubebuilder:validation:Required

	// Endpoint is the API endpoint of the Infoblox instance.
	Host string `json:"host,omitzero"`

	// +kubebuilder:validation:Required
	// +kubebuilder:default="443"

	// Port to use when connecting to the Infoblox instance.
	Port string `json:"port,omitzero"`

	// +kubebuilder:validation:Required

	// WAPIVersion is the version of the Infoblox Web-based Application Programming Interface (WAPI) endoint.
	WAPIVersion string `json:"wapiVersion,omitzero"`

	// +kubebuilder:validation:Required

	// CredentialsSecretRef is a reference to a secret containing the username and password to be used for authentication.
	// Both `username`/`password` and `clientCert`/`clientKey` are supported and one of either combination is required to be present as keys in the secret.
	CredentialsSecretRef CredentialsReferece `json:"credentialsSecretRef,omitzero"`

	// +kubebuilder:validation:Optional

	// DefaultNetworkView is the default network view used when interacting with Infoblox.
	// InfobloxIPPools will inherit this value when not explicitly specifying a network view.
	//
	// Note: We can consider adding a allowedNamespacesSelector, similar to CAPV, for access control.
	DefaultNetworkView string `json:"defaultNetworkView,omitzero"`

	// +kubebuilder:validation:Optional

	// DefaultDNSView is the default DNS view used when interacting with Infoblox.
	// InfobloxIPPools will inherit this value when not explicitly specifying a DNS view.
	DefaultDNSView string `json:"defaultDNSView,omitzero"`

	// +kubebuilder:validation:Optional

	// DisableTLSVerification if set 'true', certificates for SSL commuunication with Infoblox instance will be not verified
	DisableTLSVerification bool `json:"disableTLSVerification,omitzero"`

	// +kubebuilder:validation:Optional

	// CustomCAPath can be used to point Infoblox client to a file with a list of accepted certificate authorities.
	// Only used if DisableTLSVerification is set to 'false'.
	CustomCAPath string `json:"customCAPath,omitzero"`
}

// CredentialsReferece is a reference to a secret containing the Infoblox instance credentials.
type CredentialsReferece struct {

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength:=1

	// Name of the referenced Infoblox Instance resource.
	Name string `json:"name,omitzero"`
}

// InfobloxInstanceStatus defines the observed state of InfobloxInstance.
type InfobloxInstanceStatus struct {
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitzero"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".spec.host",description="Infoblox host's address"
// +kubebuilder:printcolumn:name="Port",type="string",JSONPath=".spec.port",description="Networking port of the Infoblox host"
// +kubebuilder:printcolumn:name="WAPI ver.",type="string",JSONPath=".spec.wapiVersion",description="Version of web API to be used"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Deleted",type=date,JSONPath=`.metadata.deletionTimestamp`,priority=1

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
