package db

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxWorkflowFileArtifactBytes            int64 = 64 * 1024 * 1024
	MaxWorkflowFileArtifactRunBytes         int64 = 256 * 1024 * 1024
	MaxWorkflowFileArtifactChunkBytes             = 1024 * 1024
	MaxWorkflowFileArtifactsPerRun                = 256
	MaxWorkflowFileArtifactRoles                  = 32
	MaxWorkflowFileArtifactCredentials            = 64
	MaxWorkflowFileArtifactFilenameBytes          = 255
	MaxWorkflowFileArtifactMediaTypeBytes         = 128
	MaxWorkflowFileArtifactProducerBytes          = 128
	MaxWorkflowFileArtifactTargetBytes            = 128
	MaxWorkflowFileArtifactDownloadLease          = 15 * time.Minute
	MinWorkflowFileArtifactDownloadLease          = 30 * time.Second
	DefaultWorkflowArtifactRetentionSeconds       = 30 * 24 * 60 * 60

	MinWorkflowArtifactRetentionSeconds int64 = 60 * 60
	MaxWorkflowArtifactRetentionSeconds int64 = 10 * 365 * 24 * 60 * 60
)

var (
	workflowFileArtifactNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	workflowFileArtifactTargetPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,127}$`)
)

type WorkflowFileArtifactState string

const (
	WorkflowFileArtifactStaging   WorkflowFileArtifactState = "staging"
	WorkflowFileArtifactAvailable WorkflowFileArtifactState = "available"
	WorkflowFileArtifactFailed    WorkflowFileArtifactState = "failed"
	WorkflowFileArtifactExpired   WorkflowFileArtifactState = "expired"
	WorkflowFileArtifactDeleted   WorkflowFileArtifactState = "deleted"
)

// WorkflowFileArtifactChunk is one bounded content segment. Data is never
// serialized through API metadata responses.
type WorkflowFileArtifactChunk struct {
	ArtifactID  int    `db:"artifact_id" json:"artifact_id"`
	Ordinal     int    `db:"ordinal" json:"ordinal"`
	OffsetBytes int64  `db:"offset_bytes" json:"offset_bytes"`
	SizeBytes   int    `db:"size_bytes" json:"size_bytes"`
	Data        []byte `db:"data" json:"-"`
}

func (chunk WorkflowFileArtifactChunk) Validate() error {
	if chunk.ArtifactID < 1 || chunk.Ordinal < 0 || chunk.OffsetBytes < 0 ||
		chunk.SizeBytes < 1 || chunk.SizeBytes > MaxWorkflowFileArtifactChunkBytes ||
		len(chunk.Data) != chunk.SizeBytes {
		return errors.New("workflow file artifact chunk is invalid")
	}
	return nil
}

// WorkflowFileArtifactRunUsage serializes lifetime reservations for a run.
// Failed and expired artifacts keep their reservation so concurrent retries
// cannot bypass the immutable per-run limit.
type WorkflowFileArtifactRunUsage struct {
	WorkflowRunID int   `db:"workflow_run_id" json:"workflow_run_id"`
	ReservedBytes int64 `db:"reserved_bytes" json:"reserved_bytes"`
	ArtifactCount int   `db:"artifact_count" json:"artifact_count"`
	Revision      int   `db:"revision" json:"revision"`
}

func (usage WorkflowFileArtifactRunUsage) Validate() error {
	if usage.WorkflowRunID < 1 || usage.ReservedBytes < 0 || usage.ReservedBytes > MaxWorkflowFileArtifactRunBytes ||
		usage.ArtifactCount < 0 || usage.ArtifactCount > MaxWorkflowFileArtifactsPerRun || usage.Revision < 1 {
		return errors.New("workflow file artifact run usage is invalid")
	}
	return nil
}

// WorkflowFileArtifactDownloadLease fences retention while one bounded
// download is streaming. Tokens are server-generated 256-bit lowercase hex.
type WorkflowFileArtifactDownloadLease struct {
	LeaseToken string    `db:"lease_token" json:"-"`
	ArtifactID int       `db:"artifact_id" json:"artifact_id"`
	ExpiresAt  time.Time `db:"expires_at" json:"expires_at"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
}

func (lease WorkflowFileArtifactDownloadLease) Validate() error {
	if !validWorkflowFileArtifactSHA256(lease.LeaseToken) || lease.ArtifactID < 1 ||
		lease.CreatedAt.IsZero() || !lease.ExpiresAt.After(lease.CreatedAt) ||
		lease.ExpiresAt.Sub(lease.CreatedAt) > MaxWorkflowFileArtifactDownloadLease {
		return errors.New("workflow file artifact download lease is invalid")
	}
	return nil
}

// WorkflowFileArtifactReference is a tenant-bound garbage-collection target.
type WorkflowFileArtifactReference struct {
	ProjectID     int `json:"project_id"`
	WorkflowRunID int `json:"workflow_run_id"`
	ArtifactID    int `json:"artifact_id"`
}

func (reference WorkflowFileArtifactReference) Validate() error {
	if reference.ProjectID < 1 || reference.WorkflowRunID < 1 || reference.ArtifactID < 1 {
		return errors.New("workflow file artifact reference is invalid")
	}
	return nil
}

type WorkflowArtifactRetentionScope string

const (
	WorkflowArtifactRetentionGlobal  WorkflowArtifactRetentionScope = "global"
	WorkflowArtifactRetentionProject WorkflowArtifactRetentionScope = "project"
)

// WorkflowFileArtifactAccessPolicy can only narrow the workflow-view grant.
// Role existence and the caller's current assignment are evaluated at read
// time so a removed assignment cannot survive as an artifact capability.
type WorkflowFileArtifactAccessPolicy struct {
	Revision int                    `json:"revision"`
	RoleIDs  []ProjectRoleReference `json:"role_ids,omitempty"`
}

func (policy WorkflowFileArtifactAccessPolicy) Validate() error {
	if policy.Revision < 1 || len(policy.RoleIDs) > MaxWorkflowFileArtifactRoles {
		return errors.New("workflow file artifact access policy is invalid")
	}
	seen := make(map[ProjectRoleReference]struct{}, len(policy.RoleIDs))
	for _, roleID := range policy.RoleIDs {
		if !IsValidProjectRoleReferenceSyntax(roleID) {
			return errors.New("workflow file artifact role reference is invalid")
		}
		if _, duplicate := seen[roleID]; duplicate {
			return errors.New("workflow file artifact role reference is duplicated")
		}
		seen[roleID] = struct{}{}
	}
	return nil
}

// WorkflowFileArtifactCredentialProvenance is a value-free snapshot of one
// successfully resolved global credential used by the producer task.
type WorkflowFileArtifactCredentialProvenance struct {
	CredentialID       int    `json:"credential_id"`
	GrantID            int    `json:"grant_id"`
	CredentialVersion  int    `json:"credential_version"`
	VersionFingerprint string `json:"version_fingerprint"`
	ProviderVersion    int    `json:"provider_version,omitempty"`
	Target             string `json:"target"`
}

func (provenance WorkflowFileArtifactCredentialProvenance) Validate() error {
	if provenance.CredentialID < 1 || provenance.GrantID < 1 || provenance.CredentialVersion < 1 ||
		provenance.ProviderVersion < 0 || !validWorkflowFileArtifactSHA256(provenance.VersionFingerprint) ||
		len(provenance.Target) > MaxWorkflowFileArtifactTargetBytes ||
		!workflowFileArtifactTargetPattern.MatchString(provenance.Target) {
		return errors.New("workflow file artifact credential provenance is invalid")
	}
	return nil
}

// WorkflowFileArtifactUpload contains caller-selectable metadata. Ownership,
// producer, credential, workflow-version, and retention provenance are loaded
// by the server from the referenced run and task.
type WorkflowFileArtifactUpload struct {
	WorkflowNodeID int                              `json:"workflow_node_id"`
	TaskID         int                              `json:"task_id"`
	Attempt        int                              `json:"attempt"`
	LogicalName    string                           `json:"logical_name"`
	Filename       string                           `json:"filename"`
	MediaType      string                           `json:"media_type"`
	SizeBytes      int64                            `json:"size_bytes"`
	SHA256         string                           `json:"sha256"`
	AccessPolicy   WorkflowFileArtifactAccessPolicy `json:"access_policy"`
}

func (upload WorkflowFileArtifactUpload) Validate() error {
	if upload.WorkflowNodeID < 1 || upload.TaskID < 1 || upload.Attempt < 0 ||
		!workflowFileArtifactNamePattern.MatchString(upload.LogicalName) ||
		upload.SizeBytes < 1 || upload.SizeBytes > MaxWorkflowFileArtifactBytes ||
		!validWorkflowFileArtifactSHA256(upload.SHA256) {
		return errors.New("workflow file artifact upload is invalid")
	}
	if err := ValidateWorkflowFileArtifactFilename(upload.Filename); err != nil {
		return err
	}
	if _, err := CanonicalWorkflowFileArtifactMediaType(upload.MediaType); err != nil {
		return err
	}
	return upload.AccessPolicy.Validate()
}

// WorkflowArtifactRetentionPolicy is append-only governance. Project policy
// may narrow but cannot widen the effective global duration or byte ceilings.
type WorkflowArtifactRetentionPolicy struct {
	ID               int                            `db:"id" json:"id"`
	Scope            WorkflowArtifactRetentionScope `db:"scope" json:"scope"`
	ProjectID        *int                           `db:"project_id" json:"project_id,omitempty"`
	Revision         int                            `db:"revision" json:"revision"`
	RetentionSeconds int64                          `db:"retention_seconds" json:"retention_seconds"`
	MaxArtifactBytes int64                          `db:"max_artifact_bytes" json:"max_artifact_bytes"`
	MaxRunBytes      int64                          `db:"max_run_bytes" json:"max_run_bytes"`
	CreatedByUserID  int                            `db:"created_by_user_id" json:"created_by_user_id"`
	CreatedAt        time.Time                      `db:"created_at" json:"created_at"`
}

func (policy WorkflowArtifactRetentionPolicy) Validate() error {
	if policy.Revision < 1 || policy.CreatedByUserID < 1 || policy.CreatedAt.IsZero() ||
		policy.RetentionSeconds < MinWorkflowArtifactRetentionSeconds ||
		policy.RetentionSeconds > MaxWorkflowArtifactRetentionSeconds ||
		policy.MaxArtifactBytes < 1 || policy.MaxArtifactBytes > MaxWorkflowFileArtifactBytes ||
		policy.MaxRunBytes < policy.MaxArtifactBytes || policy.MaxRunBytes > MaxWorkflowFileArtifactRunBytes {
		return errors.New("workflow artifact retention policy is invalid")
	}
	switch policy.Scope {
	case WorkflowArtifactRetentionGlobal:
		if policy.ProjectID != nil {
			return errors.New("global workflow artifact retention policy cannot name a project")
		}
	case WorkflowArtifactRetentionProject:
		if policy.ProjectID == nil || *policy.ProjectID < 1 {
			return errors.New("project workflow artifact retention policy requires a project")
		}
	default:
		return errors.New("workflow artifact retention scope is invalid")
	}
	return nil
}

// WorkflowArtifactRetentionSnapshot proves the exact governance revisions and
// limits used when an artifact becomes visible.
type WorkflowArtifactRetentionSnapshot struct {
	GlobalRevision   int   `json:"global_revision"`
	ProjectRevision  int   `json:"project_revision,omitempty"`
	RetentionSeconds int64 `json:"retention_seconds"`
	MaxArtifactBytes int64 `json:"max_artifact_bytes"`
	MaxRunBytes      int64 `json:"max_run_bytes"`
}

func (snapshot WorkflowArtifactRetentionSnapshot) Validate() error {
	if snapshot.GlobalRevision < 0 || snapshot.ProjectRevision < 0 ||
		snapshot.RetentionSeconds < MinWorkflowArtifactRetentionSeconds ||
		snapshot.RetentionSeconds > MaxWorkflowArtifactRetentionSeconds ||
		snapshot.MaxArtifactBytes < 1 || snapshot.MaxArtifactBytes > MaxWorkflowFileArtifactBytes ||
		snapshot.MaxRunBytes < snapshot.MaxArtifactBytes || snapshot.MaxRunBytes > MaxWorkflowFileArtifactRunBytes {
		return errors.New("workflow artifact retention snapshot is invalid")
	}
	return nil
}

// DefaultWorkflowArtifactRetentionSnapshot is the immutable built-in global
// revision 0 used until an administrator publishes revision 1.
func DefaultWorkflowArtifactRetentionSnapshot() WorkflowArtifactRetentionSnapshot {
	return WorkflowArtifactRetentionSnapshot{
		GlobalRevision:   0,
		RetentionSeconds: DefaultWorkflowArtifactRetentionSeconds,
		MaxArtifactBytes: MaxWorkflowFileArtifactBytes,
		MaxRunBytes:      MaxWorkflowFileArtifactRunBytes,
	}
}

func ResolveWorkflowArtifactRetention(global WorkflowArtifactRetentionPolicy, project *WorkflowArtifactRetentionPolicy) (WorkflowArtifactRetentionSnapshot, error) {
	if err := global.Validate(); err != nil || global.Scope != WorkflowArtifactRetentionGlobal {
		return WorkflowArtifactRetentionSnapshot{}, errors.New("global workflow artifact retention policy is invalid")
	}
	snapshot := WorkflowArtifactRetentionSnapshot{
		GlobalRevision: global.Revision, RetentionSeconds: global.RetentionSeconds,
		MaxArtifactBytes: global.MaxArtifactBytes, MaxRunBytes: global.MaxRunBytes,
	}
	if project == nil {
		return snapshot, nil
	}
	if err := project.Validate(); err != nil || project.Scope != WorkflowArtifactRetentionProject ||
		project.RetentionSeconds > global.RetentionSeconds ||
		project.MaxArtifactBytes > global.MaxArtifactBytes || project.MaxRunBytes > global.MaxRunBytes {
		return WorkflowArtifactRetentionSnapshot{}, errors.New("project workflow artifact retention policy widens the global policy")
	}
	snapshot.ProjectRevision = project.Revision
	snapshot.RetentionSeconds = project.RetentionSeconds
	snapshot.MaxArtifactBytes = project.MaxArtifactBytes
	snapshot.MaxRunBytes = project.MaxRunBytes
	return snapshot, snapshot.Validate()
}

// WorkflowFileArtifactMetadata is the value-free immutable identity and
// provenance for one file. Content is stored only in bounded SQL chunks.
type WorkflowFileArtifactMetadata struct {
	ID                         int                                        `db:"id" json:"id"`
	ProjectID                  int                                        `db:"project_id" json:"project_id"`
	WorkflowTemplateID         int                                        `db:"workflow_template_id" json:"workflow_template_id"`
	WorkflowRunID              int                                        `db:"workflow_run_id" json:"workflow_run_id"`
	WorkflowNodeID             int                                        `db:"workflow_node_id" json:"workflow_node_id"`
	WorkflowDefinitionRevision int                                        `db:"workflow_definition_revision" json:"workflow_definition_revision"`
	TaskID                     int                                        `db:"task_id" json:"task_id"`
	Attempt                    int                                        `db:"attempt" json:"attempt"`
	LogicalName                string                                     `db:"logical_name" json:"logical_name"`
	Filename                   string                                     `db:"filename" json:"filename"`
	MediaType                  string                                     `db:"media_type" json:"media_type"`
	SizeBytes                  int64                                      `db:"size_bytes" json:"size_bytes"`
	UploadedBytes              int64                                      `db:"uploaded_bytes" json:"uploaded_bytes"`
	SHA256                     string                                     `db:"sha256" json:"sha256"`
	State                      WorkflowFileArtifactState                  `db:"state" json:"state"`
	Revision                   int                                        `db:"revision" json:"revision"`
	ProducerUserID             int                                        `db:"producer_user_id" json:"producer_user_id"`
	ProducerRunnerID           *int                                       `db:"producer_runner_id" json:"producer_runner_id,omitempty"`
	ProducerTemplateID         int                                        `db:"producer_template_id" json:"producer_template_id"`
	ProducerVersion            string                                     `db:"producer_version" json:"producer_version"`
	CredentialProvenanceJSON   string                                     `db:"credential_provenance" json:"-"`
	CredentialProvenance       []WorkflowFileArtifactCredentialProvenance `db:"-" json:"credential_provenance,omitempty"`
	AccessPolicyJSON           string                                     `db:"access_policy" json:"-"`
	AccessPolicy               WorkflowFileArtifactAccessPolicy           `db:"-" json:"access_policy"`
	RetentionGlobalRevision    int                                        `db:"retention_global_revision" json:"-"`
	RetentionProjectRevision   int                                        `db:"retention_project_revision" json:"-"`
	RetentionSeconds           int64                                      `db:"retention_seconds" json:"-"`
	RetentionMaxArtifactBytes  int64                                      `db:"retention_max_artifact_bytes" json:"-"`
	RetentionMaxRunBytes       int64                                      `db:"retention_max_run_bytes" json:"-"`
	Retention                  WorkflowArtifactRetentionSnapshot          `db:"-" json:"retention"`
	CreatedAt                  time.Time                                  `db:"created_at" json:"created_at"`
	FinalizedAt                *time.Time                                 `db:"finalized_at" json:"finalized_at,omitempty"`
	ExpiresAt                  *time.Time                                 `db:"expires_at" json:"expires_at,omitempty"`
	DeletedAt                  *time.Time                                 `db:"deleted_at" json:"deleted_at,omitempty"`
}

// CanonicalizeForPersistence derives the raw SQL values from the validated
// immutable provenance, access policy, and retention snapshot before a write.
func (artifact *WorkflowFileArtifactMetadata) CanonicalizeForPersistence() error {
	if artifact == nil {
		return errors.New("workflow file artifact metadata is nil")
	}
	candidate := *artifact
	if candidate.CredentialProvenance == nil {
		candidate.CredentialProvenance = []WorkflowFileArtifactCredentialProvenance{}
	}
	credentialProvenanceJSON, accessPolicyJSON, err := candidate.canonicalPersistenceJSON()
	if err != nil {
		return err
	}
	candidate.CredentialProvenanceJSON = credentialProvenanceJSON
	candidate.AccessPolicyJSON = accessPolicyJSON
	candidate.RetentionGlobalRevision = candidate.Retention.GlobalRevision
	candidate.RetentionProjectRevision = candidate.Retention.ProjectRevision
	candidate.RetentionSeconds = candidate.Retention.RetentionSeconds
	candidate.RetentionMaxArtifactBytes = candidate.Retention.MaxArtifactBytes
	candidate.RetentionMaxRunBytes = candidate.Retention.MaxRunBytes
	if err := candidate.Validate(); err != nil {
		return err
	}
	*artifact = candidate
	return nil
}

func (artifact WorkflowFileArtifactMetadata) Validate() error {
	if artifact.ID < 1 || artifact.ProjectID < 1 || artifact.WorkflowTemplateID < 1 ||
		artifact.WorkflowRunID < 1 || artifact.WorkflowNodeID < 1 ||
		artifact.WorkflowDefinitionRevision < 1 || artifact.TaskID < 1 || artifact.Attempt < 0 ||
		artifact.SizeBytes < 1 || artifact.SizeBytes > MaxWorkflowFileArtifactBytes ||
		artifact.UploadedBytes < 0 || artifact.UploadedBytes > artifact.SizeBytes ||
		artifact.Revision < 1 || artifact.ProducerUserID < 1 || artifact.ProducerTemplateID < 1 ||
		artifact.CreatedAt.IsZero() || !workflowFileArtifactNamePattern.MatchString(artifact.LogicalName) ||
		!validWorkflowFileArtifactSHA256(artifact.SHA256) ||
		len(artifact.ProducerVersion) < 1 || len(artifact.ProducerVersion) > MaxWorkflowFileArtifactProducerBytes ||
		strings.ContainsAny(artifact.ProducerVersion, "\x00\r\n") {
		return errors.New("workflow file artifact metadata is invalid")
	}
	if artifact.ProducerRunnerID != nil && *artifact.ProducerRunnerID < 1 {
		return errors.New("workflow file artifact producer runner is invalid")
	}
	if err := ValidateWorkflowFileArtifactFilename(artifact.Filename); err != nil {
		return err
	}
	if canonical, err := CanonicalWorkflowFileArtifactMediaType(artifact.MediaType); err != nil || canonical != artifact.MediaType {
		return errors.New("workflow file artifact media type is not canonical")
	}
	if err := artifact.AccessPolicy.Validate(); err != nil {
		return err
	}
	if err := artifact.Retention.Validate(); err != nil {
		return err
	}
	if len(artifact.CredentialProvenance) > MaxWorkflowFileArtifactCredentials {
		return errors.New("workflow file artifact credential provenance exceeds the limit")
	}
	seenCredentials := make(map[string]struct{}, len(artifact.CredentialProvenance))
	for _, provenance := range artifact.CredentialProvenance {
		if err := provenance.Validate(); err != nil {
			return err
		}
		identity := fmt.Sprintf("%d:%s", provenance.CredentialID, provenance.Target)
		if _, duplicate := seenCredentials[identity]; duplicate {
			return errors.New("workflow file artifact credential provenance is duplicated")
		}
		seenCredentials[identity] = struct{}{}
	}
	credentialProvenanceJSON, accessPolicyJSON, err := artifact.canonicalPersistenceJSON()
	if err != nil || artifact.CredentialProvenanceJSON != credentialProvenanceJSON ||
		artifact.AccessPolicyJSON != accessPolicyJSON ||
		artifact.RetentionGlobalRevision != artifact.Retention.GlobalRevision ||
		artifact.RetentionProjectRevision != artifact.Retention.ProjectRevision ||
		artifact.RetentionSeconds != artifact.Retention.RetentionSeconds ||
		artifact.RetentionMaxArtifactBytes != artifact.Retention.MaxArtifactBytes ||
		artifact.RetentionMaxRunBytes != artifact.Retention.MaxRunBytes {
		return errors.New("workflow file artifact persistence is not canonical")
	}
	switch artifact.State {
	case WorkflowFileArtifactStaging:
		if artifact.FinalizedAt != nil || artifact.ExpiresAt != nil || artifact.DeletedAt != nil {
			return errors.New("staging workflow file artifact has terminal timestamps")
		}
	case WorkflowFileArtifactAvailable:
		if artifact.UploadedBytes != artifact.SizeBytes || artifact.FinalizedAt == nil || artifact.ExpiresAt == nil ||
			artifact.DeletedAt != nil || artifact.FinalizedAt.Before(artifact.CreatedAt) ||
			!artifact.ExpiresAt.Equal(workflowFileArtifactExpiry(*artifact.FinalizedAt, artifact.Retention)) {
			return errors.New("available workflow file artifact is incomplete")
		}
	case WorkflowFileArtifactFailed:
		if artifact.FinalizedAt != nil || artifact.ExpiresAt != nil || artifact.DeletedAt == nil {
			return errors.New("failed workflow file artifact has invalid timestamps")
		}
	case WorkflowFileArtifactExpired, WorkflowFileArtifactDeleted:
		if artifact.UploadedBytes != artifact.SizeBytes || artifact.FinalizedAt == nil || artifact.ExpiresAt == nil || artifact.DeletedAt == nil ||
			artifact.DeletedAt.Before(*artifact.FinalizedAt) ||
			!artifact.ExpiresAt.Equal(workflowFileArtifactExpiry(*artifact.FinalizedAt, artifact.Retention)) {
			return errors.New("deleted workflow file artifact has invalid timestamps")
		}
	default:
		return errors.New("workflow file artifact state is invalid")
	}
	return nil
}

func (artifact WorkflowFileArtifactMetadata) canonicalPersistenceJSON() (string, string, error) {
	credentialProvenanceJSON, err := json.Marshal(artifact.CredentialProvenance)
	if err != nil {
		return "", "", err
	}
	accessPolicyJSON, err := json.Marshal(artifact.AccessPolicy)
	if err != nil {
		return "", "", err
	}
	return string(credentialProvenanceJSON), string(accessPolicyJSON), nil
}

func workflowFileArtifactExpiry(finalizedAt time.Time, retention WorkflowArtifactRetentionSnapshot) time.Time {
	return finalizedAt.Add(time.Duration(retention.RetentionSeconds) * time.Second)
}

func ValidateWorkflowFileArtifactFilename(filename string) error {
	if len(filename) < 1 || len(filename) > MaxWorkflowFileArtifactFilenameBytes ||
		!utf8.ValidString(filename) || filename == "." || filename == ".." ||
		strings.TrimSpace(filename) != filename || strings.ContainsAny(filename, "/\\") {
		return errors.New("workflow file artifact filename is invalid")
	}
	for _, character := range filename {
		if unicode.IsControl(character) {
			return errors.New("workflow file artifact filename is invalid")
		}
	}
	return nil
}

func CanonicalWorkflowFileArtifactMediaType(value string) (string, error) {
	if len(value) < 3 || len(value) > MaxWorkflowFileArtifactMediaTypeBytes || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("workflow file artifact media type is invalid")
	}
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || !strings.Contains(mediaType, "/") || strings.Contains(mediaType, "*") || len(parameters) != 0 {
		return "", errors.New("workflow file artifact media type is invalid")
	}
	canonical := strings.ToLower(mediaType)
	if canonical != value {
		return "", errors.New("workflow file artifact media type is not canonical")
	}
	return canonical, nil
}

func validWorkflowFileArtifactSHA256(value string) bool {
	if len(value) != sha256HexLength || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

const sha256HexLength = 64
