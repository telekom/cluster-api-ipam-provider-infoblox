package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// InfobloxIPPoolSpec defines the desired state of InfobloxIPPool.
type InfobloxIPPoolSpec struct {
	// Instance is the Infoblox instance to use.
	InstanceRef corev1.LocalObjectReference `json:"instance"`
	// Subnets is the subnet to assign IP addresses from.
	// Can be omitted if addresses or first, last and prefix are set.
	// TODO: turn this into an array to support multiple subnets per pool
	Subnets []Subnet `json:"subnets"`
	// NetworkView
	NetworkView string `json:"networkView,omitempty"`
	// DNSZone is the DNS zone within which hostnames will be allocated.
	DNSZone string `json:"dnsZone,omitempty"`
}

// InfobloxIPPoolStatus defines the observed state of InfobloxIPPool.
type InfobloxIPPoolStatus struct {
	Conditions clusterv1.Conditions `json:"conditions"`
}

// Subnet defines the CIDR and Gateway.
type Subnet struct {
	CIDR    string `json:"cidr"`
	Gateway string `json:"gateway"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Network view",type="string",JSONPath=".spec.networkView",description="Default network view"
// +kubebuilder:printcolumn:name="Subnets",type="string",JSONPath=".spec.subnets",description="Subnets to allocate IPs from"

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
func (i *InfobloxIPPool) GetConditions() clusterv1.Conditions {
	return i.Status.Conditions
}

// SetConditions sets pool conditions.
func (i *InfobloxIPPool) SetConditions(conditions clusterv1.Conditions) {
	i.Status.Conditions = conditions
}

func init() {
	SchemeBuilder.Register(&InfobloxIPPool{}, &InfobloxIPPoolList{})
}
