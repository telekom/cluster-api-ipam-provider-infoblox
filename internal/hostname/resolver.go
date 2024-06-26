// Package hostname implements hostname resolving strategies.
package hostname

import (
	"context"
	"fmt"
	"slices"
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

// SearchOwnerReferenceResolver performs a depth search on the owner references until it finds the specified [metav1.GroupKind]
// and uses it's name as the hostname.
type SearchOwnerReferenceResolver struct {
	client.Client
	MaxDepth  int
	SearchFor metav1.GroupKind
}

// GetHostname returns the hostname for the specified claim.
func (r *SearchOwnerReferenceResolver) GetHostname(ctx context.Context, claim *ipamv1.IPAddressClaim) (string, error) {
	if r.MaxDepth == 0 {
		r.MaxDepth = 5
	}
	obj := client.Object(claim)
	name, err := r.find(ctx, obj, 1)
	if err != nil {
		return "", err
	}
	if name != "" {
		return name, nil
	}
	return "", fmt.Errorf("failed to find owner reference to specified group and kind")
}

func (r *SearchOwnerReferenceResolver) find(ctx context.Context, obj client.Object, currentDepth int) (string, error) {
	nextRefs := []metav1.OwnerReference{}
	for _, o := range obj.GetOwnerReferences() {
		if o.Kind == r.SearchFor.Kind && apiVersionToGroupVersion(o.APIVersion).Group == r.SearchFor.Group {
			return o.Name, nil
		}

		nextRefs = append(nextRefs, o)
	}

	// We'll try to iterate through promising things first to reduce the amount of api requests.
	// The simple heuristic is that anything in the infrastructure.capi.x-k8s.io group or anything that contains Machine in
	// it's name comes first. The name is more important than the group.
	// We don't care for equality since this is just optimization.
	slices.SortFunc(nextRefs, func(a, b metav1.OwnerReference) int {
		if strings.Contains(b.Kind, "Machine") {
			return 1
		}
		if strings.Contains(a.Kind, "Machine") || strings.HasPrefix(b.APIVersion, "infrastructure") {
			return -1
		}
		if strings.HasPrefix(b.APIVersion, "infrastructure") {
			return 1
		}
		return 0
	})

	for _, o := range nextRefs {
		if currentDepth >= r.MaxDepth {
			continue
		}

		obj2 := &unstructured.Unstructured{}
		obj2.SetAPIVersion(o.APIVersion)
		obj2.SetKind(o.Kind)
		if err := r.Client.Get(ctx, types.NamespacedName{Name: o.Name, Namespace: obj2.GetNamespace()}, obj2); err != nil {
			return "", err
		}
		if name, err := r.find(ctx, obj2, currentDepth+1); name != "" || err != nil {
			return name, err
		}
	}
	return "", nil
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

// apiVersionToGroupVersion converts an api version string to a [metav1.GroupVersion].
func apiVersionToGroupVersion(apiVersion string) metav1.GroupVersion {
	s := strings.Split(apiVersion, "/")
	if len(s) == 2 {
		return metav1.GroupVersion{Group: s[0], Version: s[1]}
	}
	return metav1.GroupVersion{Version: apiVersion}
}
