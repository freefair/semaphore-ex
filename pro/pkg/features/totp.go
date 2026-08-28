package features

import (
	"context"
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type communityTOTPService struct {
	repository db.Store
}

// NewTOTPService returns the Community-safe unavailable implementation.
func NewTOTPService(repository db.Store, _ pro_interfaces.CapabilityProvider) pro_interfaces.TOTPService {
	return &communityTOTPService{repository: repository}
}

func (*communityTOTPService) Initialize(context.Context) error { return nil }

func (*communityTOTPService) Status(context.Context, int) (pro_interfaces.TOTPStatus, error) {
	return pro_interfaces.TOTPStatus{}, pro_interfaces.ErrTOTPUnavailable
}

func (s *communityTOTPService) SessionRequirement(
	_ context.Context,
	userID int,
) (pro_interfaces.TOTPSessionRequirement, error) {
	_, err := s.repository.GetTOTP(userID)
	if errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.TOTPSessionNone, nil
	}
	if err != nil {
		return pro_interfaces.TOTPSessionNone, err
	}
	// Community cannot verify any persisted TOTP enrollment. Refuse to issue
	// a password-only session regardless of the legacy rollout switch.
	return pro_interfaces.TOTPSessionNone, pro_interfaces.ErrTOTPUnavailable
}

func (*communityTOTPService) BeginEnrollment(context.Context, pro_interfaces.TOTPEnrollmentRequest) (pro_interfaces.TOTPEnrollmentCeremony, error) {
	return pro_interfaces.TOTPEnrollmentCeremony{}, pro_interfaces.ErrTOTPUnavailable
}

func (*communityTOTPService) ProvisioningURI(context.Context, int, int, int) (string, error) {
	return "", pro_interfaces.ErrTOTPUnavailable
}

func (*communityTOTPService) ConfirmEnrollment(context.Context, pro_interfaces.TOTPConfirmationRequest) (pro_interfaces.TOTPStatus, error) {
	return pro_interfaces.TOTPStatus{}, pro_interfaces.ErrTOTPUnavailable
}

func (*communityTOTPService) AcknowledgeRecoveryCodes(context.Context, pro_interfaces.TOTPRecoveryAcknowledgement) (pro_interfaces.TOTPStatus, error) {
	return pro_interfaces.TOTPStatus{}, pro_interfaces.ErrTOTPUnavailable
}

func (*communityTOTPService) VerifyChallenge(context.Context, int, int, string, time.Time) error {
	return pro_interfaces.ErrTOTPUnavailable
}

func (*communityTOTPService) RecoverSession(context.Context, int, int, string, time.Time) error {
	return pro_interfaces.ErrTOTPUnavailable
}

func (*communityTOTPService) ResetEnrollment(context.Context, pro_interfaces.TOTPResetRequest) error {
	return pro_interfaces.ErrTOTPUnavailable
}

func (*communityTOTPService) Configure(context.Context, pro_interfaces.TOTPConfigurationRequest) (pro_interfaces.CapabilitySnapshot, error) {
	return pro_interfaces.CapabilitySnapshot{}, pro_interfaces.ErrTOTPUnavailable
}

func (*communityTOTPService) Configuration(context.Context) (pro_interfaces.TOTPRolloutConfiguration, error) {
	return pro_interfaces.TOTPRolloutConfiguration{}, pro_interfaces.ErrTOTPUnavailable
}

func (*communityTOTPService) Transitions(context.Context) ([]db.TOTPCapabilityTransition, error) {
	return nil, pro_interfaces.ErrTOTPUnavailable
}

var _ pro_interfaces.TOTPService = (*communityTOTPService)(nil)
