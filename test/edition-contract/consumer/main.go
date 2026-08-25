package main

import (
	"fmt"

	"github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func main() {
	compatibility := features.Compatibility()
	if compatibility.ContractVersion != pro_interfaces.CoreContractVersion {
		panic("enhanced contract version does not match core")
	}
	if !features.GetFeatures(nil, "").TaskSummary {
		panic("clean-room enhanced capability is not active")
	}
	fmt.Print(compatibility.Implementation)
}
