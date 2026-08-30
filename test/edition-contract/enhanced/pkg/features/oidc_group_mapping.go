package features

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type oidcGroupMappingService struct {
	repository db.OIDCGroupMappingRepository
}

func NewOIDCGroupMappingService(repository db.OIDCGroupMappingRepository) pro_interfaces.OIDCGroupMappingService {
	return &oidcGroupMappingService{repository: repository}
}

func (*oidcGroupMappingService) Available(context.Context) error { return nil }

func (s *oidcGroupMappingService) GroupMappings(
	_ context.Context,
	providerID string,
) ([]pro_interfaces.OIDCGroupMapping, error) {
	mappings, err := s.repository.GetOIDCGroupMappings(normalizeOIDCProviderID(providerID))
	if err != nil {
		return nil, err
	}
	result := make([]pro_interfaces.OIDCGroupMapping, 0, len(mappings))
	for _, mapping := range mappings {
		result = append(result, oidcGroupMappingFromDB(mapping))
	}
	return result, nil
}

func (s *oidcGroupMappingService) SaveGroupMapping(
	_ context.Context,
	request pro_interfaces.OIDCGroupMappingRequest,
) (pro_interfaces.OIDCGroupMapping, error) {
	if !request.ActorIsAdmin {
		return pro_interfaces.OIDCGroupMapping{}, pro_interfaces.ErrOIDCGroupMappingForbidden
	}
	if request.Now.IsZero() {
		return pro_interfaces.OIDCGroupMapping{}, common_errors.NewValidationError("OIDC group mapping time is required")
	}
	mapping := request.Mapping
	if err := pro_interfaces.NormalizeOIDCGroupMapping(&mapping, request.Configuration); err != nil {
		return pro_interfaces.OIDCGroupMapping{}, err
	}
	if mapping.Created.IsZero() {
		mapping.Created = request.Now
	}
	mapping.Updated = request.Now
	saved, err := s.repository.SaveOIDCGroupMapping(
		oidcGroupMappingToDB(mapping), request.ExpectedRevision)
	if errors.Is(err, db.ErrOIDCGroupMappingRevisionConflict) {
		return pro_interfaces.OIDCGroupMapping{}, pro_interfaces.ErrOIDCGroupMappingPreviewStale
	}
	if errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.OIDCGroupMapping{}, common_errors.NewValidationError("OIDC group mapping role target does not exist")
	}
	if err != nil {
		return pro_interfaces.OIDCGroupMapping{}, err
	}
	return oidcGroupMappingFromDB(saved), nil
}

func (s *oidcGroupMappingService) DeleteGroupMapping(
	_ context.Context,
	request pro_interfaces.OIDCGroupMappingDeleteRequest,
) error {
	if !request.ActorIsAdmin {
		return pro_interfaces.ErrOIDCGroupMappingForbidden
	}
	if request.Now.IsZero() {
		return common_errors.NewValidationError("OIDC group mapping time is required")
	}
	err := s.repository.DeleteOIDCGroupMapping(
		normalizeOIDCProviderID(request.ProviderID), strings.ToLower(strings.TrimSpace(request.MappingID)),
		request.ExpectedRevision)
	if errors.Is(err, db.ErrOIDCGroupMappingRevisionConflict) {
		return pro_interfaces.ErrOIDCGroupMappingPreviewStale
	}
	return err
}

func (s *oidcGroupMappingService) PreviewGroupMappings(
	_ context.Context,
	request pro_interfaces.OIDCGroupPreviewRequest,
) (pro_interfaces.OIDCGroupPreview, error) {
	if !request.ActorIsAdmin {
		return pro_interfaces.OIDCGroupPreview{}, pro_interfaces.ErrOIDCGroupMappingForbidden
	}
	preview, err := s.buildOIDCGroupPreview(request)
	if err != nil {
		return pro_interfaces.OIDCGroupPreview{}, err
	}
	_, err = s.saveOIDCGroupDecision(preview, request, "preview", "", nil)
	return preview, err
}

func (s *oidcGroupMappingService) ReconcileGroupMappings(
	_ context.Context,
	request pro_interfaces.OIDCGroupPreviewRequest,
) (pro_interfaces.OIDCGroupPreview, error) {
	if !request.ActorIsAdmin && request.Source != "login" {
		return pro_interfaces.OIDCGroupPreview{}, pro_interfaces.ErrOIDCGroupMappingForbidden
	}
	for attempt := 0; attempt < 2; attempt++ {
		preview, err := s.buildOIDCGroupPreview(request)
		if err != nil {
			return pro_interfaces.OIDCGroupPreview{}, err
		}
		if err = validateApplicableOIDCGroupPreview(preview); err != nil {
			s.recordOIDCGroupFailure(request, preview, err)
			return pro_interfaces.OIDCGroupPreview{}, err
		}
		appliedAt := request.Now
		reconciliation, err := oidcGroupReconciliationFromPreview(
			preview, request, "applied", "", &appliedAt)
		if err != nil {
			return pro_interfaces.OIDCGroupPreview{}, err
		}
		_, err = s.repository.ApplyOIDCGroupReconciliation(
			reconciliation, oidcGroupChangesToDB(preview.Additions), oidcGroupChangesToDB(preview.Removals))
		if errors.Is(err, db.ErrOIDCGroupPreviewStale) && attempt == 0 {
			continue
		}
		if errors.Is(err, db.ErrOIDCGroupPreviewStale) {
			err = pro_interfaces.ErrOIDCGroupMappingPreviewStale
		}
		if errors.Is(err, db.ErrOIDCGroupMappingCollision) ||
			errors.Is(err, db.ErrLastGlobalAdministrator) ||
			errors.Is(err, db.ErrLastProjectAdministrator) {
			err = pro_interfaces.ErrOIDCGroupMappingCollision
		}
		if err != nil {
			s.recordOIDCGroupFailure(request, preview, err)
			return pro_interfaces.OIDCGroupPreview{}, err
		}
		return preview, nil
	}
	return pro_interfaces.OIDCGroupPreview{}, pro_interfaces.ErrOIDCGroupMappingPreviewStale
}

func (s *oidcGroupMappingService) GroupReconciliationHistory(
	_ context.Context,
	providerID string,
	limit int,
) ([]db.OIDCGroupReconciliation, error) {
	return s.repository.GetOIDCGroupReconciliationHistory(normalizeOIDCProviderID(providerID), limit)
}

func (s *oidcGroupMappingService) EffectiveGroupAssignments(
	_ context.Context,
	providerID string,
) ([]pro_interfaces.OIDCRoleAssignment, error) {
	providerID = normalizeOIDCProviderID(providerID)
	assignments, err := s.repository.GetOIDCGroupRoleAssignments(providerID, 0)
	if err != nil {
		return nil, err
	}
	result := make([]pro_interfaces.OIDCRoleAssignment, 0)
	for _, assignment := range assignments {
		if assignment.OwnerKind != "oidc" || assignment.ManagedByProviderID != providerID {
			continue
		}
		result = append(result, oidcGroupAssignmentFromDB(assignment))
	}
	return result, nil
}

func (s *oidcGroupMappingService) buildOIDCGroupPreview(
	request pro_interfaces.OIDCGroupPreviewRequest,
) (pro_interfaces.OIDCGroupPreview, error) {
	request.ProviderID = normalizeOIDCProviderID(request.ProviderID)
	if request.ProviderID == "" || request.UserID <= 0 || request.Now.IsZero() || request.Claim.Revision == "" {
		return pro_interfaces.OIDCGroupPreview{}, common_errors.NewValidationError("OIDC group preview metadata is incomplete")
	}
	if err := pro_interfaces.NormalizeOIDCGroupClaimConfiguration(&request.Configuration); err != nil {
		return pro_interfaces.OIDCGroupPreview{}, err
	}
	mappings, err := s.repository.GetOIDCGroupMappings(request.ProviderID)
	if err != nil {
		return pro_interfaces.OIDCGroupPreview{}, err
	}
	revision, err := s.repository.GetOIDCGroupMappingRevision(request.ProviderID)
	if err != nil {
		return pro_interfaces.OIDCGroupPreview{}, err
	}
	assignments, err := s.repository.GetOIDCGroupRoleAssignments(request.ProviderID, request.UserID)
	if err != nil {
		return pro_interfaces.OIDCGroupPreview{}, err
	}
	previewMappings := make([]pro_interfaces.OIDCGroupMapping, 0, len(mappings))
	for _, mapping := range mappings {
		previewMappings = append(previewMappings, oidcGroupMappingFromDB(mapping))
	}
	previewAssignments := make([]pro_interfaces.OIDCRoleAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		previewAssignments = append(previewAssignments, oidcGroupAssignmentFromDB(assignment))
	}
	return pro_interfaces.ComputeOIDCGroupPreview(pro_interfaces.OIDCGroupPreviewInput{
		ProviderID: request.ProviderID, MappingRevision: revision, CapturedAt: request.Now,
		Configuration: request.Configuration, Claim: request.Claim, Mappings: previewMappings,
		UserID: request.UserID, ExistingAssignments: previewAssignments,
	})
}

func (s *oidcGroupMappingService) saveOIDCGroupDecision(
	preview pro_interfaces.OIDCGroupPreview,
	request pro_interfaces.OIDCGroupPreviewRequest,
	status string,
	errorCode string,
	appliedAt *time.Time,
) (db.OIDCGroupReconciliation, error) {
	reconciliation, err := oidcGroupReconciliationFromPreview(preview, request, status, errorCode, appliedAt)
	if err != nil {
		return db.OIDCGroupReconciliation{}, err
	}
	return s.repository.SaveOIDCGroupReconciliation(reconciliation)
}

func (s *oidcGroupMappingService) recordOIDCGroupFailure(
	request pro_interfaces.OIDCGroupPreviewRequest,
	preview pro_interfaces.OIDCGroupPreview,
	err error,
) {
	if preview.ProviderID == "" {
		preview.ProviderID = normalizeOIDCProviderID(request.ProviderID)
	}
	_, _ = s.saveOIDCGroupDecision(preview, request, "failed", oidcGroupErrorCode(err), nil)
}

func validateApplicableOIDCGroupPreview(preview pro_interfaces.OIDCGroupPreview) error {
	if len(preview.ProtectedAdminViolations) != 0 {
		return pro_interfaces.ErrOIDCGroupProtectedAdministrator
	}
	if len(preview.Collisions) != 0 {
		return pro_interfaces.ErrOIDCGroupMappingCollision
	}
	return nil
}

func normalizeOIDCProviderID(providerID string) string {
	return strings.ToLower(strings.TrimSpace(providerID))
}

func oidcGroupMappingToDB(mapping pro_interfaces.OIDCGroupMapping) db.OIDCGroupMapping {
	var projectID *int
	if mapping.Target.Scope == pro_interfaces.OIDCRoleScopeProject {
		value := mapping.Target.ProjectID
		projectID = &value
	}
	return db.OIDCGroupMapping{
		ID: mapping.ID, ProviderID: mapping.ProviderID, ClaimValue: mapping.ClaimValue,
		TargetScope: string(mapping.Target.Scope), ProjectID: projectID, RoleID: mapping.Target.RoleID,
		Enabled: mapping.Enabled, Revision: mapping.Revision, Created: mapping.Created, Updated: mapping.Updated,
	}
}

func oidcGroupMappingFromDB(mapping db.OIDCGroupMapping) pro_interfaces.OIDCGroupMapping {
	target := pro_interfaces.OIDCRoleTarget{
		Scope: pro_interfaces.OIDCRoleScope(mapping.TargetScope), RoleID: mapping.RoleID,
	}
	if mapping.ProjectID != nil {
		target.ProjectID = *mapping.ProjectID
	}
	return pro_interfaces.OIDCGroupMapping{
		ID: mapping.ID, ProviderID: mapping.ProviderID, ClaimValue: mapping.ClaimValue,
		Target: target, Enabled: mapping.Enabled, Revision: mapping.Revision,
		Created: mapping.Created, Updated: mapping.Updated,
	}
}

func oidcGroupAssignmentFromDB(assignment db.OIDCGroupRoleAssignment) pro_interfaces.OIDCRoleAssignment {
	target := pro_interfaces.OIDCRoleTarget{
		Scope: pro_interfaces.OIDCRoleScope(assignment.TargetScope), RoleID: assignment.RoleID,
	}
	if assignment.ProjectID != nil {
		target.ProjectID = *assignment.ProjectID
	}
	return pro_interfaces.OIDCRoleAssignment{
		UserID: assignment.UserID, Target: target, OwnerKind: assignment.OwnerKind,
		ManagedByProviderID:    assignment.ManagedByProviderID,
		ManagedByMappingID:     assignment.ManagedByMappingID,
		ProtectedAdministrator: assignment.ProtectedAdministrator,
	}
}

func oidcGroupChangesToDB(changes []pro_interfaces.OIDCGroupAssignmentChange) []db.OIDCGroupAssignmentChange {
	result := make([]db.OIDCGroupAssignmentChange, 0, len(changes))
	for _, change := range changes {
		var projectID *int
		if change.Target.Scope == pro_interfaces.OIDCRoleScopeProject {
			value := change.Target.ProjectID
			projectID = &value
		}
		result = append(result, db.OIDCGroupAssignmentChange{
			MappingID: change.MappingID, UserID: change.UserID,
			TargetScope: string(change.Target.Scope), ProjectID: projectID, RoleID: change.Target.RoleID,
		})
	}
	return result
}

func oidcGroupReconciliationFromPreview(
	preview pro_interfaces.OIDCGroupPreview,
	request pro_interfaces.OIDCGroupPreviewRequest,
	status string,
	errorCode string,
	appliedAt *time.Time,
) (db.OIDCGroupReconciliation, error) {
	payload, err := json.Marshal(preview)
	if err != nil {
		return db.OIDCGroupReconciliation{}, fmt.Errorf("encode OIDC group preview: %w", err)
	}
	return db.OIDCGroupReconciliation{
		ProviderID: preview.ProviderID, UserID: request.UserID, Source: request.Source,
		Status: status, Token: preview.Token, MappingRevision: preview.MappingRevision,
		ClaimRevision: preview.ClaimRevision, PreviewJSON: string(payload),
		AdditionCount: len(preview.Additions), RemovalCount: len(preview.Removals),
		UnknownCount: len(preview.UnknownValues), CollisionCount: len(preview.Collisions),
		ProtectedAdminCount: len(preview.ProtectedAdminViolations), ErrorCode: errorCode,
		ActorID: request.ActorID, Created: request.Now, AppliedAt: appliedAt,
	}, nil
}

func oidcGroupErrorCode(err error) string {
	switch {
	case errors.Is(err, pro_interfaces.ErrOIDCGroupMappingPreviewStale):
		return "stale_preview"
	case errors.Is(err, pro_interfaces.ErrOIDCGroupProtectedAdministrator):
		return "protected_administrator"
	case errors.Is(err, pro_interfaces.ErrOIDCGroupMappingCollision):
		return "assignment_collision"
	default:
		return "operation_failed"
	}
}

var _ pro_interfaces.OIDCGroupMappingService = (*oidcGroupMappingService)(nil)
