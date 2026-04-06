/*
Copyright 2023 The Kubernetes Authors.

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

// Package poolutil implements utility functions to manage a pool of IP addresses.
package poolutil

import (
	"context"

	"github.com/telekom/cluster-api-ipam-provider-infoblox/internal/index"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListAddressesInUse fetches all IPAddresses belonging to the specified pool.
// Note: requires `index.ipAddressByCombinedPoolRef` to be set up.
func ListAddressesInUse(ctx context.Context, c client.Client, namespace string, poolRef ipamv1.IPPoolReference) ([]ipamv1.IPAddress, error) {
	addresses := &ipamv1.IPAddressList{}
	err := c.List(ctx, addresses,
		client.MatchingFields{
			index.IPAddressPoolRefCombinedField: index.IPPoolRefValue(poolRef),
		},
		client.InNamespace(namespace),
	)
	return addresses.Items, err
}

// ListClaimsReferencingPool fetches all IPAddressClaims belonging to the specified pool.
func ListClaimsReferencingPool(ctx context.Context, c client.Client, namespace string, poolRef ipamv1.IPPoolReference) ([]ipamv1.IPAddressClaim, error) {
	claims := &ipamv1.IPAddressClaimList{}
	err := c.List(ctx, claims,
		client.MatchingFields{
			index.IPAddressClaimPoolRefCombinedField: index.IPPoolRefValue(poolRef),
		},
		client.InNamespace(namespace),
	)
	return claims.Items, err
}
