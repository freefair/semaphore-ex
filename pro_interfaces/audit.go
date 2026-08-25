package pro_interfaces

import (
	"context"
	"fmt"
	"regexp"
)

type AuditAction string

const (
	AuditActionCapabilityResolve   AuditAction = "capability_resolve"
	AuditActionCapabilityRead      AuditAction = "capability_read"
	AuditActionCapabilityWrite     AuditAction = "capability_write"
	AuditActionCapabilityExecute   AuditAction = "capability_execute"
	AuditActionCapabilityConfigure AuditAction = "capability_configure"
)

type AuditTargetType string

const AuditTargetCapability AuditTargetType = "capability"

type AuditOutcome string

const (
	AuditOutcomeAllowed AuditOutcome = "allowed"
	AuditOutcomeDenied  AuditOutcome = "denied"
	AuditOutcomeFailure AuditOutcome = "failure"
)

type AuditSource string

const (
	AuditSourceAPI    AuditSource = "api"
	AuditSourceWorker AuditSource = "worker"
)

const (
	AuditReasonUnauthenticated = "unauthenticated"
	AuditReasonCrossOrigin     = "cross_origin"
	AuditReasonProviderError   = "provider_error"
	AuditReasonInvalidInput    = "invalid_input"
	AuditReasonOperationError  = "operation_error"
)

type DependencyID string

const (
	DependencyAuditDatabase DependencyID = "audit_database"
	DependencyAuditFile     DependencyID = "audit_file"
)

type AuditSink string

const (
	AuditSinkDatabase AuditSink = "database"
	AuditSinkFile     AuditSink = "file"
)

type DroppedRecordReason string

const (
	DroppedRecordWriteFailure DroppedRecordReason = "write_failure"
	DroppedRecordInvalid      DroppedRecordReason = "invalid_record"
)

type QueueID string

const QueueEnhancedAudit QueueID = "enhanced_audit"

var (
	correlationPattern = regexp.MustCompile(`^(?:[a-f0-9]{32}|internal)$`)
	identifierPattern  = regexp.MustCompile(`^[a-z0-9_.:-]{1,64}$`)
)

// AuditEvent is the allowlisted payload shared by enhanced features. It has no
// field for request bodies, credentials, raw errors, or arbitrary log values.
type AuditEvent struct {
	CorrelationID string          `json:"correlation_id"`
	ActorID       *int            `json:"actor_id,omitempty"`
	Action        AuditAction     `json:"action"`
	TargetType    AuditTargetType `json:"target_type"`
	TargetID      string          `json:"target_id"`
	Outcome       AuditOutcome    `json:"outcome"`
	Source        AuditSource     `json:"source"`
	Reason        string          `json:"reason"`
}

func (e AuditEvent) Validate() error {
	if !correlationPattern.MatchString(e.CorrelationID) {
		return fmt.Errorf("invalid audit correlation ID")
	}
	if !identifierPattern.MatchString(e.TargetID) || !identifierPattern.MatchString(e.Reason) {
		return fmt.Errorf("invalid audit identifier")
	}
	if !validAuditAction(e.Action) || !validAuditTarget(e.TargetType, e.TargetID) ||
		!validAuditOutcome(e.Outcome) || !validAuditSource(e.Source) || !validAuditReason(e.Reason) {
		return fmt.Errorf("unsupported audit context")
	}
	return nil
}

func validAuditTarget(targetType AuditTargetType, targetID string) bool {
	return targetType == AuditTargetCapability && targetID == string(CapabilityLifecycleTest)
}

func validAuditReason(reason string) bool {
	switch reason {
	case AuditReasonUnauthenticated, AuditReasonCrossOrigin, AuditReasonProviderError, AuditReasonInvalidInput, AuditReasonOperationError,
		string(CapabilityReasonActive), string(CapabilityReasonProviderUnavailable),
		string(CapabilityReasonDisabledByAdmin), string(CapabilityReasonEntitlementExpired),
		string(CapabilityReasonReadOnly), string(CapabilityReasonInsufficientPermission):
		return true
	default:
		return false
	}
}

// SafeFields returns the only attributes permitted in logs and future traces.
func (e AuditEvent) SafeFields() map[string]any {
	if e.Validate() != nil {
		return map[string]any{
			"context": "enhanced_audit",
			"outcome": "dropped",
			"reason":  string(DroppedRecordInvalid),
		}
	}
	fields := map[string]any{
		"correlation_id": e.CorrelationID,
		"action":         e.Action,
		"target_type":    e.TargetType,
		"target_id":      e.TargetID,
		"outcome":        e.Outcome,
		"source":         e.Source,
		"reason":         e.Reason,
	}
	if e.ActorID != nil {
		fields["actor_id"] = *e.ActorID
	}
	return fields
}

type AuditServiceFacade interface {
	Record(context.Context, AuditEvent) error
}

func validAuditAction(action AuditAction) bool {
	switch action {
	case AuditActionCapabilityResolve, AuditActionCapabilityRead, AuditActionCapabilityWrite,
		AuditActionCapabilityExecute, AuditActionCapabilityConfigure:
		return true
	default:
		return false
	}
}

func validAuditOutcome(outcome AuditOutcome) bool {
	return outcome == AuditOutcomeAllowed || outcome == AuditOutcomeDenied || outcome == AuditOutcomeFailure
}

func validAuditSource(source AuditSource) bool {
	return source == AuditSourceAPI || source == AuditSourceWorker
}
