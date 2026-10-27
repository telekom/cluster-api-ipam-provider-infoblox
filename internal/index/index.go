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

// Package index implements several indexes for the controller-runtime Managers cache.
package index

import (
	"context"
	"fmt"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// IPAddressPoolRefCombinedField is an index for the poolRef of an IPAddress.
	IPAddressPoolRefCombinedField = "index.poolRef"

	// IPAddressClaimPoolRefCombinedField is an index for the poolRef of an IPAddressClaim.
	IPAddressClaimPoolRefCombinedField = "index.poolRef"
)

// SetupIndexes adds the indexes to the provided field indexer.
func SetupIndexes(ctx context.Context, indexer client.FieldIndexer) error {
	err := indexer.IndexField(ctx, &ipamv1.IPAddress{},
		IPAddressPoolRefCombinedField,
		IPAddressByCombinedPoolRef,
	)
	if err != nil {
		return err
	}

	return indexer.IndexField(ctx, &ipamv1.IPAddressClaim{},
		IPAddressClaimPoolRefCombinedField,
		ipAddressClaimByCombinedPoolRef,
	)
}

// IPAddressByCombinedPoolRef fulfills the IndexerFunc for IPAddress poolRefs.
func IPAddressByCombinedPoolRef(o client.Object) []string {
	ip, ok := o.(*ipamv1.IPAddress)
	if !ok {
		panic(fmt.Sprintf("Expected an IPAddress but got a %T", o))
	}
	return IPPoolRefValues(ip.Spec.PoolRef)
}

func ipAddressClaimByCombinedPoolRef(o client.Object) []string {
	ip, ok := o.(*ipamv1.IPAddressClaim)
	if !ok {
		panic(fmt.Sprintf("Expected an IPAddressClaim but got a %T", o))
	}
	return IPPoolRefValues(ip.Spec.PoolRef)
}

// IPPoolRefValue turns an IPPoolReference into an indexable cache key.
func IPPoolRefValue(ref ipamv1.IPPoolReference) string {
	return fmt.Sprintf("%s/%s/%s", ref.APIGroup, ref.Kind, ref.Name)
}

// IPPoolRefValues returns all index keys that can refer to the same provider pool.
func IPPoolRefValues(ref ipamv1.IPPoolReference) []string {
	values := []string{IPPoolRefValue(ref)}
	if ref.Kind != "InfobloxIPPool" ||
		(ref.APIGroup != "" && ref.APIGroup != v1alpha1.GroupVersion.Group) {
		return values
	}

	legacyRef := ref
	legacyRef.APIGroup = ""
	canonicalRef := ref
	canonicalRef.APIGroup = v1alpha1.GroupVersion.Group
	for _, value := range []string{IPPoolRefValue(legacyRef), IPPoolRefValue(canonicalRef)} {
		if values[0] != value {
			values = append(values, value)
		}
	}
	return values
}
