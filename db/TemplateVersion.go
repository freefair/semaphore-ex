package db

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	MaxCrossProjectTemplateGrantReasonBytes = 512
	MaxCrossProjectTemplateGrantPageSize    = 100
	MaxTemplateVersionVaultNameBytes        = 255
	MaxTemplateVersionVaultScriptBytes      = 64 * 1024
)

var (
	ErrCrossProjectTemplateGrantRevisionConflict   = errors.New("cross-project template grant revision conflict")
	ErrCrossProjectTemplateGrantInvalidTransition  = errors.New("cross-project template grant transition is invalid")
	ErrCrossProjectTemplateGrantActiveReference    = errors.New("active cross-project template grant references template version")
	ErrCrossProjectTemplateGrantUnrevokedReference = errors.New("unrevoked cross-project template grant references template")
)

// TemplateVersionExecution contains only the immutable execution metadata
// needed to reconstruct a task template. Credential material is represented by
// the dependency identifiers in TemplateVersionDependencies, never by resolved
// values or embedded vault records.
type TemplateVersionExecution struct {
	Name                      string             `json:"name"`
	Playbook                  string             `json:"playbook"`
	WorkingDirectory          *string            `json:"working_directory,omitempty"`
	Arguments                 *string            `json:"arguments,omitempty"`
	AllowOverrideArgsInTask   bool               `json:"allow_override_args_in_task,omitempty"`
	Description               *string            `json:"description,omitempty"`
	Type                      TemplateType       `json:"type,omitempty"`
	StartVersion              *string            `json:"start_version,omitempty"`
	Autorun                   bool               `json:"autorun,omitempty"`
	GitBranch                 *string            `json:"git_branch,omitempty"`
	SurveyVars                []SurveyVar        `json:"survey_vars,omitempty"`
	SuppressSuccessAlerts     bool               `json:"suppress_success_alerts,omitempty"`
	SuppressErrorAlerts       bool               `json:"suppress_error_alerts,omitempty"`
	App                       TemplateApp        `json:"app,omitempty"`
	TaskParams                MapStringAnyField  `json:"task_params,omitempty"`
	RunnerTag                 *string            `json:"runner_tag,omitempty"`
	RunnerTags                StringArrayField   `json:"runner_tags,omitempty"`
	RunnerTagMatchMode        RunnerTagMatchMode `json:"runner_tag_match_mode,omitempty"`
	ExecutorImage             *string            `json:"executor_image,omitempty"`
	AllowOverrideBranchInTask bool               `json:"allow_override_branch_in_task,omitempty"`
	AllowParallelTasks        bool               `json:"allow_parallel_tasks,omitempty"`
	JWTParams                 *TemplateJWTParams `json:"jwt_params,omitempty"`
}

// TemplateVersionVaultDescriptor preserves the authored vault association
// without embedding the resolved AccessKey value held by TemplateVault.Vault.
type TemplateVersionVaultDescriptor struct {
	ID         int               `json:"id"`
	Type       TemplateVaultType `json:"type"`
	Name       *string           `json:"name,omitempty"`
	Script     *string           `json:"script,omitempty"`
	VaultKeyID *int              `json:"vault_key_id,omitempty"`
}

// TemplateVersionReference pins a dependent build template to one immutable
// owner snapshot. Runtime code must resolve this exact reference, never a
// mutable BuildTemplateID or a template's latest version.
type TemplateVersionReference struct {
	OwnerProjectID     int    `json:"owner_project_id"`
	TemplateID         int    `json:"template_id"`
	VersionNumber      int    `json:"version_number"`
	ContentFingerprint string `json:"content_fingerprint"`
}

// TemplateVersionDependencies retains the owner-side identities that runtime
// resolution must revalidate. Their referenced values are deliberately absent.
type TemplateVersionDependencies struct {
	RepositoryID         int                              `json:"repository_id"`
	InventoryID          *int                             `json:"inventory_id,omitempty"`
	EnvironmentIDs       []int                            `json:"environment_ids,omitempty"`
	BuildTemplateVersion *TemplateVersionReference        `json:"build_template_version,omitempty"`
	Vaults               []TemplateVersionVaultDescriptor `json:"vaults,omitempty"`
}

// TemplateVersionSnapshot is the immutable, value-free execution definition
// of a published owner template version.
type TemplateVersionSnapshot struct {
	Execution    TemplateVersionExecution    `json:"execution"`
	Dependencies TemplateVersionDependencies `json:"dependencies"`
}

// TemplateVersion is a published, immutable owner template snapshot. Version
// numbers are monotonic per owner template and are used by grants as exact
// inclusively-bounded references.
type TemplateVersion struct {
	ID             int `db:"id" json:"id"`
	OwnerProjectID int `db:"owner_project_id" json:"owner_project_id"`
	TemplateID     int `db:"template_id" json:"template_id"`
	VersionNumber  int `db:"version_number" json:"version_number"`

	AuthorUserID       int                     `db:"author_user_id" json:"author_user_id"`
	ContentFingerprint string                  `db:"content_fingerprint" json:"content_fingerprint"`
	SnapshotJSON       string                  `db:"execution_snapshot" json:"-"`
	Snapshot           TemplateVersionSnapshot `db:"-" json:"snapshot"`
	Created            time.Time               `db:"created" json:"created"`
}

// CrossProjectTemplateGrantOperation is a bitset because a grant may permit
// reference, run, or both operations over one immutable version range.
type CrossProjectTemplateGrantOperation int

const (
	CrossProjectTemplateGrantReference CrossProjectTemplateGrantOperation = 1 << iota
	CrossProjectTemplateGrantRun
)

const allCrossProjectTemplateGrantOperations = CrossProjectTemplateGrantReference | CrossProjectTemplateGrantRun

func (operations CrossProjectTemplateGrantOperation) Allows(operation CrossProjectTemplateGrantOperation) bool {
	return operation != 0 && operations&operation == operation
}

func (operations CrossProjectTemplateGrantOperation) IsValid() bool {
	return operations != 0 && operations&^allCrossProjectTemplateGrantOperations == 0
}

type CrossProjectTemplateGrantStatus string

const (
	CrossProjectTemplateGrantPending CrossProjectTemplateGrantStatus = "pending"
	CrossProjectTemplateGrantActive  CrossProjectTemplateGrantStatus = "active"
	CrossProjectTemplateGrantRevoked CrossProjectTemplateGrantStatus = "revoked"
)

func (status CrossProjectTemplateGrantStatus) IsValid() bool {
	switch status {
	case CrossProjectTemplateGrantPending, CrossProjectTemplateGrantActive, CrossProjectTemplateGrantRevoked:
		return true
	default:
		return false
	}
}

// CrossProjectTemplateGrant is an owner-created, consumer-accepted capability
// over immutable template versions. It carries no secret or resolvable value.
type CrossProjectTemplateGrant struct {
	ID                int `db:"id" json:"id"`
	OwnerProjectID    int `db:"owner_project_id" json:"owner_project_id"`
	ConsumerProjectID int `db:"consumer_project_id" json:"consumer_project_id"`
	TemplateID        int `db:"template_id" json:"template_id"`

	MinTemplateVersion int                                `db:"min_template_version" json:"min_template_version"`
	MaxTemplateVersion int                                `db:"max_template_version" json:"max_template_version"`
	Operations         CrossProjectTemplateGrantOperation `db:"operations" json:"operations"`
	Status             CrossProjectTemplateGrantStatus    `db:"status" json:"status"`
	Revision           int                                `db:"revision" json:"revision"`

	Reason string `db:"reason" json:"reason"`

	CreatedByUserID int       `db:"created_by_user_id" json:"created_by_user_id"`
	Created         time.Time `db:"created" json:"created"`

	AcceptedByUserID int        `db:"accepted_by_user_id" json:"accepted_by_user_id,omitempty"`
	AcceptedAt       *time.Time `db:"accepted_at" json:"accepted_at,omitempty"`
	RevokedByUserID  int        `db:"revoked_by_user_id" json:"revoked_by_user_id,omitempty"`
	RevokedAt        *time.Time `db:"revoked_at" json:"revoked_at,omitempty"`
	RevocationReason string     `db:"revocation_reason" json:"revocation_reason,omitempty"`
}

func (snapshot TemplateVersionSnapshot) CanonicalJSON() ([]byte, error) {
	canonical := snapshot
	canonical.Dependencies.EnvironmentIDs = slices.Clone(snapshot.Dependencies.EnvironmentIDs)
	canonical.Dependencies.Vaults = sortedTemplateVersionVaults(snapshot.Dependencies.Vaults)
	if err := canonical.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("encode template version snapshot: %w", err)
	}
	return payload, nil
}

func (snapshot TemplateVersionSnapshot) Validate() error {
	if snapshot.Dependencies.RepositoryID <= 0 {
		return errors.New("template version snapshot requires a repository")
	}
	if snapshot.Dependencies.InventoryID != nil && *snapshot.Dependencies.InventoryID <= 0 {
		return errors.New("template version snapshot inventory is invalid")
	}
	if snapshot.Dependencies.BuildTemplateVersion != nil {
		reference := snapshot.Dependencies.BuildTemplateVersion
		if reference.OwnerProjectID <= 0 || reference.TemplateID <= 0 || reference.VersionNumber <= 0 ||
			!isTemplateVersionFingerprint(reference.ContentFingerprint) {
			return errors.New("template version snapshot build template reference is invalid")
		}
	}
	if err := validateTemplateVersionIDs("environment", snapshot.Dependencies.EnvironmentIDs); err != nil {
		return err
	}
	if err := validateTemplateVersionVaults(snapshot.Dependencies.Vaults); err != nil {
		return err
	}
	return nil
}

// NewTemplateVersionSnapshot preserves authored vault descriptors but excludes
// TemplateVault.Vault, which is where a resolved AccessKey value can appear.
// It therefore keeps exact execution metadata without serializing credential
// resolution output.
func NewTemplateVersionSnapshot(template Template) (TemplateVersionSnapshot, error) {
	snapshot := TemplateVersionSnapshot{
		Execution: TemplateVersionExecution{
			Name: template.Name, Playbook: template.Playbook, WorkingDirectory: template.WorkingDirectory, Arguments: template.Arguments,
			AllowOverrideArgsInTask: template.AllowOverrideArgsInTask, Description: template.Description,
			Type: template.Type, StartVersion: template.StartVersion, Autorun: template.Autorun,
			GitBranch: template.GitBranch, SurveyVars: template.SurveyVars,
			SuppressSuccessAlerts: template.SuppressSuccessAlerts,
			SuppressErrorAlerts:   template.SuppressErrorAlerts,
			App:                   template.App, TaskParams: template.TaskParams, RunnerTag: template.RunnerTag,
			RunnerTags: template.RunnerTags, RunnerTagMatchMode: template.RunnerTagMatchMode,
			ExecutorImage: template.ExecutorImage, AllowOverrideBranchInTask: template.AllowOverrideBranchInTask,
			AllowParallelTasks: template.AllowParallelTasks, JWTParams: template.JWTParams,
		},
		Dependencies: TemplateVersionDependencies{
			RepositoryID: template.RepositoryID, InventoryID: template.InventoryID,
			EnvironmentIDs: slices.Clone(template.EnvironmentIDs),
		},
	}
	for _, vault := range template.Vaults {
		if vault.ID <= 0 {
			return TemplateVersionSnapshot{}, errors.New("template version snapshot vault is invalid")
		}
		snapshot.Dependencies.Vaults = append(snapshot.Dependencies.Vaults, TemplateVersionVaultDescriptor{
			ID: vault.ID, Type: vault.Type, Name: vault.Name, Script: vault.Script, VaultKeyID: vault.VaultKeyID,
		})
	}
	snapshot.Dependencies.Vaults = sortedTemplateVersionVaults(snapshot.Dependencies.Vaults)
	immutable, err := deepCopyTemplateVersionSnapshot(snapshot)
	if err != nil {
		return TemplateVersionSnapshot{}, err
	}
	immutable.Dependencies.Vaults = sortedTemplateVersionVaults(immutable.Dependencies.Vaults)
	if _, err = immutable.CanonicalJSON(); err != nil {
		return TemplateVersionSnapshot{}, err
	}
	return immutable, nil
}

// ReconstructTemplate rebuilds the executable template metadata solely from
// a published version. It intentionally restores no live BuildTemplateID and
// no credential values: nested build provenance remains in the snapshot, and
// owner-side resources are resolved only when the queued task is dispatched.
func (snapshot TemplateVersionSnapshot) ReconstructTemplate(ownerProjectID int, templateID int) (Template, error) {
	if ownerProjectID <= 0 || templateID <= 0 {
		return Template{}, errors.New("template version reconstruction ownership is invalid")
	}
	payload, err := snapshot.CanonicalJSON()
	if err != nil {
		return Template{}, err
	}
	var immutable TemplateVersionSnapshot
	if err = json.Unmarshal(payload, &immutable); err != nil {
		return Template{}, fmt.Errorf("decode immutable template version snapshot: %w", err)
	}
	template := Template{
		ID: templateID, ProjectID: ownerProjectID,
		RepositoryID:   immutable.Dependencies.RepositoryID,
		InventoryID:    immutable.Dependencies.InventoryID,
		EnvironmentIDs: immutable.Dependencies.EnvironmentIDs,
		Name:           immutable.Execution.Name, Playbook: immutable.Execution.Playbook,
		WorkingDirectory:        immutable.Execution.WorkingDirectory,
		Arguments:               immutable.Execution.Arguments,
		AllowOverrideArgsInTask: immutable.Execution.AllowOverrideArgsInTask,
		Description:             immutable.Execution.Description, Type: immutable.Execution.Type,
		StartVersion: immutable.Execution.StartVersion, Autorun: immutable.Execution.Autorun,
		GitBranch: immutable.Execution.GitBranch, SurveyVars: immutable.Execution.SurveyVars,
		SuppressSuccessAlerts: immutable.Execution.SuppressSuccessAlerts,
		SuppressErrorAlerts:   immutable.Execution.SuppressErrorAlerts,
		App:                   immutable.Execution.App, TaskParams: immutable.Execution.TaskParams,
		RunnerTag: immutable.Execution.RunnerTag, RunnerTags: immutable.Execution.RunnerTags,
		RunnerTagMatchMode:        immutable.Execution.RunnerTagMatchMode,
		ExecutorImage:             immutable.Execution.ExecutorImage,
		AllowOverrideBranchInTask: immutable.Execution.AllowOverrideBranchInTask,
		AllowParallelTasks:        immutable.Execution.AllowParallelTasks,
		JWTParams:                 immutable.Execution.JWTParams,
	}
	if len(template.EnvironmentIDs) > 0 {
		template.EnvironmentID = template.EnvironmentIDs[0]
	}
	for _, descriptor := range immutable.Dependencies.Vaults {
		template.Vaults = append(template.Vaults, TemplateVault{
			ID: descriptor.ID, ProjectID: ownerProjectID, TemplateID: templateID,
			Type: descriptor.Type, Name: descriptor.Name, Script: descriptor.Script,
			VaultKeyID: descriptor.VaultKeyID,
		})
	}
	return template, nil
}

func deepCopyTemplateVersionSnapshot(snapshot TemplateVersionSnapshot) (TemplateVersionSnapshot, error) {
	copy := snapshot
	copy.Execution.WorkingDirectory = cloneTemplateVersionString(snapshot.Execution.WorkingDirectory)
	copy.Execution.Arguments = cloneTemplateVersionString(snapshot.Execution.Arguments)
	copy.Execution.Description = cloneTemplateVersionString(snapshot.Execution.Description)
	copy.Execution.StartVersion = cloneTemplateVersionString(snapshot.Execution.StartVersion)
	copy.Execution.GitBranch = cloneTemplateVersionString(snapshot.Execution.GitBranch)
	copy.Execution.RunnerTag = cloneTemplateVersionString(snapshot.Execution.RunnerTag)
	copy.Execution.ExecutorImage = cloneTemplateVersionString(snapshot.Execution.ExecutorImage)
	copy.Execution.RunnerTags = slices.Clone(snapshot.Execution.RunnerTags)
	if snapshot.Execution.JWTParams != nil {
		jwtParams := *snapshot.Execution.JWTParams
		jwtParams.Audience = slices.Clone(snapshot.Execution.JWTParams.Audience)
		copy.Execution.JWTParams = &jwtParams
	}
	if snapshot.Execution.SurveyVars != nil {
		payload, err := json.Marshal(snapshot.Execution.SurveyVars)
		if err != nil {
			return TemplateVersionSnapshot{}, fmt.Errorf("encode template version survey variables: %w", err)
		}
		copy.Execution.SurveyVars = nil
		if err = json.Unmarshal(payload, &copy.Execution.SurveyVars); err != nil {
			return TemplateVersionSnapshot{}, fmt.Errorf("decode template version survey variables: %w", err)
		}
	}
	if snapshot.Execution.TaskParams != nil {
		payload, err := json.Marshal(snapshot.Execution.TaskParams)
		if err != nil {
			return TemplateVersionSnapshot{}, fmt.Errorf("encode template version task parameters: %w", err)
		}
		copy.Execution.TaskParams = nil
		if err = json.Unmarshal(payload, &copy.Execution.TaskParams); err != nil {
			return TemplateVersionSnapshot{}, fmt.Errorf("decode template version task parameters: %w", err)
		}
	}
	copy.Dependencies.InventoryID = cloneTemplateVersionInt(snapshot.Dependencies.InventoryID)
	copy.Dependencies.EnvironmentIDs = slices.Clone(snapshot.Dependencies.EnvironmentIDs)
	if snapshot.Dependencies.BuildTemplateVersion != nil {
		reference := *snapshot.Dependencies.BuildTemplateVersion
		copy.Dependencies.BuildTemplateVersion = &reference
	}
	copy.Dependencies.Vaults = make([]TemplateVersionVaultDescriptor, len(snapshot.Dependencies.Vaults))
	for index, vault := range snapshot.Dependencies.Vaults {
		copy.Dependencies.Vaults[index] = TemplateVersionVaultDescriptor{
			ID: vault.ID, Type: vault.Type, Name: cloneTemplateVersionString(vault.Name),
			Script: cloneTemplateVersionString(vault.Script), VaultKeyID: cloneTemplateVersionInt(vault.VaultKeyID),
		}
	}
	return copy, nil
}

func cloneTemplateVersionString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTemplateVersionInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func TemplateVersionFingerprint(snapshot TemplateVersionSnapshot) (string, error) {
	payload, err := snapshot.CanonicalJSON()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (version *TemplateVersion) DecodeSnapshot() error {
	if version.SnapshotJSON == "" {
		return errors.New("template version snapshot is empty")
	}
	if err := json.Unmarshal([]byte(version.SnapshotJSON), &version.Snapshot); err != nil {
		return fmt.Errorf("decode template version snapshot: %w", err)
	}
	fingerprint, err := TemplateVersionFingerprint(version.Snapshot)
	if err != nil {
		return err
	}
	if fingerprint != version.ContentFingerprint {
		return errors.New("template version fingerprint does not match snapshot")
	}
	return nil
}

func (version TemplateVersion) Validate() error {
	if version.OwnerProjectID <= 0 || version.TemplateID <= 0 || version.VersionNumber <= 0 ||
		version.AuthorUserID <= 0 || version.Created.IsZero() {
		return errors.New("template version ownership or lifecycle is invalid")
	}
	if !isTemplateVersionFingerprint(version.ContentFingerprint) {
		return errors.New("template version fingerprint is invalid")
	}
	copy := version
	return copy.DecodeSnapshot()
}

func (grant CrossProjectTemplateGrant) Validate() error {
	if grant.OwnerProjectID <= 0 || grant.ConsumerProjectID <= 0 || grant.TemplateID <= 0 ||
		grant.OwnerProjectID == grant.ConsumerProjectID {
		return errors.New("cross-project template grant ownership is invalid")
	}
	if grant.MinTemplateVersion <= 0 || grant.MaxTemplateVersion < grant.MinTemplateVersion {
		return errors.New("cross-project template grant version range is invalid")
	}
	if !grant.Operations.IsValid() || !grant.Status.IsValid() || grant.Revision <= 0 ||
		grant.CreatedByUserID <= 0 || grant.Created.IsZero() ||
		len([]byte(grant.Reason)) > MaxCrossProjectTemplateGrantReasonBytes ||
		len([]byte(grant.RevocationReason)) > MaxCrossProjectTemplateGrantReasonBytes {
		return errors.New("cross-project template grant is invalid")
	}
	accepted := grant.AcceptedAt != nil || grant.AcceptedByUserID != 0
	if accepted && (grant.AcceptedAt == nil || grant.AcceptedByUserID <= 0 || grant.AcceptedAt.Before(grant.Created)) {
		return errors.New("cross-project template grant acceptance is invalid")
	}
	revoked := grant.RevokedAt != nil || grant.RevokedByUserID != 0
	if revoked && (grant.RevokedAt == nil || grant.RevokedByUserID <= 0 || grant.RevokedAt.Before(grant.Created)) {
		return errors.New("cross-project template grant revocation is invalid")
	}
	switch grant.Status {
	case CrossProjectTemplateGrantPending:
		if accepted || revoked || grant.RevocationReason != "" {
			return errors.New("pending cross-project template grant has terminal lifecycle data")
		}
	case CrossProjectTemplateGrantActive:
		if !accepted || revoked || grant.RevocationReason != "" {
			return errors.New("active cross-project template grant lifecycle is invalid")
		}
	case CrossProjectTemplateGrantRevoked:
		if !revoked || strings.TrimSpace(grant.RevocationReason) == "" {
			return errors.New("revoked cross-project template grant lifecycle is invalid")
		}
	}
	return nil
}

// Accept advances a pending grant after the caller has verified that the actor
// administers the consuming project. The repository enforces the same
// expected revision atomically.
func (grant CrossProjectTemplateGrant) Accept(actorUserID, expectedRevision int, at time.Time) (CrossProjectTemplateGrant, error) {
	if expectedRevision <= 0 || grant.Revision != expectedRevision {
		return CrossProjectTemplateGrant{}, ErrCrossProjectTemplateGrantRevisionConflict
	}
	if grant.Status != CrossProjectTemplateGrantPending || actorUserID <= 0 || at.IsZero() {
		return CrossProjectTemplateGrant{}, ErrCrossProjectTemplateGrantInvalidTransition
	}
	grant.Status = CrossProjectTemplateGrantActive
	grant.AcceptedByUserID = actorUserID
	acceptedAt := at.UTC()
	grant.AcceptedAt = &acceptedAt
	grant.Revision++
	if err := grant.Validate(); err != nil {
		return CrossProjectTemplateGrant{}, err
	}
	return grant, nil
}

// Revoke advances a pending or active grant after the caller has verified that
// the actor administers either participating project.
func (grant CrossProjectTemplateGrant) Revoke(actorUserID, expectedRevision int, reason string, at time.Time) (CrossProjectTemplateGrant, error) {
	if expectedRevision <= 0 || grant.Revision != expectedRevision {
		return CrossProjectTemplateGrant{}, ErrCrossProjectTemplateGrantRevisionConflict
	}
	if (grant.Status != CrossProjectTemplateGrantPending && grant.Status != CrossProjectTemplateGrantActive) ||
		actorUserID <= 0 || at.IsZero() || strings.TrimSpace(reason) == "" {
		return CrossProjectTemplateGrant{}, ErrCrossProjectTemplateGrantInvalidTransition
	}
	grant.Status = CrossProjectTemplateGrantRevoked
	grant.RevokedByUserID = actorUserID
	revokedAt := at.UTC()
	grant.RevokedAt = &revokedAt
	grant.RevocationReason = reason
	grant.Revision++
	if err := grant.Validate(); err != nil {
		return CrossProjectTemplateGrant{}, err
	}
	return grant, nil
}

// CrossProjectTemplateGrantUpdate contains the only mutable grant scope
// fields. Changes are restricted to pending grants so consumer acceptance is
// always for the exact range and operation set that becomes active.
type CrossProjectTemplateGrantUpdate struct {
	MinTemplateVersion int
	MaxTemplateVersion int
	Operations         CrossProjectTemplateGrantOperation
	Reason             string
}

func (grant CrossProjectTemplateGrant) Update(
	update CrossProjectTemplateGrantUpdate,
	expectedRevision int,
) (CrossProjectTemplateGrant, error) {
	if expectedRevision <= 0 || grant.Revision != expectedRevision {
		return CrossProjectTemplateGrant{}, ErrCrossProjectTemplateGrantRevisionConflict
	}
	if grant.Status != CrossProjectTemplateGrantPending {
		return CrossProjectTemplateGrant{}, ErrCrossProjectTemplateGrantInvalidTransition
	}
	grant.MinTemplateVersion = update.MinTemplateVersion
	grant.MaxTemplateVersion = update.MaxTemplateVersion
	grant.Operations = update.Operations
	grant.Reason = update.Reason
	grant.Revision++
	if err := grant.Validate(); err != nil {
		return CrossProjectTemplateGrant{}, err
	}
	return grant, nil
}

func (grant CrossProjectTemplateGrant) CoversTemplateVersion(ownerProjectID, templateID, versionNumber int) bool {
	return grant.OwnerProjectID == ownerProjectID && grant.TemplateID == templateID &&
		versionNumber >= grant.MinTemplateVersion && versionNumber <= grant.MaxTemplateVersion
}

func (grant CrossProjectTemplateGrant) BlocksTemplateVersionDeletion(ownerProjectID, templateID, versionNumber int) bool {
	return grant.Status == CrossProjectTemplateGrantActive && grant.CoversTemplateVersion(ownerProjectID, templateID, versionNumber)
}

func (grant CrossProjectTemplateGrant) BlocksTemplateDeletion(ownerProjectID, templateID int) bool {
	return grant.Status == CrossProjectTemplateGrantActive && grant.OwnerProjectID == ownerProjectID && grant.TemplateID == templateID
}

func validateTemplateVersionIDs(kind string, values []int) error {
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			return fmt.Errorf("template version %s ID is invalid", kind)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("template version %s ID is duplicated", kind)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func sortedTemplateVersionVaults(values []TemplateVersionVaultDescriptor) []TemplateVersionVaultDescriptor {
	result := slices.Clone(values)
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func validateTemplateVersionVaults(values []TemplateVersionVaultDescriptor) error {
	seen := make(map[int]struct{}, len(values))
	for _, vault := range values {
		if vault.ID <= 0 {
			return errors.New("template version vault ID is invalid")
		}
		if _, duplicate := seen[vault.ID]; duplicate {
			return errors.New("template version vault ID is duplicated")
		}
		seen[vault.ID] = struct{}{}
		switch vault.Type {
		case TemplateVaultPassword:
			if vault.VaultKeyID == nil || *vault.VaultKeyID <= 0 || vault.Script != nil {
				return errors.New("template version password vault descriptor is invalid")
			}
		case TemplateVaultScript:
			if vault.VaultKeyID != nil || vault.Script == nil || len([]byte(*vault.Script)) > MaxTemplateVersionVaultScriptBytes {
				return errors.New("template version script vault descriptor is invalid")
			}
		default:
			return errors.New("template version vault type is invalid")
		}
		if vault.Name != nil && len([]byte(*vault.Name)) > MaxTemplateVersionVaultNameBytes {
			return errors.New("template version vault name is invalid")
		}
	}
	return nil
}

func isTemplateVersionFingerprint(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
