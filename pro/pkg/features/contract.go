package features

import "github.com/semaphoreui/semaphore/pro_interfaces"

const (
	// CompatibilityVersion must match the core contract version.
	CompatibilityVersion = pro_interfaces.CoreContractVersion
	// ImplementationVersion identifies the Community implementation of the seam.
	ImplementationVersion = "community-1"
)

// Compatibility returns build-time metadata for the active implementation.
func Compatibility() pro_interfaces.Compatibility {
	return pro_interfaces.Compatibility{
		Edition:         pro_interfaces.EditionCommunity,
		ContractVersion: CompatibilityVersion,
		Implementation:  ImplementationVersion,
	}
}
