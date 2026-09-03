package db

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	MaxWorkflowTriggers             = 64
	MaxWorkflowTriggerNameBytes     = 128
	MaxWorkflowTriggerMappings      = 64
	MaxWorkflowTriggerRequestKey    = 128
	MaxWorkflowTriggerHistoryPage   = 100
	WorkflowTriggerCredentialPrefix = "swt_"
)

var ErrWorkflowTriggerRevisionConflict = errors.New("workflow trigger revision conflict")
var ErrWorkflowTriggerStateChanged = errors.New("workflow trigger state changed")

type WorkflowTriggerType string

const (
	WorkflowTriggerManual   WorkflowTriggerType = "manual"
	WorkflowTriggerSchedule WorkflowTriggerType = "schedule"
	WorkflowTriggerAPI      WorkflowTriggerType = "api"
	WorkflowTriggerWebhook  WorkflowTriggerType = "webhook"
)

func WorkflowTriggerTypes() []WorkflowTriggerType {
	return []WorkflowTriggerType{
		WorkflowTriggerManual,
		WorkflowTriggerSchedule,
		WorkflowTriggerAPI,
		WorkflowTriggerWebhook,
	}
}

type WorkflowTriggerInputSource string

const (
	WorkflowTriggerInputFixed   WorkflowTriggerInputSource = "fixed"
	WorkflowTriggerInputRequest WorkflowTriggerInputSource = "request"
)

// WorkflowTriggerInputMapping maps either a persisted, typed value or one
// explicitly named request field to a declared workflow parameter.
type WorkflowTriggerInputMapping struct {
	Parameter string                     `json:"parameter"`
	Source    WorkflowTriggerInputSource `json:"source"`
	Key       string                     `json:"key,omitempty"`
	Value     json.RawMessage            `json:"value,omitempty"`
}

// WorkflowTrigger is a versioned start policy owned by one project workflow.
// External credentials are write-only: only CredentialHash is persisted.
type WorkflowTrigger struct {
	ID                 int                 `db:"id" json:"id" backup:"-"`
	ProjectID          int                 `db:"project_id" json:"project_id" backup:"-"`
	WorkflowTemplateID int                 `db:"workflow_template_id" json:"workflow_template_id" backup:"-"`
	Revision           int                 `db:"revision" json:"revision" backup:"revision"`
	Name               string              `db:"name" json:"name" backup:"name"`
	Type               WorkflowTriggerType `db:"type" json:"type" backup:"type"`
	OwnerUserID        int                 `db:"owner_user_id" json:"owner_user_id" backup:"owner_user_id"`
	Enabled            bool                `db:"enabled" json:"enabled" backup:"enabled"`
	CronFormat         string              `db:"cron_format" json:"cron_format,omitempty" backup:"cron_format"`

	InputMappingsJSON string                        `db:"input_mappings" json:"-" backup:"input_mappings"`
	InputMappings     []WorkflowTriggerInputMapping `db:"-" json:"input_mappings,omitempty" backup:"-"`

	CredentialHash       string `db:"credential_hash" json:"-" backup:"-"`
	CredentialGeneration int    `db:"credential_generation" json:"credential_generation,omitempty" backup:"-"`

	// Webhook signing state is independent from API trigger credentials. Ciphertext
	// is write-only and key IDs are non-secret rotation metadata.
	CurrentSigningSecretEncrypted string `db:"current_signing_secret_encrypted" json:"-" backup:"-"`
	NextSigningSecretEncrypted    string `db:"next_signing_secret_encrypted" json:"-" backup:"-"`
	CurrentSigningKeyID           string `db:"current_signing_key_id" json:"current_signing_key_id,omitempty" backup:"-"`
	NextSigningKeyID              string `db:"next_signing_key_id" json:"next_signing_key_id,omitempty" backup:"-"`
	// Generations make a retired key distinguishable from a staged key. A
	// retired next key is intentionally accepted during the overlap window but
	// must never be promoted back to current.
	CurrentSigningGeneration int `db:"current_signing_generation" json:"current_signing_generation,omitempty" backup:"-"`
	NextSigningGeneration    int `db:"next_signing_generation" json:"next_signing_generation,omitempty" backup:"-"`

	Created    time.Time  `db:"created" json:"created" backup:"created"`
	Updated    time.Time  `db:"updated" json:"updated" backup:"updated"`
	LastFired  *time.Time `db:"last_fired" json:"last_fired,omitempty" backup:"-"`
	LastResult string     `db:"last_result" json:"last_result,omitempty" backup:"-"`
}

type WorkflowTriggerInvocationStatus string

const (
	WorkflowTriggerInvocationClaimed   WorkflowTriggerInvocationStatus = "claimed"
	WorkflowTriggerInvocationRunning   WorkflowTriggerInvocationStatus = "running"
	WorkflowTriggerInvocationSucceeded WorkflowTriggerInvocationStatus = "succeeded"
	WorkflowTriggerInvocationFailed    WorkflowTriggerInvocationStatus = "failed"
	WorkflowTriggerInvocationRejected  WorkflowTriggerInvocationStatus = "rejected"
	WorkflowTriggerInvocationBlocked   WorkflowTriggerInvocationStatus = "blocked"
)

// WorkflowTriggerSnapshot identifies the exact trigger revision responsible
// for a run without retaining a credential or mutable trigger configuration.
type WorkflowTriggerSnapshot struct {
	ID                   int                 `json:"id"`
	Revision             int                 `json:"revision"`
	CredentialGeneration int                 `json:"credential_generation,omitempty"`
	Name                 string              `json:"name"`
	Type                 WorkflowTriggerType `json:"type"`
	OwnerUserID          int                 `json:"owner_user_id"`
	InvocationID         int                 `json:"invocation_id"`
	ScheduledAt          *time.Time          `json:"scheduled_at,omitempty"`
	TriggeredAt          time.Time           `json:"triggered_at"`
}

// WorkflowTriggerInvocation is the bounded audit and idempotency record for a
// single attempt to start a workflow. Request and schedule keys are hashes or
// derived identities; raw credentials and caller idempotency keys are absent.
type WorkflowTriggerInvocation struct {
	ID                         int                                  `db:"id" json:"id"`
	ProjectID                  int                                  `db:"project_id" json:"project_id"`
	WorkflowTriggerID          int                                  `db:"workflow_trigger_id" json:"workflow_trigger_id"`
	WorkflowTemplateID         int                                  `db:"workflow_template_id" json:"workflow_template_id"`
	TriggerRevision            int                                  `db:"trigger_revision" json:"trigger_revision"`
	CredentialGeneration       int                                  `db:"credential_generation" json:"credential_generation,omitempty"`
	DefinitionRevision         int                                  `db:"definition_revision" json:"definition_revision"`
	RequestKeyHash             *string                              `db:"request_key_hash" json:"-"`
	OccurrenceIdentity         *string                              `db:"occurrence_identity" json:"occurrence_identity,omitempty"`
	WebhookEventHash           *string                              `db:"webhook_event_hash" json:"-"`
	WebhookEventID             string                               `db:"webhook_event_id" json:"-"`
	WebhookKeyID               string                               `db:"webhook_key_id" json:"-"`
	WebhookSignedAt            *time.Time                           `db:"webhook_signed_at" json:"-"`
	WebhookReplayCount         int                                  `db:"webhook_replay_count" json:"-"`
	WebhookLastReplayedAt      *time.Time                           `db:"webhook_last_replayed_at" json:"-"`
	Status                     WorkflowTriggerInvocationStatus      `db:"status" json:"status"`
	RunID                      *int                                 `db:"run_id" json:"run_id,omitempty"`
	DeploymentWindowDecisionID *int                                 `db:"deployment_window_decision_id" json:"-"`
	NextEligibleAt             *time.Time                           `db:"next_eligible_at" json:"-"`
	NextEligibleKnown          bool                                 `db:"next_eligible_known" json:"-"`
	BlockedAt                  *time.Time                           `db:"blocked_at" json:"-"`
	ActorUserID                int                                  `db:"actor_user_id" json:"actor_user_id"`
	TriggerSnapshotJSON        string                               `db:"trigger_snapshot" json:"-"`
	TriggerSnapshot            WorkflowTriggerSnapshot              `db:"-" json:"trigger"`
	InputSnapshotJSON          string                               `db:"input_snapshot" json:"-"`
	InputSnapshot              map[string]WorkflowParameterSnapshot `db:"-" json:"inputs,omitempty"`
	Result                     string                               `db:"result" json:"result,omitempty"`
	Reason                     string                               `db:"reason" json:"reason,omitempty"`
	Created                    time.Time                            `db:"created" json:"created"`
	Updated                    time.Time                            `db:"updated" json:"updated"`
	ExpiresAt                  *time.Time                           `db:"expires_at" json:"expires_at,omitempty"`
}

type WorkflowTriggerManager interface {
	GetWorkflowTriggers(projectID int, workflowTemplateID int, params RetrieveQueryParams) ([]WorkflowTrigger, error)
	GetWorkflowTrigger(projectID int, workflowTemplateID int, triggerID int) (WorkflowTrigger, error)
	CreateWorkflowTrigger(trigger WorkflowTrigger) (WorkflowTrigger, error)
	UpdateWorkflowTrigger(trigger WorkflowTrigger, expectedRevision int) (WorkflowTrigger, error)
	DeleteWorkflowTrigger(projectID int, workflowTemplateID int, triggerID int) error
	GetWorkflowTriggerInvocations(projectID int, triggerID int, params RetrieveQueryParams) ([]WorkflowTriggerInvocation, error)
	ClaimWorkflowTriggerInvocation(invocation WorkflowTriggerInvocation, now time.Time) (WorkflowTriggerInvocation, bool, error)
	UpdateWorkflowTriggerInvocation(invocation WorkflowTriggerInvocation) error
	UpdateWorkflowTriggerInvocationSnapshots(invocation WorkflowTriggerInvocation) error
	DeleteExpiredWorkflowTriggerInvocations(before time.Time, limit int) (int64, error)
	GetActiveWorkflowScheduleTriggers() ([]WorkflowTrigger, error)
	RecordWorkflowTriggerResult(projectID int, triggerID int, firedAt time.Time, result string) error
}

func ValidateWorkflowTriggerInvocation(invocation WorkflowTriggerInvocation) error {
	if invocation.ProjectID <= 0 || invocation.WorkflowTriggerID <= 0 || invocation.WorkflowTemplateID <= 0 {
		return errors.New("workflow trigger invocation ownership is invalid")
	}
	if invocation.TriggerRevision <= 0 || invocation.DefinitionRevision <= 0 {
		return errors.New("workflow trigger invocation revision is invalid")
	}
	identities := 0
	if invocation.RequestKeyHash != nil {
		identities++
	}
	if invocation.OccurrenceIdentity != nil {
		identities++
	}
	if invocation.WebhookEventHash != nil {
		identities++
	}
	if identities > 1 {
		return errors.New("workflow trigger invocation cannot have multiple identities")
	}
	if invocation.RequestKeyHash != nil && !validWorkflowTriggerHash(*invocation.RequestKeyHash) {
		return errors.New("workflow trigger invocation request key hash is invalid")
	}
	if invocation.OccurrenceIdentity != nil && (len(*invocation.OccurrenceIdentity) != 64 || !strings.HasPrefix(*invocation.OccurrenceIdentity, "wts_")) {
		return errors.New("workflow trigger invocation occurrence identity is invalid")
	}
	if invocation.WebhookEventHash != nil {
		if !validWorkflowWebhookEventHash(*invocation.WebhookEventHash) ||
			!validWorkflowWebhookEventID(invocation.WebhookEventID) ||
			!validWorkflowWebhookKeyID(invocation.WebhookKeyID) ||
			invocation.WebhookSignedAt == nil || invocation.WebhookSignedAt.IsZero() ||
			invocation.ExpiresAt != nil {
			return errors.New("workflow trigger invocation webhook identity is invalid")
		}
	} else if invocation.WebhookEventID != "" || invocation.WebhookKeyID != "" || invocation.WebhookSignedAt != nil || invocation.WebhookReplayCount != 0 || invocation.WebhookLastReplayedAt != nil {
		return errors.New("workflow trigger invocation webhook metadata is invalid")
	}
	if invocation.WebhookReplayCount < 0 ||
		(invocation.WebhookReplayCount == 0) != (invocation.WebhookLastReplayedAt == nil) ||
		(invocation.WebhookLastReplayedAt != nil && (invocation.WebhookLastReplayedAt.Before(invocation.Created) ||
			(invocation.WebhookSignedAt != nil && invocation.WebhookLastReplayedAt.Before(*invocation.WebhookSignedAt)))) {
		return errors.New("workflow trigger invocation webhook replay metadata is invalid")
	}
	if !containsWorkflowTriggerInvocationStatus(invocation.Status) {
		return errors.New("workflow trigger invocation status is invalid")
	}
	if invocation.TriggerSnapshotJSON == "" || !json.Valid([]byte(invocation.TriggerSnapshotJSON)) {
		return errors.New("workflow trigger invocation snapshot is invalid")
	}
	if invocation.InputSnapshotJSON == "" || !json.Valid([]byte(invocation.InputSnapshotJSON)) {
		return errors.New("workflow trigger invocation input snapshot is invalid")
	}
	if invocation.Created.IsZero() || invocation.Updated.IsZero() {
		return errors.New("workflow trigger invocation timestamps are invalid")
	}
	if invocation.ExpiresAt != nil && !invocation.ExpiresAt.After(invocation.Created) {
		return errors.New("workflow trigger invocation expiry is invalid")
	}
	return nil
}

func (trigger WorkflowTrigger) UsesCredential() bool {
	return trigger.Type == WorkflowTriggerAPI
}

func (trigger WorkflowTrigger) UsesWebhookSigning() bool {
	return trigger.Type == WorkflowTriggerWebhook
}

func ValidateWorkflowTrigger(
	trigger WorkflowTrigger,
	declarations []WorkflowParameterDeclaration,
) error {
	if trigger.ProjectID <= 0 || trigger.WorkflowTemplateID <= 0 || trigger.OwnerUserID <= 0 {
		return errors.New("workflow trigger ownership is invalid")
	}
	if trigger.Revision < 0 {
		return errors.New("workflow trigger revision is invalid")
	}
	if strings.TrimSpace(trigger.Name) == "" || len(trigger.Name) > MaxWorkflowTriggerNameBytes {
		return errors.New("workflow trigger name is invalid")
	}
	if !containsWorkflowTriggerType(trigger.Type) {
		return errors.New("workflow trigger type is invalid")
	}
	if trigger.Type == WorkflowTriggerSchedule {
		if strings.TrimSpace(trigger.CronFormat) == "" {
			return errors.New("scheduled workflow trigger requires a cron expression")
		}
	} else if trigger.CronFormat != "" {
		return errors.New("only scheduled workflow triggers can declare cron expressions")
	}
	switch trigger.Type {
	case WorkflowTriggerAPI:
		if trigger.CredentialGeneration < 0 {
			return errors.New("workflow trigger credential generation is invalid")
		}
		if trigger.CredentialHash != "" && !validWorkflowTriggerHash(trigger.CredentialHash) {
			return errors.New("workflow trigger credential hash is invalid")
		}
	case WorkflowTriggerWebhook:
		if !validDormantWorkflowWebhookCredential(trigger.CredentialHash, trigger.CredentialGeneration) {
			return errors.New("workflow trigger dormant credential metadata is invalid")
		}
	default:
		if trigger.CredentialHash != "" || trigger.CredentialGeneration != 0 {
			return errors.New("manual and scheduled workflow triggers cannot carry credentials")
		}
	}
	if trigger.UsesWebhookSigning() {
		if !ValidWorkflowWebhookSigningState(
			trigger.CurrentSigningSecretEncrypted,
			trigger.CurrentSigningKeyID,
			trigger.NextSigningSecretEncrypted,
			trigger.NextSigningKeyID,
			trigger.CurrentSigningGeneration,
			trigger.NextSigningGeneration,
		) {
			return errors.New("workflow trigger signing state is invalid")
		}
	} else if trigger.CurrentSigningSecretEncrypted != "" || trigger.CurrentSigningKeyID != "" ||
		trigger.NextSigningSecretEncrypted != "" || trigger.NextSigningKeyID != "" ||
		trigger.CurrentSigningGeneration != 0 || trigger.NextSigningGeneration != 0 {
		return errors.New("non-webhook workflow triggers cannot carry signing material")
	}
	if len(trigger.InputMappings) > MaxWorkflowTriggerMappings {
		return fmt.Errorf("workflow trigger mappings exceed maximum count %d", MaxWorkflowTriggerMappings)
	}
	if err := ValidateWorkflowParameterDeclarations(declarations); err != nil {
		return err
	}

	parameters := make(map[string]WorkflowParameterDeclaration, len(declarations))
	for _, declaration := range declarations {
		parameters[declaration.Name] = declaration
	}
	mappedParameters := make(map[string]struct{}, len(trigger.InputMappings))
	requestKeys := make(map[string]struct{}, len(trigger.InputMappings))
	for index, mapping := range trigger.InputMappings {
		declaration, ok := parameters[mapping.Parameter]
		if !ok {
			return fmt.Errorf("workflow trigger mapping %d references unknown parameter %q", index, mapping.Parameter)
		}
		if _, duplicate := mappedParameters[mapping.Parameter]; duplicate {
			return fmt.Errorf("workflow trigger parameter %q is mapped more than once", mapping.Parameter)
		}
		mappedParameters[mapping.Parameter] = struct{}{}
		switch mapping.Source {
		case WorkflowTriggerInputFixed:
			if mapping.Key != "" || len(mapping.Value) == 0 {
				return fmt.Errorf("workflow trigger fixed mapping %q is invalid", mapping.Parameter)
			}
			if _, err := resolveWorkflowParameterValue(declaration, mapping.Value, WorkflowParameterSourceTrigger); err != nil {
				return fmt.Errorf("workflow trigger fixed mapping %q: %w", mapping.Parameter, err)
			}
		case WorkflowTriggerInputRequest:
			if trigger.Type == WorkflowTriggerSchedule {
				return errors.New("scheduled workflow triggers cannot read request fields")
			}
			if !workflowArtifactNamePattern.MatchString(mapping.Key) || len(mapping.Value) != 0 {
				return fmt.Errorf("workflow trigger request mapping %q is invalid", mapping.Parameter)
			}
			if _, duplicate := requestKeys[mapping.Key]; duplicate {
				return fmt.Errorf("workflow trigger request field %q is mapped more than once", mapping.Key)
			}
			requestKeys[mapping.Key] = struct{}{}
		default:
			return fmt.Errorf("workflow trigger mapping %q has an invalid source", mapping.Parameter)
		}
	}

	for _, declaration := range declarations {
		if !declaration.Required || len(declaration.Default) != 0 {
			continue
		}
		if _, ok := mappedParameters[declaration.Name]; !ok {
			return fmt.Errorf("workflow trigger must map required parameter %q", declaration.Name)
		}
	}
	return nil
}

func ResolveWorkflowTriggerValues(
	trigger WorkflowTrigger,
	requestValues map[string]json.RawMessage,
) (map[string]json.RawMessage, error) {
	allowedRequestKeys := make(map[string]struct{}, len(trigger.InputMappings))
	for _, mapping := range trigger.InputMappings {
		if mapping.Source == WorkflowTriggerInputRequest {
			allowedRequestKeys[mapping.Key] = struct{}{}
		}
	}
	for _, key := range sortedRawMessageKeys(requestValues) {
		if _, ok := allowedRequestKeys[key]; !ok {
			return nil, fmt.Errorf("unknown workflow trigger input %q", key)
		}
	}

	values := make(map[string]json.RawMessage, len(trigger.InputMappings))
	for _, mapping := range trigger.InputMappings {
		switch mapping.Source {
		case WorkflowTriggerInputFixed:
			values[mapping.Parameter] = append(json.RawMessage(nil), mapping.Value...)
		case WorkflowTriggerInputRequest:
			if value, ok := requestValues[mapping.Key]; ok {
				values[mapping.Parameter] = append(json.RawMessage(nil), value...)
			}
		default:
			return nil, fmt.Errorf("workflow trigger mapping %q has an invalid source", mapping.Parameter)
		}
	}
	return values, nil
}

func ValidateWorkflowTriggerCanFire(trigger WorkflowTrigger) error {
	if !trigger.Enabled {
		return errors.New("workflow trigger is disabled")
	}
	if trigger.UsesCredential() && (trigger.CredentialGeneration <= 0 || !validWorkflowTriggerHash(trigger.CredentialHash)) {
		return errors.New("workflow trigger credential is unavailable")
	}
	if trigger.UsesWebhookSigning() && (trigger.CurrentSigningSecretEncrypted == "" || trigger.CurrentSigningGeneration <= 0 || !validWorkflowWebhookKeyID(trigger.CurrentSigningKeyID)) {
		return errors.New("workflow trigger signing material is unavailable")
	}
	return nil
}

func HashWorkflowTriggerCredential(credential string) string {
	if credential == "" {
		return ""
	}
	return workflowTriggerHash("semaphore-workflow-trigger-credential:v1", credential)
}

func WorkflowTriggerCredentialMatches(hash string, credential string) bool {
	candidate := HashWorkflowTriggerCredential(credential)
	return len(hash) == len(candidate) && subtle.ConstantTimeCompare([]byte(hash), []byte(candidate)) == 1
}

func HashWorkflowTriggerRequestKey(triggerID int, credentialGeneration int, idempotencyKey string) string {
	if triggerID <= 0 || credentialGeneration <= 0 || strings.TrimSpace(idempotencyKey) == "" || len(idempotencyKey) > MaxWorkflowTriggerRequestKey {
		return ""
	}
	return workflowTriggerHash(
		"semaphore-workflow-trigger-request:v1",
		fmt.Sprintf("%d\x00%d\x00%s", triggerID, credentialGeneration, idempotencyKey),
	)
}

func WorkflowTriggerScheduleOccurrenceIdentity(
	triggerID int,
	triggerRevision int,
	definitionRevision int,
	scheduledAt time.Time,
) string {
	if triggerID <= 0 || triggerRevision <= 0 || definitionRevision <= 0 || scheduledAt.IsZero() {
		return ""
	}
	hash := workflowTriggerHash(
		"semaphore-workflow-trigger-schedule:v1",
		fmt.Sprintf("%d\x00%d\x00%d\x00%s", triggerID, triggerRevision, definitionRevision, scheduledAt.UTC().Format(time.RFC3339Nano)),
	)
	return "wts_" + strings.TrimPrefix(hash, "sha256:")[:60]
}

func WorkflowTriggerInvocationCorrelationID(invocation WorkflowTriggerInvocation) string {
	if invocation.WebhookEventHash != nil {
		// Keep correlation IDs within the persisted varchar(64) limit while
		// preserving the stable, rotation-independent event identity.
		if !validWorkflowWebhookEventHash(*invocation.WebhookEventHash) {
			return ""
		}
		return "swh_" + (*invocation.WebhookEventHash)[:60]
	}
	if invocation.OccurrenceIdentity != nil {
		return *invocation.OccurrenceIdentity
	}
	if invocation.RequestKeyHash == nil {
		return ""
	}
	hash := workflowTriggerHash(
		"semaphore-workflow-trigger-correlation:v1",
		fmt.Sprintf("%d\x00%d\x00%s", invocation.WorkflowTriggerID, invocation.CredentialGeneration, *invocation.RequestKeyHash),
	)
	return "wte_" + strings.TrimPrefix(hash, "sha256:")[:60]
}

func workflowTriggerHash(domain string, value string) string {
	hash := sha256.Sum256([]byte(domain + "\x00" + value))
	return "sha256:" + hex.EncodeToString(hash[:])
}

func validWorkflowTriggerHash(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validWorkflowWebhookEventHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if !(value[index] >= '0' && value[index] <= '9') && !(value[index] >= 'a' && value[index] <= 'f') {
			return false
		}
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ValidWorkflowWebhookSigningState accepts only deliberately blank migrated
// records, a current key, and an optional staged or retired next key. It is
// exported so consumers never decrypt attacker-influenced persisted state
// before validating its rotation metadata.
func ValidWorkflowWebhookSigningState(currentSecret, currentKeyID, nextSecret, nextKeyID string, currentGeneration, nextGeneration int) bool {
	if (currentSecret == "") != (currentKeyID == "") || (nextSecret == "") != (nextKeyID == "") {
		return false
	}
	if currentSecret == "" {
		return currentKeyID == "" && nextSecret == "" && nextKeyID == "" && currentGeneration == 0 && nextGeneration == 0
	}
	if currentGeneration <= 0 {
		return false
	}
	if !validWorkflowWebhookKeyID(currentKeyID) {
		return false
	}
	if nextSecret == "" {
		return nextGeneration == 0
	}
	if nextKeyID == currentKeyID || !validWorkflowWebhookKeyID(nextKeyID) {
		return false
	}
	// A greater next generation is staged; a smaller one is retired after a
	// promotion. Equal generations are never a meaningful rotation state.
	if nextGeneration <= 0 || nextGeneration == currentGeneration {
		return false
	}
	return true
}

// Legacy webhook credential metadata is retained solely for a reversible
// migration path. UsesCredential deliberately excludes webhooks, so this data
// cannot authenticate an inbound request or bypass signing state validation.
func validDormantWorkflowWebhookCredential(hash string, generation int) bool {
	if hash == "" && generation == 0 {
		return true
	}
	return generation > 0 && validWorkflowTriggerHash(hash)
}

func validWorkflowWebhookEventID(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if !(character >= 'a' && character <= 'z') &&
			!(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') &&
			character != '.' && character != '_' && character != '-' && character != ':' {
			return false
		}
	}
	return true
}

func validWorkflowWebhookKeyID(value string) bool {
	const prefix = "swhkid_"
	if len(value) < len(prefix)+1 || len(value) > 64 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for index := len(prefix); index < len(value); index++ {
		character := value[index]
		if !(character >= 'a' && character <= 'z') &&
			!(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func containsWorkflowTriggerType(wanted WorkflowTriggerType) bool {
	for _, value := range WorkflowTriggerTypes() {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsWorkflowTriggerInvocationStatus(wanted WorkflowTriggerInvocationStatus) bool {
	switch wanted {
	case WorkflowTriggerInvocationClaimed,
		WorkflowTriggerInvocationRunning,
		WorkflowTriggerInvocationSucceeded,
		WorkflowTriggerInvocationFailed,
		WorkflowTriggerInvocationRejected,
		WorkflowTriggerInvocationBlocked:
		return true
	default:
		return false
	}
}
