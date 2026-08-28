package features

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"golang.org/x/crypto/bcrypt"
)

const (
	totpPeriod             = 30 * time.Second
	totpEnrollmentLifetime = 15 * time.Minute
	totpAttemptWindow      = 5 * time.Minute
	totpAttemptBlock       = 5 * time.Minute
	totpMaxFailures        = 5
	totpRecoveryCodeCount  = 8
)

type totpService struct {
	repository db.Store
	provider   pro_interfaces.CapabilityProvider
	now        func() time.Time
}

// NewTOTPService returns the clean-room enhanced TOTP lifecycle service.
func NewTOTPService(
	repository db.Store,
	provider pro_interfaces.CapabilityProvider,
) pro_interfaces.TOTPService {
	return &totpService{repository: repository, provider: provider, now: func() time.Time { return time.Now().UTC() }}
}

func (s *totpService) Initialize(ctx context.Context) error {
	legacy, err := s.repository.GetLegacyTOTPs()
	if err != nil {
		return fmt.Errorf("load legacy TOTP enrollments: %w", err)
	}
	if _, err = s.repository.GetCapabilityConfig(string(pro_interfaces.CapabilityTOTP)); errors.Is(err, db.ErrNotFound) {
		state := pro_interfaces.CapabilityStateDisabled
		if len(legacy) > 0 {
			state = pro_interfaces.CapabilityStateOptional
		}
		err = s.repository.SaveCapabilityConfig(db.CapabilityConfig{
			CapabilityID: string(pro_interfaces.CapabilityTOTP), State: string(state), Updated: s.now(),
		})
	}
	if err != nil {
		return fmt.Errorf("initialize TOTP capability state: %w", err)
	}
	if len(legacy) > 0 && !util.Config.OptionEncryptionEnabled() {
		return errors.New("TOTP requires option or access-key encryption before startup")
	}
	for _, enrollment := range legacy {
		encrypted, encryptErr := util.Config.EncryptOption([]byte(enrollment.URL))
		if encryptErr != nil {
			return fmt.Errorf("encrypt legacy TOTP enrollment for user %d: %w", enrollment.UserID, encryptErr)
		}
		if err = s.repository.UpdateLegacyTOTPSecret(
			enrollment.UserID, enrollment.ID, encrypted, s.now(),
		); err != nil {
			return fmt.Errorf("migrate legacy TOTP enrollment for user %d: %w", enrollment.UserID, err)
		}
	}
	_, err = s.provider.Resolve(ctx, pro_interfaces.CapabilityRequest{At: s.now()})
	return err
}

func (s *totpService) Status(ctx context.Context, userID int) (pro_interfaces.TOTPStatus, error) {
	request := pro_interfaces.CapabilityRequest{UserID: userID, At: s.now()}
	snapshot, err := s.provider.Resolve(ctx, request)
	if err != nil {
		return pro_interfaces.TOTPStatus{}, err
	}
	decision := snapshot.Decision(pro_interfaces.CapabilityTOTP)
	if decision.State() == pro_interfaces.CapabilityStateUnavailable {
		return pro_interfaces.TOTPStatus{}, pro_interfaces.ErrTOTPUnavailable
	}
	selected, err := s.repository.IsTOTPUserSelected(userID)
	if err != nil {
		return pro_interfaces.TOTPStatus{}, fmt.Errorf("load TOTP rollout selection: %w", err)
	}
	status := pro_interfaces.TOTPStatus{
		CapabilityState: decision.State(),
		EnrollmentState: pro_interfaces.TOTPEnrollmentNone,
		Required: decision.State() == pro_interfaces.CapabilityStateRequired ||
			(decision.State() == pro_interfaces.CapabilityStateRequiredSelected && selected),
		Selected: selected,
	}
	enrollment, err := s.repository.GetTOTP(userID)
	if errors.Is(err, db.ErrNotFound) {
		return status, nil
	}
	if err != nil {
		return pro_interfaces.TOTPStatus{}, fmt.Errorf("load TOTP enrollment: %w", err)
	}
	status.EnrollmentState = pro_interfaces.TOTPEnrollmentState(enrollment.State)
	status.EnrollmentID = enrollment.ID
	status.RecoveryAcknowledged = enrollment.RecoveryAcknowledgedAt != nil
	codes, err := s.repository.GetUnusedTOTPRecoveryCodes(userID, enrollment.ID)
	if err != nil {
		return pro_interfaces.TOTPStatus{}, fmt.Errorf("load TOTP recovery status: %w", err)
	}
	status.RecoveryCodesRemaining = len(codes)
	return status, nil
}

func (s *totpService) SessionRequirement(
	ctx context.Context,
	userID int,
) (pro_interfaces.TOTPSessionRequirement, error) {
	status, err := s.Status(ctx, userID)
	if errors.Is(err, pro_interfaces.ErrTOTPUnavailable) {
		return pro_interfaces.TOTPSessionNone, nil
	}
	if err != nil {
		return pro_interfaces.TOTPSessionNone, err
	}
	if status.CapabilityState == pro_interfaces.CapabilityStateDisabled ||
		status.CapabilityState == pro_interfaces.CapabilityStateShadow {
		return pro_interfaces.TOTPSessionNone, nil
	}
	if status.EnrollmentState == pro_interfaces.TOTPEnrollmentActive {
		return pro_interfaces.TOTPSessionChallenge, nil
	}
	if status.Required {
		user, userErr := s.repository.GetUser(userID)
		if userErr != nil {
			return pro_interfaces.TOTPSessionNone, userErr
		}
		if user.External {
			return pro_interfaces.TOTPSessionNone, pro_interfaces.ErrTOTPForbidden
		}
		return pro_interfaces.TOTPSessionEnroll, nil
	}
	return pro_interfaces.TOTPSessionNone, nil
}

func (s *totpService) BeginEnrollment(
	ctx context.Context,
	request pro_interfaces.TOTPEnrollmentRequest,
) (pro_interfaces.TOTPEnrollmentCeremony, error) {
	if request.ActorID != request.TargetUserID {
		return pro_interfaces.TOTPEnrollmentCeremony{}, pro_interfaces.ErrTOTPForbidden
	}
	if request.Now.IsZero() {
		request.Now = s.now()
	}
	status, err := s.Status(ctx, request.TargetUserID)
	if err != nil {
		return pro_interfaces.TOTPEnrollmentCeremony{}, err
	}
	if status.CapabilityState == pro_interfaces.CapabilityStateDisabled ||
		status.CapabilityState == pro_interfaces.CapabilityStateShadow {
		return pro_interfaces.TOTPEnrollmentCeremony{}, pro_interfaces.ErrTOTPUnavailable
	}
	if status.EnrollmentState == pro_interfaces.TOTPEnrollmentActive {
		return pro_interfaces.TOTPEnrollmentCeremony{}, pro_interfaces.ErrTOTPConflict
	}
	if !util.Config.OptionEncryptionEnabled() {
		return pro_interfaces.TOTPEnrollmentCeremony{}, errors.New("TOTP enrollment requires configured encryption")
	}
	user, err := s.requireLocalPassword(request.TargetUserID, request.Reauthentication)
	if err != nil {
		return pro_interfaces.TOTPEnrollmentCeremony{}, err
	}
	issuer := ""
	if util.Config.Mfa != nil && util.Config.Mfa.Totp != nil {
		issuer = util.Config.Mfa.Totp.Issuer
	}
	if issuer == "" {
		issuer = "Semaphore"
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: user.Email})
	if err != nil {
		return pro_interfaces.TOTPEnrollmentCeremony{}, fmt.Errorf("generate TOTP key: %w", err)
	}
	uri := key.URL()
	encrypted, err := util.Config.EncryptOption([]byte(uri))
	if err != nil {
		return pro_interfaces.TOTPEnrollmentCeremony{}, fmt.Errorf("encrypt TOTP key: %w", err)
	}
	codes := make([]string, 0, totpRecoveryCodeCount)
	hashes := make([]string, 0, totpRecoveryCodeCount)
	for index := 0; index < totpRecoveryCodeCount; index++ {
		code, hash, codeErr := util.GenerateRecoveryCode()
		if codeErr != nil {
			return pro_interfaces.TOTPEnrollmentCeremony{}, fmt.Errorf("generate recovery code: %w", codeErr)
		}
		codes = append(codes, code)
		hashes = append(hashes, hash)
	}
	expiresAt := request.Now.Add(totpEnrollmentLifetime)
	enrollment, err := s.repository.CreateTOTPEnrollment(db.UserTotp{
		UserID:          request.TargetUserID,
		EncryptedSecret: encrypted,
		State:           string(pro_interfaces.TOTPEnrollmentPendingConfirmation),
		ExpiresAt:       &expiresAt,
		Created:         request.Now,
	}, hashes)
	if err != nil {
		return pro_interfaces.TOTPEnrollmentCeremony{}, fmt.Errorf("persist TOTP enrollment: %w", err)
	}
	return pro_interfaces.TOTPEnrollmentCeremony{
		ID: enrollment.ID, ProvisioningURI: uri, RecoveryCodes: codes, ExpiresAt: expiresAt,
	}, nil
}

func (s *totpService) ProvisioningURI(
	_ context.Context,
	actorID int,
	userID int,
	totpID int,
) (string, error) {
	if actorID != userID {
		return "", pro_interfaces.ErrTOTPForbidden
	}
	enrollment, err := s.repository.GetTOTP(userID)
	if err != nil || enrollment.ID != totpID {
		return "", pro_interfaces.ErrTOTPNotFound
	}
	if enrollment.State != string(pro_interfaces.TOTPEnrollmentPendingConfirmation) ||
		enrollment.ExpiresAt == nil || !s.now().Before(*enrollment.ExpiresAt) {
		return "", pro_interfaces.ErrTOTPForbidden
	}
	return s.decryptURI(enrollment)
}

func (s *totpService) ConfirmEnrollment(
	ctx context.Context,
	request pro_interfaces.TOTPConfirmationRequest,
) (pro_interfaces.TOTPStatus, error) {
	if request.ActorID != request.TargetUserID {
		return pro_interfaces.TOTPStatus{}, pro_interfaces.ErrTOTPForbidden
	}
	if request.Now.IsZero() {
		request.Now = s.now()
	}
	if _, err := s.requireLocalPassword(request.TargetUserID, request.Reauthentication); err != nil {
		return pro_interfaces.TOTPStatus{}, err
	}
	enrollment, err := s.repository.GetTOTP(request.TargetUserID)
	if err != nil || enrollment.ID != request.EnrollmentID ||
		enrollment.State != string(pro_interfaces.TOTPEnrollmentPendingConfirmation) {
		return pro_interfaces.TOTPStatus{}, pro_interfaces.ErrTOTPNotFound
	}
	secret, err := s.secret(enrollment)
	if err != nil {
		return pro_interfaces.TOTPStatus{}, err
	}
	step, valid, err := matchingTOTPStep(secret, request.Passcode, request.Now)
	if err != nil {
		return pro_interfaces.TOTPStatus{}, err
	}
	if !valid {
		return pro_interfaces.TOTPStatus{}, pro_interfaces.ErrTOTPInvalidCode
	}
	confirmed, err := s.repository.ConfirmTOTPEnrollment(
		request.TargetUserID, request.EnrollmentID, step, request.Now,
	)
	if err != nil {
		return pro_interfaces.TOTPStatus{}, fmt.Errorf("confirm TOTP enrollment: %w", err)
	}
	if !confirmed {
		return pro_interfaces.TOTPStatus{}, pro_interfaces.ErrTOTPConflict
	}
	return s.Status(ctx, request.TargetUserID)
}

func (s *totpService) AcknowledgeRecoveryCodes(
	ctx context.Context,
	request pro_interfaces.TOTPRecoveryAcknowledgement,
) (pro_interfaces.TOTPStatus, error) {
	if request.ActorID != request.TargetUserID {
		return pro_interfaces.TOTPStatus{}, pro_interfaces.ErrTOTPForbidden
	}
	if !request.Stored {
		return pro_interfaces.TOTPStatus{}, common_errors.NewValidationError("recovery code storage must be acknowledged")
	}
	if request.Now.IsZero() {
		request.Now = s.now()
	}
	activated, err := s.repository.ActivateTOTPEnrollmentAndRevokeSessions(
		request.TargetUserID, request.EnrollmentID, request.SessionID, request.Now,
	)
	if err != nil {
		return pro_interfaces.TOTPStatus{}, fmt.Errorf("activate TOTP enrollment: %w", err)
	}
	if !activated {
		return pro_interfaces.TOTPStatus{}, pro_interfaces.ErrTOTPConflict
	}
	return s.Status(ctx, request.TargetUserID)
}

func (s *totpService) VerifyChallenge(
	_ context.Context,
	userID int,
	sessionID int,
	passcode string,
	now time.Time,
) error {
	if now.IsZero() {
		now = s.now()
	}
	if err := s.checkThrottle(userID, now); err != nil {
		return err
	}
	enrollment, err := s.repository.GetTOTP(userID)
	if err != nil || enrollment.State != string(pro_interfaces.TOTPEnrollmentActive) {
		return pro_interfaces.ErrTOTPNotFound
	}
	secret, err := s.secret(enrollment)
	if err != nil {
		return err
	}
	step, valid, err := matchingTOTPStep(secret, passcode, now)
	if err != nil {
		return err
	}
	if !valid {
		return s.failedAttempt(userID, now, pro_interfaces.ErrTOTPInvalidCode)
	}
	consumed, err := s.repository.ConsumeTOTPStep(userID, enrollment.ID, step)
	if err != nil {
		return fmt.Errorf("consume TOTP time step: %w", err)
	}
	if !consumed {
		return pro_interfaces.ErrTOTPReplay
	}
	if err = s.repository.VerifySession(userID, sessionID); err != nil {
		return fmt.Errorf("verify TOTP session: %w", err)
	}
	return s.repository.ClearTOTPFailures(userID)
}

func (s *totpService) RecoverSession(
	_ context.Context,
	userID int,
	sessionID int,
	recoveryCode string,
	now time.Time,
) error {
	if now.IsZero() {
		now = s.now()
	}
	if err := s.checkThrottle(userID, now); err != nil {
		return err
	}
	enrollment, err := s.repository.GetTOTP(userID)
	if err != nil || enrollment.State != string(pro_interfaces.TOTPEnrollmentActive) {
		return pro_interfaces.ErrTOTPNotFound
	}
	codes, err := s.repository.GetUnusedTOTPRecoveryCodes(userID, enrollment.ID)
	if err != nil {
		return fmt.Errorf("load recovery codes: %w", err)
	}
	normalized := strings.ToUpper(strings.TrimSpace(recoveryCode))
	for _, code := range codes {
		if bcrypt.CompareHashAndPassword([]byte(code.CodeHash), []byte(normalized)) != nil {
			continue
		}
		consumed, consumeErr := s.repository.ConsumeTOTPRecoveryCode(
			userID, enrollment.ID, code.ID, now,
		)
		if consumeErr != nil {
			return fmt.Errorf("consume recovery code: %w", consumeErr)
		}
		if !consumed {
			return pro_interfaces.ErrTOTPInvalidRecovery
		}
		if err = s.repository.VerifySession(userID, sessionID); err != nil {
			return fmt.Errorf("verify recovered session: %w", err)
		}
		return s.repository.ClearTOTPFailures(userID)
	}
	return s.failedAttempt(userID, now, pro_interfaces.ErrTOTPInvalidRecovery)
}

func (s *totpService) ResetEnrollment(
	ctx context.Context,
	request pro_interfaces.TOTPResetRequest,
) error {
	if request.Now.IsZero() {
		request.Now = s.now()
	}
	if request.ActorID != request.TargetUserID && !request.ActorIsAdmin {
		return pro_interfaces.ErrTOTPForbidden
	}
	if request.ActorID == request.TargetUserID {
		if _, err := s.requireLocalPassword(request.TargetUserID, request.Reauthentication); err != nil {
			return err
		}
	}
	status, err := s.Status(ctx, request.TargetUserID)
	if err != nil {
		return err
	}
	user, err := s.repository.GetUser(request.TargetUserID)
	if err != nil {
		return err
	}
	if user.Admin && status.Required && status.EnrollmentState == pro_interfaces.TOTPEnrollmentActive {
		remaining, countErr := s.repository.CountRecoverableTOTPAdmins(request.TargetUserID)
		if countErr != nil {
			return fmt.Errorf("check recoverable administrators: %w", countErr)
		}
		if remaining == 0 {
			return pro_interfaces.ErrTOTPReadiness
		}
	}
	if err = s.repository.ResetTOTPEnrollmentAndRevokeSessions(
		request.TargetUserID, request.EnrollmentID,
	); err != nil {
		if errors.Is(err, db.ErrTOTPReadiness) {
			return pro_interfaces.ErrTOTPReadiness
		}
		return fmt.Errorf("reset TOTP enrollment: %w", err)
	}
	return nil
}

func (s *totpService) Configure(
	ctx context.Context,
	request pro_interfaces.TOTPConfigurationRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	if !request.ActorIsAdmin {
		return pro_interfaces.CapabilitySnapshot{}, pro_interfaces.ErrTOTPForbidden
	}
	if request.Now.IsZero() {
		request.Now = s.now()
	}
	switch request.State {
	case pro_interfaces.CapabilityStateDisabled,
		pro_interfaces.CapabilityStateShadow,
		pro_interfaces.CapabilityStateOptional,
		pro_interfaces.CapabilityStateRequiredSelected,
		pro_interfaces.CapabilityStateRequired:
	default:
		return pro_interfaces.CapabilitySnapshot{}, common_errors.NewValidationError("unsupported TOTP capability state")
	}
	selected := uniquePositiveIDs(request.SelectedUserIDs)
	if request.State == pro_interfaces.CapabilityStateRequiredSelected && len(selected) == 0 {
		return pro_interfaces.CapabilitySnapshot{}, common_errors.NewValidationError("required-selected mode needs at least one user")
	}
	if request.State != pro_interfaces.CapabilityStateRequiredSelected {
		selected = nil
	}
	for _, userID := range selected {
		user, err := s.repository.GetUser(userID)
		if err != nil {
			return pro_interfaces.CapabilitySnapshot{}, common_errors.NewValidationError("selected TOTP user does not exist")
		}
		if user.External {
			return pro_interfaces.CapabilitySnapshot{}, common_errors.NewValidationError("external users cannot be selected for password-reauthenticated TOTP enrollment")
		}
	}
	if request.State == pro_interfaces.CapabilityStateRequired {
		users, err := s.repository.GetUsers(db.RetrieveQueryParams{})
		if err != nil {
			return pro_interfaces.CapabilitySnapshot{}, fmt.Errorf("load users for TOTP readiness: %w", err)
		}
		for _, user := range users {
			if user.External {
				return pro_interfaces.CapabilitySnapshot{}, pro_interfaces.ErrTOTPReadiness
			}
		}
		recoverableAdmins, err := s.repository.CountRecoverableTOTPAdmins(0)
		if err != nil {
			return pro_interfaces.CapabilitySnapshot{}, fmt.Errorf("check TOTP administrator recovery readiness: %w", err)
		}
		if recoverableAdmins == 0 {
			return pro_interfaces.CapabilitySnapshot{}, pro_interfaces.ErrTOTPReadiness
		}
	}
	if err := s.repository.ConfigureTOTP(
		string(request.State), selected, request.ActorID, request.Now,
	); err != nil {
		if errors.Is(err, db.ErrTOTPReadiness) {
			return pro_interfaces.CapabilitySnapshot{}, pro_interfaces.ErrTOTPReadiness
		}
		return pro_interfaces.CapabilitySnapshot{}, fmt.Errorf("configure TOTP rollout: %w", err)
	}
	return s.provider.Resolve(ctx, pro_interfaces.CapabilityRequest{
		UserID: request.ActorID, IsAdmin: true, At: request.Now,
	})
}

func (s *totpService) Configuration(context.Context) (pro_interfaces.TOTPRolloutConfiguration, error) {
	config, err := s.repository.GetCapabilityConfig(string(pro_interfaces.CapabilityTOTP))
	if errors.Is(err, db.ErrNotFound) {
		config.State = string(pro_interfaces.CapabilityStateDisabled)
	} else if err != nil {
		return pro_interfaces.TOTPRolloutConfiguration{}, err
	}
	selected, err := s.repository.GetTOTPSelectedUsers()
	if err != nil {
		return pro_interfaces.TOTPRolloutConfiguration{}, err
	}
	return pro_interfaces.TOTPRolloutConfiguration{
		State: pro_interfaces.CapabilityState(config.State), SelectedUserIDs: selected,
	}, nil
}

func (s *totpService) Transitions(context.Context) ([]db.TOTPCapabilityTransition, error) {
	return s.repository.GetTOTPCapabilityTransitions()
}

func (s *totpService) requireLocalPassword(userID int, password string) (db.User, error) {
	user, err := s.repository.GetUser(userID)
	if err != nil {
		return db.User{}, err
	}
	if user.External {
		return db.User{}, pro_interfaces.ErrTOTPForbidden
	}
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)) != nil {
		return db.User{}, pro_interfaces.ErrTOTPInvalidPassword
	}
	return user, nil
}

func (s *totpService) decryptURI(enrollment db.UserTotp) (string, error) {
	plaintext, err := util.Config.DecryptOption(enrollment.EncryptedSecret)
	if err != nil {
		return "", fmt.Errorf("decrypt TOTP key: %w", err)
	}
	uri := string(plaintext)
	for index := range plaintext {
		plaintext[index] = 0
	}
	return uri, nil
}

func (s *totpService) secret(enrollment db.UserTotp) (string, error) {
	uri, err := s.decryptURI(enrollment)
	if err != nil {
		return "", err
	}
	key, err := otp.NewKeyFromURL(uri)
	if err != nil {
		return "", fmt.Errorf("parse TOTP key: %w", err)
	}
	return key.Secret(), nil
}

func (s *totpService) checkThrottle(userID int, now time.Time) error {
	attempt, err := s.repository.GetTOTPAttempt(userID)
	if errors.Is(err, db.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load TOTP throttle state: %w", err)
	}
	if attempt.BlockedUntil != nil && now.Before(*attempt.BlockedUntil) {
		return pro_interfaces.ErrTOTPThrottled
	}
	return nil
}

func (s *totpService) failedAttempt(userID int, now time.Time, invalid error) error {
	attempt, err := s.repository.RecordTOTPFailure(
		userID, now, totpAttemptWindow, totpMaxFailures, totpAttemptBlock,
	)
	if err != nil {
		return fmt.Errorf("record TOTP failure: %w", err)
	}
	if attempt.BlockedUntil != nil && now.Before(*attempt.BlockedUntil) {
		return pro_interfaces.ErrTOTPThrottled
	}
	return invalid
}

func matchingTOTPStep(secret string, passcode string, now time.Time) (int64, bool, error) {
	if len(passcode) != 6 {
		return 0, false, nil
	}
	current := now.Unix() / int64(totpPeriod/time.Second)
	for _, step := range []int64{current, current - 1, current + 1} {
		candidate, err := totp.GenerateCodeCustom(secret, time.Unix(step*30, 0).UTC(), totp.ValidateOpts{
			Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, false, fmt.Errorf("generate TOTP verification candidate: %w", err)
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(passcode)) == 1 {
			return step, true, nil
		}
	}
	return 0, false, nil
}

func uniquePositiveIDs(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

var _ pro_interfaces.TOTPService = (*totpService)(nil)
