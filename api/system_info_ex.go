package api

import (
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func totpAuthMethod(snapshot pro_interfaces.CapabilitySnapshot) *LoginTotpAuthMethod {
	switch snapshot.Decision(pro_interfaces.CapabilityTOTP).State() {
	case pro_interfaces.CapabilityStateDisabled,
		pro_interfaces.CapabilityStateShadow,
		pro_interfaces.CapabilityStateUnavailable:
		return nil
	default:
		return &LoginTotpAuthMethod{AllowRecovery: true}
	}
}
