package hostname

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:generate mockgen -destination=mock/resolver.go -package=mock . Resolver

// Resolver is an interface used to get hostname of the machine.
type Resolver interface {
	GetHostname(context.Context, *ipamv1.IPAddressClaim) (string, error)
}

// OwnerChainResolver traverses the owner chain and uses the name of the last owner reference as the hostname.
type OwnerChainResolver struct {
	client.Client
	Chain []metav1.GroupKind
}

// GetHostname returns the hostname for the specified claim.
func (r *OwnerChainResolver) GetHostname(ctx context.Context, claim *ipamv1.IPAddressClaim) (string, error) {
	obj := client.Object(claim)
	for i, c := range r.Chain {
		ref, err := findOwnerReferenceWithGK(obj, c)
		if err != nil {
			return "", fmt.Errorf("failed to find next owner in chain: %w", err)
		}
		// if this is the last element in the chain, return the name
		if i >= len(r.Chain)-1 {
			return ref.Name, nil
		}
		nextObj := &unstructured.Unstructured{}
		nextObj.SetAPIVersion(ref.APIVersion)
		nextObj.SetKind(ref.Kind)
		if err := r.Get(ctx, types.NamespacedName{Namespace: obj.GetNamespace(), Name: ref.Name}, nextObj); err != nil {
			return "", fmt.Errorf("failed to fetch next owner in chain: %w", err)
		}
		obj = nextObj
	}
	// should be unreachable
	return "", fmt.Errorf("failed to follow owner chain")
}

// findOwnerReferenceWithGK searches the owner references of an object and returns the first with the specified [metav1.GroupVersion].
func findOwnerReferenceWithGK(obj client.Object, gk metav1.GroupKind) (metav1.OwnerReference, error) {
	for _, o := range obj.GetOwnerReferences() {
		if o.Kind == gk.Kind && apiVersionToGroupVersion(o.APIVersion).Group == gk.Group {
			return o, nil
		}
	}
	return metav1.OwnerReference{}, fmt.Errorf("failed to find owner reference with kind '%s'", gk.String())
}

// apiVersionToGroupVersion converts an api version string to a [metav1.GroupVersion]
func apiVersionToGroupVersion(apiVersion string) metav1.GroupVersion {
	s := strings.Split(apiVersion, "/")
	if len(s) == 2 {
		return metav1.GroupVersion{Group: s[0], Version: s[1]}
	}
	return metav1.GroupVersion{Version: apiVersion}
}
