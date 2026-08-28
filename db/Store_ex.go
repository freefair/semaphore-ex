package db

import (
	"time"
)

// TOTPRepository is the durable authority for enrollment state, one-time
// consumption, throttling, rollout selection, and transition history.
type TOTPRepository interface {
	GetTOTP(userID int) (UserTotp, error)
	GetLegacyTOTPs() ([]UserTotp, error)
	UpdateLegacyTOTPSecret(userID int, totpID int, encryptedSecret string, migratedAt time.Time) error
	CreateTOTPEnrollment(enrollment UserTotp, recoveryCodeHashes []string) (UserTotp, error)
	ConfirmTOTPEnrollment(userID int, totpID int, step int64, confirmedAt time.Time) (bool, error)
	ActivateTOTPEnrollmentAndRevokeSessions(userID int, totpID int, currentSessionID int, acknowledgedAt time.Time) (bool, error)
	ResetTOTPEnrollmentAndRevokeSessions(userID int, totpID int) error
	ForceResetTOTPEnrollmentAndRevokeSessions(userID int, totpID int) error
	GetUnusedTOTPRecoveryCodes(userID int, totpID int) ([]TOTPRecoveryCode, error)
	ConsumeTOTPRecoveryCode(userID int, totpID int, recoveryCodeID int, consumedAt time.Time) (bool, error)
	ConsumeTOTPStep(userID int, totpID int, step int64) (bool, error)
	GetTOTPAttempt(userID int) (TOTPAttempt, error)
	RecordTOTPFailure(userID int, now time.Time, window time.Duration, maxFailures int, blockFor time.Duration) (TOTPAttempt, error)
	ClearTOTPFailures(userID int) error
	IsTOTPUserSelected(userID int) (bool, error)
	GetTOTPSelectedUsers() ([]int, error)
	ConfigureTOTP(state string, selectedUserIDs []int, actorID int, changedAt time.Time) error
	GetTOTPCapabilityTransitions() ([]TOTPCapabilityTransition, error)
	CountRecoverableTOTPAdmins(excludeUserID int) (int, error)
}
