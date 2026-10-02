package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InfobloxIPPoolKind is the Kind of the InfobloxIPPool resource.
const InfobloxIPPoolKind = "InfobloxIPPool"

// InfobloxIPPoolSpec defines the desired state of InfobloxIPPool.
type InfobloxIPPoolSpec struct {

	// Instance is the Infoblox instance to use.
	//
	// +kubebuilder:validation:Required
	InstanceRef InstanceReference `json:"instance,omitzero"`

	// Subnets is the subnet to assign IP addresses from.
	// Can be omitted if addresses or first, last and prefix are set.
	//
	// +listType=map
	// +listMapKey=cidr
	// +kubebuilder:validation:Required
	Subnets []Subnet `json:"subnets,omitzero"`

	// NetworkView defines Infoblox netwok view to be used with pool.
	//
	// +kubebuilder:validation:Optional
	NetworkView string `json:"networkView,omitzero"`

	// DNSView defines Infoblox DNS view to be used with pool.
	//
	// +kubebuilder:validation:Optional
	DNSView string `json:"dnsView,omitzero"`

	// DNSZone is the DNS zone within which hostnames will be allocated.
	//
	// +kubebuilder:validation:Optional
	DNSZone string `json:"dnsZone,omitzero"`
}

// InstanceReference is a reference to an infoblox instance resource.
type InstanceReference struct {

	// Name of the referenced Infoblox Instance resource.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength:=1
	Name string `json:"name,omitzero"`
}

// Subnet defines the CIDR and Gateway.
type Subnet struct {

	// CIDR for the subnet.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=43
	// +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$|^[0-9a-fA-F:]+/[0-9]{1,3}$`
	CIDR string `json:"cidr,omitzero"`

	// Gateway for the subnet.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=39
	// +kubebuilder:validation:Pattern=`^$|^([0-9]{1,3}\.){3}[0-9]{1,3}$|^[0-9a-fA-F:]+$`
	Gateway string `json:"gateway,omitzero"`
}

// InfobloxIPPoolStatus defines the observed state of InfobloxIPPool.
type InfobloxIPPoolStatus struct {
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitzero"`
}

// InfobloxIPPool is the Schema for the InfobloxIPPools API.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Instance",type="string",JSONPath=".spec.instance.name",description="InfobloxInstance to allocate IPs from"
// +kubebuilder:printcolumn:name="Network view",type="string",JSONPath=".spec.networkView",description="Default network view"
// +kubebuilder:printcolumn:name="DNSZone",type="string",JSONPath=".spec.dnsZone",description="The DNS zone within which hostnames will be allocated"
// +kubebuilder:printcolumn:name="Subnets",type="string",JSONPath=".spec.subnets",description="Subnets to allocate IPs from",priority=1
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`,description="Status of the Ready condition"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].reason`,description="Reason of the Ready condition"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Deleted",type=date,JSONPath=`.metadata.deletionTimestamp`,priority=1
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].message`,description="Message of the Ready condition",priority=1
type InfobloxIPPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfobloxIPPoolSpec   `json:"spec,omitempty"`
	Status InfobloxIPPoolStatus `json:"status,omitempty"`
}

// InfobloxIPPoolList contains a list of InfobloxIPPool.
//
// +kubebuilder:object:root=true
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
