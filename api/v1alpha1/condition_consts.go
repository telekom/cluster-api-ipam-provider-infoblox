package v1alpha1

const (
	// ReadyCondition indicates whether a resource is ready.
	ReadyCondition = "Ready"

	// InfobloxConnectionFailedReason indicates that the connection to infoblox failed.
	InfobloxConnectionFailedReason = "InfobloxConnectionFailed"
	// InfobloxAuthenticationFailedReason indicates that the credentials provided to infoblox were invalid.
	InfobloxAuthenticationFailedReason = "InfobloxAuthenticationFailed"
	// InfobloxNetworkViewNotFoundReason indicates that the specified network view could not be found on the Infoblox instance.
	InfobloxNetworkViewNotFoundReason = "InfobloxNetworkViewNotFound"
	// InfobloxNetworkViewNotFoundReason indicates that the specified network view could not be found on the Infoblox instance.
	InfobloxNetworkNotFoundReason = "InfobloxNetworkNotFound"
	// InfobloxAddressAllocationFailedReason indicates that the address allocation in Infoblox failed.
	InfobloxAddressAllocationFailedReason = "InfobloxAddressAllocationFailed"

	// InfobloxInstanceNotReadyReason indicates that the referenced InfobloxInstance is not ready.
	InfobloxInstanceNotReadyReason = "InfobloxInstanceNotReady"

	// InfobloxClientCreationFailedReason indicates that the Infoblox client could not be created.
	InfobloxClientCreationFailedReason = "InfobloxClientCreationFailed"

	// AddressesAvailableCondition indicates whether there are still addresses available to allocate from a pool.
	AddressesAvailableCondition = "AddressesAavailable"
	// PoolExhaustedReason indicates that there are no more unallocated addresses in a pool.
	PoolExhaustedReason = "PoolExhausted"
)
