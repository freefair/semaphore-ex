// Package features is a clean-room test implementation of the replaceable
// enhanced-module seam. It deliberately contains no product implementation.
package features

import "github.com/semaphoreui/semaphore/pro_interfaces"

const (
	CompatibilityVersion  = pro_interfaces.CoreContractVersion
	ImplementationVersion = "clean-room-test-1"
)

func Compatibility() pro_interfaces.Compatibility {
	return pro_interfaces.Compatibility{
		Edition:         pro_interfaces.EditionEnhanced,
		ContractVersion: CompatibilityVersion,
		Implementation:  ImplementationVersion,
	}
}

func GetFeatures() pro_interfaces.Features {
	return pro_interfaces.Features{
		ProjectRunners:          true,
		TaskSummary:             true,
		SecretStorages:          true,
		SecretStorageManagement: true,
		HighAvailability:        true,
		Workflows:               true,
		CustomRolesManagement:   true,
		DockerExecutor:          true,
		K8sExecutor:             true,
	}
}
