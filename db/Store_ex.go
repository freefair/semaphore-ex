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

// LDAPRepository persists provider configuration, rollout selection,
// readiness, transitions, and authentication throttling without credentials.
type LDAPRepository interface {
	GetLDAPProvider(providerID string) (LDAPProvider, error)
	GetLDAPProviders() ([]LDAPProvider, error)
	SaveLDAPProvider(provider LDAPProvider) error
	SaveLDAPReadiness(providerID string, status string, code string, checkedAt time.Time,
		recoveryAdminUserID *int, expectedConfigVersion int) error
	ConfigureLDAPProvider(providerID string, state string, selectedUserIDs []int, actorID int, changedAt time.Time, readinessMaxAge time.Duration) error
	GetLDAPSelectedUsers(providerID string) ([]int, error)
	GetLDAPLinkedUserIDs(providerID string) ([]int, error)
	IsLDAPUserSelected(providerID string, userID int) (bool, error)
	GetLDAPCapabilityTransitions(providerID string) ([]LDAPCapabilityTransition, error)
	GetLDAPAuthAttempt(providerID string, subjectHash string) (LDAPAuthAttempt, error)
	RecordLDAPAuthFailure(providerID string, subjectHash string, now time.Time, window time.Duration, maxFailures int, blockFor time.Duration) (LDAPAuthAttempt, error)
	ClearLDAPAuthFailures(providerID string, subjectHash string) error
}
