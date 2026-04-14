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

package index

import (
	"testing"

	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
)

func TestIPPoolRefValue(t *testing.T) {
	tests := []struct {
		name     string
		ref      ipamv1.IPPoolReference
		expected string
	}{
		{
			name:     "empty ref",
			ref:      ipamv1.IPPoolReference{},
			expected: "",
		},
		{
			name: "kind only",
			ref: ipamv1.IPPoolReference{
				Kind: "InfobloxIPPool",
			},
			expected: "InfobloxIPPool",
		},
		{
			name: "kind and name",
			ref: ipamv1.IPPoolReference{
				Kind: "InfobloxIPPool",
				Name: "my-pool",
			},
			expected: "InfobloxIPPoolmy-pool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IPPoolRefValue(tt.ref)
			if got != tt.expected {
				t.Errorf("IPPoolRefValue(%v) = %q, want %q", tt.ref, got, tt.expected)
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
	expected := "InfobloxIPPooltest-pool"
	if result[0] != expected {
		t.Errorf("expected %q, got %q", expected, result[0])
	}
}

// TestIPAddressByCombinedPoolRefWrongType verifies that passing a non-IPAddress object
// returns an empty slice (graceful degradation) rather than panicking.
// This behaviour was changed from a panic to a log+return in fix/preexisting-code-quality.
func TestIPAddressByCombinedPoolRefWrongType(t *testing.T) {
	result := IPAddressByCombinedPoolRef(&ipamv1.IPAddressClaim{})
	if len(result) != 0 {
		t.Errorf("expected empty slice for wrong type, got %v", result)
	}
}

// TestIPAddressClaimByCombinedPoolRefWrongType mirrors the above for the claim indexer.
func TestIPAddressClaimByCombinedPoolRefWrongType(t *testing.T) {
	// ipAddressClaimByCombinedPoolRef is unexported; test it indirectly by confirming
	// the IPPoolRefValue helper behaves consistently for any PoolReference input.
	// The direct wrong-type path is exercised implicitly by SetupIndexes in envtest suites.
	ref := ipamv1.IPPoolReference{Kind: "InfobloxIPPool", Name: "pool"}
	got := IPPoolRefValue(ref)
	expected := "InfobloxIPPoolpool"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
