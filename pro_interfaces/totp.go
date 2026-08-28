package pro_interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

type TOTPEnrollmentState string

const (
	TOTPEnrollmentNone                TOTPEnrollmentState = "none"
	TOTPEnrollmentPendingConfirmation TOTPEnrollmentState = "pending_confirmation"
	TOTPEnrollmentPendingRecoveryAck  TOTPEnrollmentState = "pending_recovery_ack"
	TOTPEnrollmentActive              TOTPEnrollmentState = "active"
)

type TOTPSessionRequirement string

const (
	TOTPSessionNone      TOTPSessionRequirement = "none"
	TOTPSessionChallenge TOTPSessionRequirement = "challenge"
	TOTPSessionEnroll    TOTPSessionRequirement = "enroll"
)

type TOTPStatus struct {
	CapabilityState        CapabilityState     `json:"capability_state"`
	EnrollmentID          int                 `json:"enrollment_id,omitempty"`
	EnrollmentState        TOTPEnrollmentState `json:"enrollment_state"`
	Required               bool                `json:"required"`
	Selected               bool                `json:"selected"`
	RecoveryAcknowledged   bool                `json:"recovery_acknowledged"`
	RecoveryCodesRemaining int                 `json:"recovery_codes_remaining"`
}

type TOTPEnrollmentCeremony struct {
	ID              int       `json:"id"`
	ProvisioningURI string    `json:"provisioning_uri"`
	RecoveryCodes   []string  `json:"recovery_codes"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type TOTPEnrollmentRequest struct {
	ActorID          int
	TargetUserID     int
	Reauthentication string
	Now              time.Time
}

type TOTPConfirmationRequest struct {
	ActorID          int
	TargetUserID     int
	EnrollmentID     int
	Reauthentication string
	Passcode         string
	Now              time.Time
}

type TOTPRecoveryAcknowledgement struct {
	ActorID      int
	TargetUserID int
	EnrollmentID int
	SessionID    int
	Stored       bool
	Now          time.Time
}

type TOTPResetRequest struct {
	ActorID          int
	ActorIsAdmin     bool
	TargetUserID     int
	EnrollmentID     int
	Reauthentication string
	Now              time.Time
}

type TOTPConfigurationRequest struct {
	ActorID         int
	ActorIsAdmin    bool
	State           CapabilityState
	SelectedUserIDs []int
	Now             time.Time
}

type TOTPRolloutConfiguration struct {
	State           CapabilityState `json:"state"`
	SelectedUserIDs []int           `json:"selected_user_ids"`
}

var (
	ErrTOTPUnavailable     = errors.New("TOTP capability unavailable")
	ErrTOTPForbidden       = errors.New("TOTP operation forbidden")
	ErrTOTPInvalidPassword = errors.New("invalid current password")
	ErrTOTPInvalidCode     = errors.New("invalid TOTP passcode")
	ErrTOTPReplay          = errors.New("TOTP time step already used")
	ErrTOTPThrottled       = errors.New("TOTP attempts throttled")
	ErrTOTPInvalidRecovery = errors.New("invalid recovery code")
	ErrTOTPNotFound        = errors.New("TOTP enrollment not found")
	ErrTOTPConflict        = errors.New("TOTP enrollment conflict")
	ErrTOTPReadiness       = errors.New("TOTP required-mode readiness check failed")
)

// TOTPService is the framework-free enhanced TOTP use-case boundary.
type TOTPService interface {
	Initialize(context.Context) error
	Status(context.Context, int) (TOTPStatus, error)
	SessionRequirement(context.Context, int) (TOTPSessionRequirement, error)
	BeginEnrollment(context.Context, TOTPEnrollmentRequest) (TOTPEnrollmentCeremony, error)
	ProvisioningURI(context.Context, int, int, int) (string, error)
	ConfirmEnrollment(context.Context, TOTPConfirmationRequest) (TOTPStatus, error)
	AcknowledgeRecoveryCodes(context.Context, TOTPRecoveryAcknowledgement) (TOTPStatus, error)
	VerifyChallenge(context.Context, int, int, string, time.Time) error
	RecoverSession(context.Context, int, int, string, time.Time) error
	ResetEnrollment(context.Context, TOTPResetRequest) error
	Configure(context.Context, TOTPConfigurationRequest) (CapabilitySnapshot, error)
	Configuration(context.Context) (TOTPRolloutConfiguration, error)
	Transitions(context.Context) ([]db.TOTPCapabilityTransition, error)
}
