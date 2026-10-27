package predicates

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// poolRefCases are the reference variants both predicates have to classify the same way.
var poolRefCases = []struct {
	name   string
	ref    ipamv1.IPPoolReference
	result bool
}{
	{
		name: "true for valid reference",
		ref: ipamv1.IPPoolReference{
			APIGroup: "ipam.cluster.x-k8s.io",
			Kind:     "InfobloxIPPool",
		},
		result: true,
	},
	{
		name: "false when kind does not match",
		ref: ipamv1.IPPoolReference{
			APIGroup: "ipam.cluster.x-k8s.io",
			Kind:     "OutOfClusterIPPool",
		},
		result: false,
	},
	{
		name: "true when no group is set for a legacy reference",
		ref: ipamv1.IPPoolReference{
			Kind: "InfobloxIPPool",
		},
		result: true,
	},
	{
		name: "false when group does not match",
		ref: ipamv1.IPPoolReference{
			APIGroup: "cluster.x-k8s.io",
			Kind:     "InfobloxIPPool",
		},
		result: false,
	},
}

var testGroupKind = metav1.GroupKind{
	Group: "ipam.cluster.x-k8s.io",
	Kind:  "InfobloxIPPool",
}

// expectAllEventFuncs asserts that every event type is classified identically, since the predicates
// apply the same rule to all of them.
func expectAllEventFuncs(t *testing.T, funcs predicate.Funcs, obj client.Object, expected bool) {
	t.Helper()
	g := NewWithT(t)
	g.Expect(funcs.CreateFunc(event.CreateEvent{Object: obj})).To(Equal(expected))
	g.Expect(funcs.DeleteFunc(event.DeleteEvent{Object: obj})).To(Equal(expected))
	g.Expect(funcs.GenericFunc(event.GenericEvent{Object: obj})).To(Equal(expected))
	g.Expect(funcs.UpdateFunc(event.UpdateEvent{ObjectNew: obj})).To(Equal(expected))
	// assert update func uses ObjectNew and ignores ObjectOld (by passing a radom object instead of the expected type).
	g.Expect(funcs.UpdateFunc(event.UpdateEvent{ObjectOld: &corev1.ConfigMap{}, ObjectNew: obj})).To(Equal(expected))
}

func TestClaimReferencesPoolKind(t *testing.T) {
	for _, tt := range poolRefCases {
		t.Run(tt.name, func(t *testing.T) {
			claim := &ipamv1.IPAddressClaim{
				Spec: ipamv1.IPAddressClaimSpec{PoolRef: tt.ref},
			}
			expectAllEventFuncs(t, ClaimReferencesPoolKind(testGroupKind), claim, tt.result)
		})
	}

	t.Run("false for an object that is not a claim", func(t *testing.T) {
		address := &ipamv1.IPAddress{
			Spec: ipamv1.IPAddressSpec{PoolRef: poolRefCases[0].ref},
		}
		expectAllEventFuncs(t, ClaimReferencesPoolKind(testGroupKind), address, false)
	})
}

func TestAddressReferencesPoolKind(t *testing.T) {
	for _, tt := range poolRefCases {
		t.Run(tt.name, func(t *testing.T) {
			address := &ipamv1.IPAddress{
				Spec: ipamv1.IPAddressSpec{PoolRef: tt.ref},
			}
			expectAllEventFuncs(t, AddressReferencesPoolKind(testGroupKind), address, tt.result)
		})
	}

	t.Run("false for an object that is not an address", func(t *testing.T) {
		claim := &ipamv1.IPAddressClaim{
			Spec: ipamv1.IPAddressClaimSpec{PoolRef: poolRefCases[0].ref},
		}
		expectAllEventFuncs(t, AddressReferencesPoolKind(testGroupKind), claim, false)
	})
}
