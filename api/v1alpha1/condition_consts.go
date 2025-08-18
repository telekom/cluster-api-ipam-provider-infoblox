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

package v1alpha1

const (
	// AuthenticationFailedReason indicates that the credentials provided to Infoblox were invalid.
	AuthenticationFailedReason = "AuthenticationFailed"
	// NetworkViewNotFoundReason indicates that the specified network view could not be found on the Infoblox instance.
	NetworkViewNotFoundReason = "NetworkViewNotFound"
	// DNSViewNotFoundReason indicates that the specified DNS view could not be found on the Infoblox instance.
	DNSViewNotFoundReason = "DNSViewNotFound"
	// NetworkNotFoundReason indicates that the specified network could not be found on the Infoblox instance.
	NetworkNotFoundReason = "NetworkNotFound"
)
