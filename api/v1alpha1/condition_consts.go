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
	// AddressDriftedReason indicates that the address of a claim's IPAddress is no longer assigned to the claim's host in Infoblox.
	AddressDriftedReason = "AddressDrifted"
	// AddressInvalidReason indicates that the address of a claim's IPAddress is invalid and cannot be used.
	AddressInvalidReason = "AddressInvalid"
	// VerificationFailedReason indicates that it could not be checked whether Infoblox still holds the address of a claim's IPAddress.
	VerificationFailedReason = "VerificationFailed"
	// ReleaseFailedReason indicates that the IP address held by a claim could not be released back to Infoblox.
	ReleaseFailedReason = "ReleaseFailed"

	// ClaimsPendingDeletionReason indicates that IPAddressClaims still reference the InfobloxIPPool, blocking its deletion.
	ClaimsPendingDeletionReason = "ClaimsPendingDeletion"

	// AuthenticationFailedReason indicates that the credentials provided to Infoblox were invalid.
	AuthenticationFailedReason = "AuthenticationFailed"
	// ConfigurationInvalidReason indicates that no Infoblox client can be created from the configuration, e.g. because of an invalid TLS setting.
	ConfigurationInvalidReason = "ConfigurationInvalid"
	// InfobloxRequestFailedReason indicates that Infoblox rejected a request with a WAPI error.
	InfobloxRequestFailedReason = "InfobloxRequestFailed"
	// InfobloxConnectionFailedReason indicates that an Infoblox request failed without a WAPI response, e.g. because of a
	// transport error, an HTTP error of a proxy or an unreadable response.
	InfobloxConnectionFailedReason = "InfobloxConnectionFailed"

	// NetworkViewNotFoundReason indicates that the specified network view could not be found on the Infoblox instance.
	NetworkViewNotFoundReason = "NetworkViewNotFound"
	// DNSViewNotFoundReason indicates that the specified DNS view could not be found on the Infoblox instance.
	DNSViewNotFoundReason = "DNSViewNotFound"
	// NetworkNotFoundReason indicates that the specified network could not be found on the Infoblox instance.
	NetworkNotFoundReason = "NetworkNotFound"
	// ConfigurationValidReason indicates that the configuration of the InfobloxInstance has been validated successfully.
	ConfigurationValidReason = "ConfigurationValid"
)
