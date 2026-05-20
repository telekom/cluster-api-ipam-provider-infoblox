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

package controllers

import "testing"

func TestDetermineDNSView(t *testing.T) {
	tests := []struct {
		name                   string
		poolDNSView            string
		instanceDefaultDNSView string
		networkView            string
		expected               string
	}{
		{"pool DNS view set returns pool DNS view", "pool-view", "instance-view", "custom-view", "pool-view"},
		{"instance default DNS view used when pool empty", "", "instance-view", "custom-view", "instance-view"},
		{"default returned when network view empty", "", "", "", "default"},
		{"default returned when network view is default", "", "", "default", "default"},
		{"derived DNS view returned for custom network view", "", "", "custom-view", "default.custom-view"},
		{"pool DNS view takes precedence over instance default", "pool-view", "instance-view", "default", "pool-view"},
		{"pool DNS view takes precedence over network view derivation", "pool-view", "", "custom-view", "pool-view"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineDNSView(tt.poolDNSView, tt.instanceDefaultDNSView, tt.networkView)
			if got != tt.expected {
				t.Errorf("determineDNSView(%q, %q, %q) = %q, want %q",
					tt.poolDNSView, tt.instanceDefaultDNSView, tt.networkView, got, tt.expected)
			}
		})
	}
}
