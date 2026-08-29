package pro_interfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// CapabilityID identifies one backend-authoritative capability.
type CapabilityID string

const (
	// CapabilityLifecycleTest exercises activation and downgrade behavior without
	// coupling the contract to a product capability.
	CapabilityLifecycleTest CapabilityID = "lifecycle_test"
	// CapabilityProjectRunners gates project-scoped runner inventory and registration.
	CapabilityProjectRunners CapabilityID = "project_runners"
	// CapabilityTOTP gates the enhanced TOTP lifecycle and rollout policy.
	CapabilityTOTP CapabilityID = "totp"
	// CapabilityLDAP gates the enhanced LDAP identity lifecycle.
	CapabilityLDAP CapabilityID = "ldap"
	// CapabilityWorkflowTriggers gates trigger management and every background
	// or credential-authenticated workflow-start entry point.
	CapabilityWorkflowTriggers CapabilityID = "workflow_triggers"
	// CapabilityProjectRoles gates project-scoped custom role management.
	CapabilityProjectRoles CapabilityID = "project_roles"
)

// LimitID identifies one numeric limit in a capability decision.
type LimitID string

const (
	LimitLifecycleTestRecords     LimitID = "lifecycle_test_records"
	LimitLifecycleTestRecordBytes LimitID = "lifecycle_test_record_bytes"
)

// CapabilityState is the effective, user-specific lifecycle state.
type CapabilityState string

const (
	CapabilityStateActive                 CapabilityState = "active"
	CapabilityStateUnavailable            CapabilityState = "unavailable"
	CapabilityStateDisabled               CapabilityState = "disabled"
	CapabilityStateExpired                CapabilityState = "expired"
	CapabilityStateReadOnly               CapabilityState = "read_only"
	CapabilityStateInsufficientPermission CapabilityState = "insufficient_permission"
	CapabilityStateShadow                 CapabilityState = "shadow"
	CapabilityStateOptional               CapabilityState = "optional"
	CapabilityStateRequiredSelected       CapabilityState = "required_selected"
	CapabilityStateRequired               CapabilityState = "required"
)

// CapabilityReasonCode is a stable machine-readable explanation of a state.
type CapabilityReasonCode string

const (
	CapabilityReasonActive                 CapabilityReasonCode = "active"
	CapabilityReasonProviderUnavailable    CapabilityReasonCode = "provider_unavailable"
	CapabilityReasonDisabledByAdmin        CapabilityReasonCode = "disabled_by_admin"
	CapabilityReasonEntitlementExpired     CapabilityReasonCode = "entitlement_expired"
	CapabilityReasonReadOnly               CapabilityReasonCode = "read_only"
	CapabilityReasonInsufficientPermission CapabilityReasonCode = "insufficient_permission"
	CapabilityReasonShadow                 CapabilityReasonCode = "shadow"
	CapabilityReasonOptional               CapabilityReasonCode = "optional"
	CapabilityReasonRequiredSelected       CapabilityReasonCode = "required_selected"
	CapabilityReasonRequired               CapabilityReasonCode = "required"
)

// CapabilityAccess is an operation that a decision may permit.
type CapabilityAccess string

const (
	CapabilityAccessRead    CapabilityAccess = "read"
	CapabilityAccessWrite   CapabilityAccess = "write"
	CapabilityAccessExecute CapabilityAccess = "execute"
)

// CapabilityRequest captures the immutable inputs used for one resolution.
type CapabilityRequest struct {
	UserID  int
	IsAdmin bool
	At      time.Time
}

// CapabilityConfiguration is non-secret internal configuration persisted by a provider.
type CapabilityConfiguration struct {
	ID        CapabilityID    `json:"id"`
	State     CapabilityState `json:"state"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
}

// CapabilityDecision is immutable after construction. Access and limits are
// copied on input and output so one request cannot mutate another snapshot.
type CapabilityDecision struct {
	id     CapabilityID
	state  CapabilityState
	reason CapabilityReasonCode
	access map[CapabilityAccess]struct{}
	limits map[LimitID]int64
}

// NewCapabilityDecision constructs an isolated capability decision.
func NewCapabilityDecision(
	id CapabilityID,
	state CapabilityState,
	reason CapabilityReasonCode,
	access []CapabilityAccess,
	limits map[LimitID]int64,
) CapabilityDecision {
	decision := CapabilityDecision{
		id:     id,
		state:  state,
		reason: reason,
		access: make(map[CapabilityAccess]struct{}, len(access)),
		limits: make(map[LimitID]int64, len(limits)),
	}
	for _, current := range access {
		decision.access[current] = struct{}{}
	}
	for limitID, value := range limits {
		decision.limits[limitID] = value
	}
	return decision
}

func (d CapabilityDecision) clone() CapabilityDecision {
	access := make([]CapabilityAccess, 0, len(d.access))
	for current := range d.access {
		access = append(access, current)
	}
	return NewCapabilityDecision(d.id, d.state, d.reason, access, d.limits)
}

// ID returns the typed capability identifier.
func (d CapabilityDecision) ID() CapabilityID {
	return d.id
}

// State returns the effective lifecycle state.
func (d CapabilityDecision) State() CapabilityState {
	return d.state
}

// Reason returns the stable explanation for the effective state.
func (d CapabilityDecision) Reason() CapabilityReasonCode {
	return d.reason
}

// Allows reports whether the immutable decision permits an operation.
func (d CapabilityDecision) Allows(access CapabilityAccess) bool {
	_, ok := d.access[access]
	return ok
}

// Limit returns a copied numeric limit by typed identifier.
func (d CapabilityDecision) Limit(id LimitID) (int64, bool) {
	value, ok := d.limits[id]
	return value, ok
}

// MarshalJSON exposes only sanitized decision fields.
func (d CapabilityDecision) MarshalJSON() ([]byte, error) {
	access := make([]CapabilityAccess, 0, len(d.access))
	for current := range d.access {
		access = append(access, current)
	}
	sort.Slice(access, func(i, j int) bool { return access[i] < access[j] })
	limits := make(map[LimitID]int64, len(d.limits))
	for id, value := range d.limits {
		limits[id] = value
	}
	return json.Marshal(struct {
		ID     CapabilityID         `json:"id"`
		State  CapabilityState      `json:"state"`
		Reason CapabilityReasonCode `json:"reason"`
		Access []CapabilityAccess   `json:"access"`
		Limits map[LimitID]int64    `json:"limits"`
	}{
		ID:     d.id,
		State:  d.state,
		Reason: d.reason,
		Access: access,
		Limits: limits,
	})
}

// CapabilitySnapshot is the effective decision set for one request.
type CapabilitySnapshot struct {
	request   CapabilityRequest
	decisions map[CapabilityID]CapabilityDecision
}

// NewCapabilitySnapshot copies every decision into an isolated request snapshot.
func NewCapabilitySnapshot(request CapabilityRequest, decisions []CapabilityDecision) CapabilitySnapshot {
	snapshot := CapabilitySnapshot{
		request:   request,
		decisions: make(map[CapabilityID]CapabilityDecision, len(decisions)),
	}
	for _, decision := range decisions {
		snapshot.decisions[decision.ID()] = decision.clone()
	}
	return snapshot
}

// Request returns the scalar inputs used to resolve the snapshot.
func (s CapabilitySnapshot) Request() CapabilityRequest {
	return s.request
}

// Decision returns an isolated decision or an unavailable default.
func (s CapabilitySnapshot) Decision(id CapabilityID) CapabilityDecision {
	decision, ok := s.decisions[id]
	if !ok {
		return NewCapabilityDecision(
			id,
			CapabilityStateUnavailable,
			CapabilityReasonProviderUnavailable,
			nil,
			nil,
		)
	}
	return decision.clone()
}

// Decisions returns a stable, isolated copy sorted by capability ID.
func (s CapabilitySnapshot) Decisions() []CapabilityDecision {
	decisions := make([]CapabilityDecision, 0, len(s.decisions))
	for _, decision := range s.decisions {
		decisions = append(decisions, decision.clone())
	}
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].ID() < decisions[j].ID() })
	return decisions
}

// MarshalJSON exposes only the resolution instant and sanitized decisions.
func (s CapabilitySnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ResolvedAt   time.Time            `json:"resolved_at"`
		Capabilities []CapabilityDecision `json:"capabilities"`
	}{
		ResolvedAt:   s.request.At,
		Capabilities: s.Decisions(),
	})
}

// CapabilityProvider resolves and updates backend-authoritative configuration.
type CapabilityProvider interface {
	Resolve(context.Context, CapabilityRequest) (CapabilitySnapshot, error)
	Configure(context.Context, CapabilityRequest, CapabilityConfiguration) (CapabilitySnapshot, error)
}

// CapabilityTestService protects both request and background entry points with
// a snapshot resolved by the provider at the operation boundary.
type CapabilityTestService interface {
	ListRecords(context.Context, CapabilitySnapshot) ([]db.CapabilityTestRecord, error)
	CreateRecord(context.Context, CapabilitySnapshot, string) (db.CapabilityTestRecord, error)
	RunBackgroundAction(context.Context, CapabilitySnapshot, string) (db.CapabilityTestRecord, error)
}

// CapabilityTestRecordDTO is the transport-safe lifecycle-test representation.
type CapabilityTestRecordDTO struct {
	ID      int       `json:"id"`
	Value   string    `json:"value"`
	Source  string    `json:"source"`
	Created time.Time `json:"created"`
}

// CapabilityServiceFacade is the controller boundary for capability use cases.
// Persistence entities remain below this interface.
type CapabilityServiceFacade interface {
	Resolve(context.Context, CapabilityRequest) (CapabilitySnapshot, error)
	Configure(context.Context, CapabilityRequest, CapabilityConfiguration) (CapabilitySnapshot, error)
	ListRecords(context.Context, CapabilitySnapshot) ([]CapabilityTestRecordDTO, error)
	CreateRecord(context.Context, CapabilitySnapshot, string) (CapabilityTestRecordDTO, error)
	RunBackgroundAction(context.Context, CapabilitySnapshot, string) (CapabilityTestRecordDTO, error)
}

// CapabilityDeniedError identifies a rejected operation without protected data.
type CapabilityDeniedError struct {
	Decision CapabilityDecision
	Required CapabilityAccess
}

func (e CapabilityDeniedError) Error() string {
	return fmt.Sprintf("capability %s does not allow %s: %s", e.Decision.ID(), e.Required, e.Decision.Reason())
}

// Require returns a typed denial when an operation is not allowed.
func (s CapabilitySnapshot) Require(id CapabilityID, access CapabilityAccess) error {
	decision := s.Decision(id)
	if decision.Allows(access) {
		return nil
	}
	return CapabilityDeniedError{Decision: decision, Required: access}
}
