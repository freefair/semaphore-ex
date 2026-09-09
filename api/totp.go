package api

import (
	"bytes"
	"errors"
	"image/png"
	"net/http"

	"github.com/pquerna/otp"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/tz"
	proApi "github.com/semaphoreui/semaphore/pro/api"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

type TOTPController struct {
	service pro_interfaces.TOTPService
	audit   pro_interfaces.AuditServiceFacade
}

func NewTOTPController(
	service pro_interfaces.TOTPService,
	audit pro_interfaces.AuditServiceFacade,
) *TOTPController {
	return &TOTPController{service: service, audit: audit}
}

func (c *TOTPController) Status(w http.ResponseWriter, r *http.Request) {
	_, target, ok := authenticatedTOTPUsers(w, r)
	if !ok {
		return
	}
	status, err := c.service.Status(r.Context(), target.ID)
	if err != nil {
		writeTOTPError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, status)
}

func (c *TOTPController) BeginEnrollment(w http.ResponseWriter, r *http.Request) {
	actor, target, ok := authenticatedTOTPUsers(w, r)
	if !ok {
		return
	}
	c.begin(w, r, actor.ID, target.ID)
}

func (c *TOTPController) BeginSessionEnrollment(w http.ResponseWriter, r *http.Request) {
	session, ok := enrollmentSession(w, r)
	if !ok {
		return
	}
	c.begin(w, r, session.UserID, session.UserID)
}

func (c *TOTPController) begin(w http.ResponseWriter, r *http.Request, actorID int, userID int) {
	var body struct {
		Reauthentication string `json:"reauthentication" binding:"required"`
	}
	if !helpers.Bind(w, r, &body) {
		c.record(r, actorID, pro_interfaces.AuditActionTOTPEnrollBegin,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput)
		return
	}
	ceremony, err := c.service.BeginEnrollment(r.Context(), pro_interfaces.TOTPEnrollmentRequest{
		ActorID: actorID, TargetUserID: userID,
		Reauthentication: body.Reauthentication, Now: tz.Now(),
	})
	if err != nil {
		c.recordError(r, actorID, pro_interfaces.AuditActionTOTPEnrollBegin, err)
		writeTOTPError(w, err)
		return
	}
	c.record(r, actorID, pro_interfaces.AuditActionTOTPEnrollBegin,
		pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonEnrollmentPending)
	helpers.WriteJSON(w, http.StatusCreated, ceremony)
}

func (c *TOTPController) QR(w http.ResponseWriter, r *http.Request) {
	actor, target, ok := authenticatedTOTPUsers(w, r)
	if !ok {
		return
	}
	c.qr(w, r, actor.ID, target.ID)
}

func (c *TOTPController) SessionQR(w http.ResponseWriter, r *http.Request) {
	session, ok := enrollmentSession(w, r)
	if !ok {
		return
	}
	c.qr(w, r, session.UserID, session.UserID)
}

func (c *TOTPController) qr(w http.ResponseWriter, r *http.Request, actorID int, userID int) {
	totpID, ok := helpers.GetIntParamOrAbort("totp_id", w, r)
	if !ok {
		return
	}
	uri, err := c.service.ProvisioningURI(r.Context(), actorID, userID, totpID)
	if err != nil {
		writeTOTPError(w, err)
		return
	}
	key, err := otp.NewKeyFromURL(uri)
	if err != nil {
		writeTOTPError(w, err)
		return
	}
	image, err := key.Image(256, 256)
	if err != nil {
		writeTOTPError(w, err)
		return
	}
	var buffer bytes.Buffer
	if err = png.Encode(&buffer, image); err != nil {
		writeTOTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(buffer.Bytes())
}

func (c *TOTPController) ConfirmEnrollment(w http.ResponseWriter, r *http.Request) {
	actor, target, ok := authenticatedTOTPUsers(w, r)
	if !ok {
		return
	}
	c.confirm(w, r, actor.ID, target.ID)
}

func (c *TOTPController) ConfirmSessionEnrollment(w http.ResponseWriter, r *http.Request) {
	session, ok := enrollmentSession(w, r)
	if !ok {
		return
	}
	c.confirm(w, r, session.UserID, session.UserID)
}

func (c *TOTPController) confirm(w http.ResponseWriter, r *http.Request, actorID int, userID int) {
	var body struct {
		Reauthentication string `json:"reauthentication" binding:"required"`
		Passcode         string `json:"passcode" binding:"required"`
	}
	if !helpers.Bind(w, r, &body) {
		c.record(r, actorID, pro_interfaces.AuditActionTOTPEnrollConfirm,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput)
		return
	}
	totpID, ok := helpers.GetIntParamOrAbort("totp_id", w, r)
	if !ok {
		return
	}
	status, err := c.service.ConfirmEnrollment(r.Context(), pro_interfaces.TOTPConfirmationRequest{
		ActorID: actorID, TargetUserID: userID, EnrollmentID: totpID,
		Reauthentication: body.Reauthentication, Passcode: body.Passcode, Now: tz.Now(),
	})
	if err != nil {
		c.recordError(r, actorID, pro_interfaces.AuditActionTOTPEnrollConfirm, err)
		writeTOTPError(w, err)
		return
	}
	c.record(r, actorID, pro_interfaces.AuditActionTOTPEnrollConfirm,
		pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonEnrollmentPending)
	helpers.WriteJSON(w, http.StatusOK, status)
}

func (c *TOTPController) AcknowledgeRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	actor, target, ok := authenticatedTOTPUsers(w, r)
	if !ok {
		return
	}
	session, found := getSession(r)
	if !found || session.UserID != actor.ID {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	c.acknowledge(w, r, actor.ID, target.ID, session.ID)
}

func (c *TOTPController) AcknowledgeSessionRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	session, ok := enrollmentSession(w, r)
	if !ok {
		return
	}
	c.acknowledge(w, r, session.UserID, session.UserID, session.ID)
}

func (c *TOTPController) acknowledge(
	w http.ResponseWriter,
	r *http.Request,
	actorID int,
	userID int,
	sessionID int,
) {
	var body struct {
		Stored bool `json:"stored"`
	}
	if !helpers.Bind(w, r, &body) {
		c.record(r, actorID, pro_interfaces.AuditActionTOTPRecoveryAck,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput)
		return
	}
	totpID, ok := helpers.GetIntParamOrAbort("totp_id", w, r)
	if !ok {
		return
	}
	status, err := c.service.AcknowledgeRecoveryCodes(
		r.Context(), pro_interfaces.TOTPRecoveryAcknowledgement{
			ActorID: actorID, TargetUserID: userID, EnrollmentID: totpID,
			SessionID: sessionID, Stored: body.Stored, Now: tz.Now(),
		},
	)
	if err != nil {
		c.recordError(r, actorID, pro_interfaces.AuditActionTOTPRecoveryAck, err)
		writeTOTPError(w, err)
		return
	}
	c.record(r, actorID, pro_interfaces.AuditActionTOTPRecoveryAck,
		pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonEnrollmentActive)
	helpers.WriteJSON(w, http.StatusOK, status)
}

func (c *TOTPController) Reset(w http.ResponseWriter, r *http.Request) {
	actor, target, ok := authenticatedTOTPUsers(w, r)
	if !ok {
		return
	}
	var body struct {
		Reauthentication string `json:"reauthentication"`
	}
	if r.Body != nil && r.ContentLength != 0 && !helpers.Bind(w, r, &body) {
		return
	}
	totpID, ok := helpers.GetIntParamOrAbort("totp_id", w, r)
	if !ok {
		return
	}
	err := c.service.ResetEnrollment(r.Context(), pro_interfaces.TOTPResetRequest{
		ActorID: actor.ID, ActorIsAdmin: actor.Admin, TargetUserID: target.ID,
		EnrollmentID: totpID, Reauthentication: body.Reauthentication, Now: tz.Now(),
	})
	if err != nil {
		c.recordError(r, actor.ID, pro_interfaces.AuditActionTOTPReset, err)
		writeTOTPError(w, err)
		return
	}
	c.record(r, actor.ID, pro_interfaces.AuditActionTOTPReset,
		pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonReset)
	w.WriteHeader(http.StatusNoContent)
}

func (c *TOTPController) VerifySession(w http.ResponseWriter, r *http.Request) {
	session, ok := getSession(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch session.VerificationMethod {
	case db.SessionVerificationEmail:
		proApi.VerifySessionByEmail(session, w, r)
	case db.SessionVerificationTotp:
		var body totpRequestBody
		if !helpers.Bind(w, r, &body) {
			return
		}
		err := c.service.VerifyChallenge(r.Context(), session.UserID, session.ID, body.Passcode, tz.Now())
		if err != nil {
			c.recordError(r, session.UserID, pro_interfaces.AuditActionTOTPChallenge, err)
			writeTOTPError(w, err)
			return
		}
		c.record(r, session.UserID, pro_interfaces.AuditActionTOTPChallenge,
			pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive))
		w.WriteHeader(http.StatusNoContent)
	case db.SessionVerificationNone:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusForbidden)
	}
}

func (c *TOTPController) RecoverSession(w http.ResponseWriter, r *http.Request) {
	session, ok := getSession(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if session.VerificationMethod != db.SessionVerificationTotp {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var body totpRecoveryRequestBody
	if !helpers.Bind(w, r, &body) {
		return
	}
	err := c.service.RecoverSession(r.Context(), session.UserID, session.ID, body.RecoveryCode, tz.Now())
	if err != nil {
		c.recordError(r, session.UserID, pro_interfaces.AuditActionTOTPRecover, err)
		writeTOTPError(w, err)
		return
	}
	c.record(r, session.UserID, pro_interfaces.AuditActionTOTPRecover,
		pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonRecoveryUsed)
	w.WriteHeader(http.StatusNoContent)
}

func (c *TOTPController) Configure(w http.ResponseWriter, r *http.Request) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	var body struct {
		State           pro_interfaces.CapabilityState `json:"state" binding:"required"`
		SelectedUserIDs []int                          `json:"selected_user_ids"`
	}
	if !helpers.Bind(w, r, &body) {
		c.record(r, actor.ID, pro_interfaces.AuditActionTOTPRollout,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput)
		return
	}
	snapshot, err := c.service.Configure(r.Context(), pro_interfaces.TOTPConfigurationRequest{
		ActorID: actor.ID, ActorIsAdmin: actor.Admin, State: body.State,
		SelectedUserIDs: body.SelectedUserIDs, Now: tz.Now(),
	})
	if err != nil {
		c.recordError(r, actor.ID, pro_interfaces.AuditActionTOTPRollout, err)
		writeTOTPError(w, err)
		return
	}
	c.record(r, actor.ID, pro_interfaces.AuditActionTOTPRollout,
		pro_interfaces.AuditOutcomeAllowed, string(snapshot.Decision(pro_interfaces.CapabilityTOTP).Reason()))
	helpers.WriteJSON(w, http.StatusOK, snapshot)
}

func (c *TOTPController) Configuration(w http.ResponseWriter, r *http.Request) {
	configuration, err := c.service.Configuration(r.Context())
	if err != nil {
		writeTOTPError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, configuration)
}

func (c *TOTPController) Transitions(w http.ResponseWriter, r *http.Request) {
	transitions, err := c.service.Transitions(r.Context())
	if err != nil {
		writeTOTPError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, transitions)
}

func authenticatedTOTPUsers(w http.ResponseWriter, r *http.Request) (*db.User, db.User, bool) {
	actor, ok := helpers.GetFromContext(r, "user").(*db.User)
	if !ok || actor == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return nil, db.User{}, false
	}
	target, ok := helpers.GetFromContext(r, "_user").(db.User)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return nil, db.User{}, false
	}
	return actor, target, true
}

func enrollmentSession(w http.ResponseWriter, r *http.Request) (*db.Session, bool) {
	session, ok := getSession(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return nil, false
	}
	if session.VerificationMethod != db.SessionVerificationTotpEnrollment || session.Verified {
		w.WriteHeader(http.StatusForbidden)
		return nil, false
	}
	return session, true
}

func (c *TOTPController) recordError(
	r *http.Request,
	actorID int,
	action pro_interfaces.AuditAction,
	err error,
) {
	outcome := pro_interfaces.AuditOutcomeFailure
	reason := pro_interfaces.AuditReasonOperationError
	switch {
	case errors.Is(err, pro_interfaces.ErrTOTPInvalidCode), errors.Is(err, pro_interfaces.ErrTOTPInvalidRecovery):
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = pro_interfaces.AuditReasonInvalidCode
	case errors.Is(err, pro_interfaces.ErrTOTPReplay):
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = pro_interfaces.AuditReasonReplay
	case errors.Is(err, pro_interfaces.ErrTOTPThrottled):
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = pro_interfaces.AuditReasonThrottled
	case errors.Is(err, pro_interfaces.ErrTOTPForbidden), errors.Is(err, pro_interfaces.ErrTOTPInvalidPassword):
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = string(pro_interfaces.CapabilityReasonInsufficientPermission)
	case errors.Is(err, pro_interfaces.ErrTOTPReadiness):
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = pro_interfaces.AuditReasonReadiness
	}
	c.record(r, actorID, action, outcome, reason)
}

func (c *TOTPController) record(
	r *http.Request,
	actorID int,
	action pro_interfaces.AuditAction,
	outcome pro_interfaces.AuditOutcome,
	reason string,
) {
	if c.audit == nil {
		return
	}
	event := capabilityAuditEvent(r, action, outcome, reason)
	event.ActorID = &actorID
	event.TargetID = string(pro_interfaces.CapabilityTOTP)
	if err := c.audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to store TOTP security audit event")
	}
}

func writeTOTPError(w http.ResponseWriter, err error) {
	var validationError *common_errors.ValidationError
	switch {
	case errors.As(err, &validationError):
		helpers.WriteErrorStatus(w, validationError.Error(), http.StatusBadRequest)
	case errors.Is(err, pro_interfaces.ErrTOTPUnavailable):
		helpers.WriteErrorStatus(w, "TOTP_UNAVAILABLE", http.StatusForbidden)
	case errors.Is(err, pro_interfaces.ErrTOTPForbidden):
		helpers.WriteErrorStatus(w, "TOTP_FORBIDDEN", http.StatusForbidden)
	case errors.Is(err, pro_interfaces.ErrTOTPInvalidPassword):
		helpers.WriteErrorStatus(w, "INVALID_REAUTHENTICATION", http.StatusUnauthorized)
	case errors.Is(err, pro_interfaces.ErrTOTPInvalidCode):
		helpers.WriteErrorStatus(w, "INVALID_PASSCODE", http.StatusUnauthorized)
	case errors.Is(err, pro_interfaces.ErrTOTPInvalidRecovery):
		helpers.WriteErrorStatus(w, "INVALID_RECOVERY_CODE", http.StatusUnauthorized)
	case errors.Is(err, pro_interfaces.ErrTOTPThrottled):
		w.Header().Set("Retry-After", "300")
		helpers.WriteErrorStatus(w, "TOTP_THROTTLED", http.StatusTooManyRequests)
	case errors.Is(err, pro_interfaces.ErrTOTPReplay):
		helpers.WriteErrorStatus(w, "TOTP_REPLAYED", http.StatusConflict)
	case errors.Is(err, pro_interfaces.ErrTOTPReadiness):
		helpers.WriteErrorStatus(w, "TOTP_ADMIN_RECOVERY_NOT_READY", http.StatusConflict)
	case errors.Is(err, pro_interfaces.ErrTOTPConflict):
		helpers.WriteErrorStatus(w, "TOTP_ENROLLMENT_CONFLICT", http.StatusConflict)
	case errors.Is(err, pro_interfaces.ErrTOTPNotFound), errors.Is(err, db.ErrNotFound):
		helpers.WriteErrorStatus(w, "TOTP_NOT_FOUND", http.StatusNotFound)
	default:
		log.WithError(err).Error("TOTP operation failed")
		helpers.WriteErrorStatus(w, "TOTP_OPERATION_FAILED", http.StatusInternalServerError)
	}
}
