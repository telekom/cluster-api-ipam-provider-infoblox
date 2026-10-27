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

func TestIPPoolRefValueIncludesAPIGroup(t *testing.T) {
	ref := ipamv1.IPPoolReference{
		APIGroup: "ipam.cluster.x-k8s.io",
		Kind:     "InfobloxIPPool",
		Name:     "pool",
	}
	otherGroupRef := ref
	otherGroupRef.APIGroup = "other.example.com"

	if got, other := IPPoolRefValue(ref), IPPoolRefValue(otherGroupRef); got == other {
		t.Fatalf("IPPoolRefValue ignored APIGroup: both refs produced %q", got)
	}
	if got, want := IPPoolRefValue(ref), "ipam.cluster.x-k8s.io/InfobloxIPPool/pool"; got != want {
		t.Fatalf("IPPoolRefValue() = %q, want %q", got, want)
	}
}

func TestIPPoolRefValuesIncludesLegacyAndCanonicalInfobloxKeys(t *testing.T) {
	ref := ipamv1.IPPoolReference{
		APIGroup: "",
		Kind:     "InfobloxIPPool",
		Name:     "pool",
	}

	got := IPPoolRefValues(ref)
	want := map[string]bool{
		"/InfobloxIPPool/pool":                      false,
		"ipam.cluster.x-k8s.io/InfobloxIPPool/pool": false,
	}
	for _, value := range got {
		if _, ok := want[value]; ok {
			want[value] = true
		}
	}
	for value, found := range want {
		if !found {
			t.Fatalf("IPPoolRefValues() = %v, missing %q", got, value)
		}
	}
}
