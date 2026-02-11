package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InfobloxIPPoolSpec defines the desired state of InfobloxIPPool.
type InfobloxIPPoolSpec struct {

	// +kubebuilder:validation:Required

	// Instance is the Infoblox instance to use.
	InstanceRef InstanceReference `json:"instance,omitzero"`

	// +kubebuilder:validation:Required

	// Subnets is the subnet to assign IP addresses from.
	// Can be omitted if addresses or first, last and prefix are set.
	Subnets []Subnet `json:"subnets,omitzero"`

	// +kubebuilder:validation:Optional

	// NetworkView defines Infoblox netwok view to be used with pool.
	NetworkView string `json:"networkView,omitzero"`

	// +kubebuilder:validation:Optional

	// DNSView defines Infoblox DNS view to be used with pool.
	DNSView string `json:"dnsView,omitzero"`

	// +kubebuilder:validation:Optional

	// DNSZone is the DNS zone within which hostnames will be allocated.
	DNSZone string `json:"dnsZone,omitzero"`
}

// InstanceReference is a reference to an infoblox instance resource.
type InstanceReference struct {

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength:=1

	// Name of the referenced Infoblox Instance resource.
	Name string `json:"name,omitzero"`
}

// InfobloxIPPoolStatus defines the observed state of InfobloxIPPool.
type InfobloxIPPoolStatus struct {
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitzero"`
}

// Subnet defines the CIDR and Gateway.
type Subnet struct {

	// +kubebuilder:validation:Required

	// CIDR for the subnet.
	CIDR string `json:"cidr,omitzero"`

	// +kubebuilder:validation:Optional

	// Gateway for the subnet.
	Gateway string `json:"gateway,omitzero"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Network view",type="string",JSONPath=".spec.networkView",description="Default network view"
// +kubebuilder:printcolumn:name="Subnets",type="string",JSONPath=".spec.subnets",description="Subnets to allocate IPs from"
// +kubebuilder:printcolumn:name="DNSZone",type="string",JSONPath=".spec.dnsZone",description="The DNS zone within which hostnames will be allocated"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Deleted",type=date,JSONPath=`.metadata.deletionTimestamp`,priority=1

// InfobloxIPPool is the Schema for the InfobloxIPPools API.
type InfobloxIPPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfobloxIPPoolSpec   `json:"spec,omitempty"`
	Status InfobloxIPPoolStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// InfobloxIPPoolList contains a list of InfobloxIPPool.
type InfobloxIPPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InfobloxIPPool `json:"items"`
}

// GetConditions returns pool conditions.
func (i *InfobloxIPPool) GetConditions() []metav1.Condition {
	return i.Status.Conditions
}

// SetConditions sets pool conditions.
func (i *InfobloxIPPool) SetConditions(conditions []metav1.Condition) {
	i.Status.Conditions = conditions
}

func init() {
	SchemeBuilder.Register(&InfobloxIPPool{}, &InfobloxIPPoolList{})
}
