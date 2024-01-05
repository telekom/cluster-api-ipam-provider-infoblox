package predicates

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestClaimReferencesPoolKind(t *testing.T) {
	tests := []struct {
		name   string
		ref    corev1.TypedLocalObjectReference
		result bool
	}{
		{
			name: "true for valid reference",
			ref: corev1.TypedLocalObjectReference{
				APIGroup: ptr.To[string]("ipam.cluster.x-k8s.io"),
				Kind:     "InfobloxIPPool",
			},
			result: true,
		},
		{
			name: "false when kind does not match",
			ref: corev1.TypedLocalObjectReference{
				APIGroup: ptr.To[string]("ipam.cluster.x-k8s.io"),
				Kind:     "OutOfClusterIPPool",
			},
			result: false,
		},
		{
			name: "false when no group is set",
			ref: corev1.TypedLocalObjectReference{
				Kind: "InfobloxIPPool",
			},
			result: false,
		},
		{
			name: "false when group does not match",
			ref: corev1.TypedLocalObjectReference{
				APIGroup: ptr.To[string]("cluster.x-k8s.io"),
				Kind:     "InfobloxIPPool",
			},
			result: false,
		},
	}

	gk := metav1.GroupKind{
		Group: "ipam.cluster.x-k8s.io",
		Kind:  "InfobloxIPPool",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			claim := &ipamv1.IPAddressClaim{
				Spec: ipamv1.IPAddressClaimSpec{
					PoolRef: tt.ref,
				},
			}
			funcs := ClaimReferencesPoolKind(gk)
			g.Expect(funcs.CreateFunc(event.CreateEvent{Object: claim})).To(Equal(tt.result))
			g.Expect(funcs.DeleteFunc(event.DeleteEvent{Object: claim})).To(Equal(tt.result))
			g.Expect(funcs.GenericFunc(event.GenericEvent{Object: claim})).To(Equal(tt.result))
			g.Expect(funcs.UpdateFunc(event.UpdateEvent{ObjectNew: claim})).To(Equal(tt.result))
		})
	}
}
