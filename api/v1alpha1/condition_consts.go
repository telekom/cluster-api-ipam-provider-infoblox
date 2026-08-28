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

package v1alpha1

const (
	// ReadyReason is a generic Reason for the Ready condition to be true.
	ReadyReason = "Ready"

	// PoolNotReadyReason indicates that the InfobloxIPPool referenced by a claim is not ready.
	PoolNotReadyReason = "PoolNotReady"
	// AddressAllocatedReason indicates that an IP address has been successfully allocated from the InfobloxIPPool.
	AddressAllocatedReason = "AddressAllocated"
	// AllocationFailedReason indicates that the allocation of an IP address from the InfobloxIPPool has failed.
	AllocationFailedReason = "AllocationFailed"
	// ReleaseFailedReason indicates that the IP address held by a claim could not be released back to Infoblox.
	ReleaseFailedReason = "ReleaseFailed"

	// ClaimsPendingDeletionReason indicates that IPAddressClaims still reference the InfobloxIPPool, blocking its deletion.
	ClaimsPendingDeletionReason = "ClaimsPendingDeletion"

	// AuthenticationFailedReason indicates that the credentials provided to Infoblox were invalid.
	AuthenticationFailedReason = "AuthenticationFailed"
	// InfobloxCheckFailedReason indicates that a check against the Infoblox instance could not be
	// performed, so whether the checked object exists is unknown.
	InfobloxCheckFailedReason = "InfobloxCheckFailed"
	// InfobloxValidationFailedReason indicates that the Infoblox API could not validate the requested object.
	InfobloxValidationFailedReason = "InfobloxValidationFailed"

	// NetworkViewNotFoundReason indicates that the specified network view could not be found on the Infoblox instance.
	NetworkViewNotFoundReason = "NetworkViewNotFound"
	// DNSViewNotFoundReason indicates that the specified DNS view could not be found on the Infoblox instance.
	DNSViewNotFoundReason = "DNSViewNotFound"
	// NetworkNotFoundReason indicates that the specified network could not be found on the Infoblox instance.
	NetworkNotFoundReason = "NetworkNotFound"
	// ConfigurationValidReason indicates that the configuration of the InfobloxInstance has been validated successfully.
	ConfigurationValidReason = "ConfigurationValid"
)
