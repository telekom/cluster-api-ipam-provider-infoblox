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

package index

import (
	"testing"

	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
)

func TestIPPoolRefValue(t *testing.T) {
	tests := []struct {
		name string
		ref  ipamv1.IPPoolReference
		want string
	}{
		{
			name: "explicit API group",
			ref: ipamv1.IPPoolReference{
				APIGroup: "infrastructure.cluster.x-k8s.io",
				Kind:     "ExternalIPPool",
				Name:     "pool-1",
			},
			want: "infrastructure.cluster.x-k8s.io/ExternalIPPool/pool-1",
		},
		{
			name: "default API group",
			ref: ipamv1.IPPoolReference{
				Kind: "InClusterIPPool",
				Name: "pool-1",
			},
			want: ipamv1.GroupVersion.Group + "/InClusterIPPool/pool-1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IPPoolRefValue(test.ref); got != test.want {
				t.Fatalf("IPPoolRefValue() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestIPAddressByCombinedPoolRef(t *testing.T) {
	addr := &ipamv1.IPAddress{}
	addr.Spec.PoolRef = ipamv1.IPPoolReference{
		Kind: "InfobloxIPPool",
		Name: "test-pool",
	}

	result := IPAddressByCombinedPoolRef(addr)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	want := ipamv1.GroupVersion.Group + "/InfobloxIPPool/test-pool"
	if result[0] != want {
		t.Errorf("IPAddressByCombinedPoolRef() = %q, want %q", result[0], want)
	}
}
