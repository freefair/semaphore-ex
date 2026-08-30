package features

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func (s *ldapService) GroupMappings(
	_ context.Context,
	providerID string,
) ([]pro_interfaces.LDAPGroupMapping, error) {
	mappings, err := s.repository.GetLDAPGroupMappings(strings.ToLower(strings.TrimSpace(providerID)))
	if err != nil {
		return nil, err
	}
	result := make([]pro_interfaces.LDAPGroupMapping, 0, len(mappings))
	for _, mapping := range mappings {
		result = append(result, ldapGroupMappingFromDB(mapping))
	}
	return result, nil
}

func (s *ldapService) SaveGroupMapping(
	_ context.Context,
	request pro_interfaces.LDAPGroupMappingRequest,
) (pro_interfaces.LDAPGroupMapping, error) {
	if !request.ActorIsAdmin {
		return pro_interfaces.LDAPGroupMapping{}, pro_interfaces.ErrLDAPForbidden
	}
	if request.Now.IsZero() {
		return pro_interfaces.LDAPGroupMapping{}, common_errors.NewValidationError("LDAP group mapping time is required")
	}
	mapping := request.Mapping
	if err := pro_interfaces.NormalizeLDAPGroupMapping(&mapping); err != nil {
		return pro_interfaces.LDAPGroupMapping{}, err
	}
	if _, err := s.repository.GetLDAPProvider(mapping.ProviderID); errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.LDAPGroupMapping{}, pro_interfaces.ErrLDAPProviderNotFound
	} else if err != nil {
		return pro_interfaces.LDAPGroupMapping{}, err
	}
	if mapping.Created.IsZero() {
		mapping.Created = request.Now
	}
	mapping.Updated = request.Now
	saved, err := s.repository.SaveLDAPGroupMapping(
		ldapGroupMappingToDB(mapping), request.ExpectedRevision)
	if errors.Is(err, db.ErrLDAPGroupMappingRevisionConflict) {
		return pro_interfaces.LDAPGroupMapping{}, pro_interfaces.ErrLDAPGroupPreviewStale
	}
	if errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.LDAPGroupMapping{}, common_errors.NewValidationError("LDAP group mapping role target does not exist")
	}
	if err != nil {
		return pro_interfaces.LDAPGroupMapping{}, err
	}
	return ldapGroupMappingFromDB(saved), nil
}

func (s *ldapService) DeleteGroupMapping(
	_ context.Context,
	request pro_interfaces.LDAPGroupMappingDeleteRequest,
) error {
	if !request.ActorIsAdmin {
		return pro_interfaces.ErrLDAPForbidden
	}
	if request.Now.IsZero() {
		return common_errors.NewValidationError("LDAP group mapping deletion time is required")
	}
	err := s.repository.DeleteLDAPGroupMapping(
		strings.ToLower(strings.TrimSpace(request.ProviderID)),
		strings.ToLower(strings.TrimSpace(request.MappingID)),
		request.ExpectedRevision,
	)
	if errors.Is(err, db.ErrLDAPGroupMappingRevisionConflict) {
		return pro_interfaces.ErrLDAPGroupPreviewStale
	}
	return err
}

func (s *ldapService) PreviewGroupMappings(
	ctx context.Context,
	request pro_interfaces.LDAPGroupPreviewRequest,
) (pro_interfaces.LDAPGroupPreview, error) {
	preview, err := s.computeLDAPGroupPreview(ctx, request)
	if err != nil {
		s.recordLDAPGroupFailure(request, err)
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	if _, err = s.saveLDAPGroupPreview(preview, request, "preview", ""); err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	return preview, nil
}

func (s *ldapService) ApplyGroupPreview(
	ctx context.Context,
	request pro_interfaces.LDAPGroupApplyRequest,
) (pro_interfaces.LDAPGroupPreview, error) {
	if !request.ActorIsAdmin {
		return pro_interfaces.LDAPGroupPreview{}, pro_interfaces.ErrLDAPForbidden
	}
	if request.Now.IsZero() || strings.TrimSpace(request.Token) == "" {
		return pro_interfaces.LDAPGroupPreview{}, common_errors.NewValidationError("LDAP group apply request is incomplete")
	}
	providerID := strings.ToLower(strings.TrimSpace(request.ProviderID))
	stored, err := s.repository.GetLDAPGroupPreview(providerID, request.Token)
	if errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.LDAPGroupPreview{}, pro_interfaces.ErrLDAPGroupPreviewStale
	}
	if err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	var storedPreview pro_interfaces.LDAPGroupPreview
	if err = json.Unmarshal([]byte(stored.PreviewJSON), &storedPreview); err != nil {
		return pro_interfaces.LDAPGroupPreview{}, fmt.Errorf("decode LDAP group preview: %w", err)
	}
	actorID := request.ActorID
	freshRequest := pro_interfaces.LDAPGroupPreviewRequest{
		ActorID: &actorID, ActorIsAdmin: true, ProviderID: providerID,
		Source: "manual", Now: request.Now,
	}
	fresh, err := s.computeLDAPGroupPreview(ctx, freshRequest)
	if err != nil {
		s.recordLDAPGroupFailure(freshRequest, err)
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	if storedPreview.Token != request.Token || fresh.Token != request.Token {
		_, _ = s.saveLDAPGroupPreview(fresh, freshRequest, "stale", "directory_or_mapping_changed")
		return fresh, pro_interfaces.ErrLDAPGroupPreviewStale
	}
	if err = validateApplicableLDAPGroupPreview(fresh); err != nil {
		_, _ = s.saveLDAPGroupPreview(fresh, freshRequest, "blocked", ldapGroupErrorCode(err))
		return fresh, err
	}
	return s.applyLDAPGroupPreview(fresh, freshRequest)
}

func (s *ldapService) ReconcileGroupMappings(
	ctx context.Context,
	request pro_interfaces.LDAPGroupPreviewRequest,
) (pro_interfaces.LDAPGroupPreview, error) {
	if request.Source != "manual" && request.Source != "scheduled" && request.Source != "login" {
		return pro_interfaces.LDAPGroupPreview{}, common_errors.NewValidationError("invalid LDAP group reconciliation source")
	}
	if request.Source == "manual" && !request.ActorIsAdmin {
		return pro_interfaces.LDAPGroupPreview{}, pro_interfaces.ErrLDAPForbidden
	}
	if request.Now.IsZero() {
		return pro_interfaces.LDAPGroupPreview{}, common_errors.NewValidationError("LDAP group reconciliation time is required")
	}
	preview, err := s.computeLDAPGroupPreview(ctx, request)
	if err != nil {
		s.recordLDAPGroupFailure(request, err)
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	if _, err = s.saveLDAPGroupPreview(preview, request, "preview", ""); err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	if err = validateApplicableLDAPGroupPreview(preview); err != nil {
		_, _ = s.saveLDAPGroupPreview(preview, request, "blocked", ldapGroupErrorCode(err))
		return preview, err
	}
	return s.applyLDAPGroupPreview(preview, request)
}

func (s *ldapService) GroupReconciliationHistory(
	_ context.Context,
	providerID string,
	limit int,
) ([]db.LDAPGroupReconciliation, error) {
	return s.repository.GetLDAPGroupReconciliationHistory(
		strings.ToLower(strings.TrimSpace(providerID)), limit)
}

func (s *ldapService) computeLDAPGroupPreview(
	ctx context.Context,
	request pro_interfaces.LDAPGroupPreviewRequest,
) (pro_interfaces.LDAPGroupPreview, error) {
	if request.Source == "manual" && !request.ActorIsAdmin {
		return pro_interfaces.LDAPGroupPreview{}, pro_interfaces.ErrLDAPForbidden
	}
	providerID := strings.ToLower(strings.TrimSpace(request.ProviderID))
	provider, err := s.repository.GetLDAPProvider(providerID)
	if errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.LDAPGroupPreview{}, pro_interfaces.ErrLDAPProviderNotFound
	}
	if err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	configuration, cleanup, err := s.clientConfiguration(provider)
	if err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	defer cleanup()
	snapshot, err := s.client.ReadGroupSnapshot(ctx, configuration)
	if err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	mappingRevision, err := s.repository.GetLDAPGroupMappingRevision(providerID)
	if err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	dbMappings, err := s.repository.GetLDAPGroupMappings(providerID)
	if err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	linkedRows, err := s.repository.GetLDAPLinkedUsers(providerID)
	if err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	assignmentRows, err := s.repository.GetLDAPGroupRoleAssignments(providerID)
	if err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}

	mappings := make([]pro_interfaces.LDAPGroupMapping, 0, len(dbMappings))
	for _, mapping := range dbMappings {
		mappings = append(mappings, ldapGroupMappingFromDB(mapping))
	}
	linked := make([]pro_interfaces.LDAPLinkedUser, 0, len(linkedRows))
	for _, user := range linkedRows {
		if request.UserID == nil || user.UserID == *request.UserID {
			linked = append(linked, pro_interfaces.LDAPLinkedUser{ExternalID: user.ExternalID, UserID: user.UserID})
		}
	}
	directoryUsers := snapshot.Users
	if request.UserID != nil {
		allowedExternalIDs := make(map[string]bool)
		for _, user := range linked {
			allowedExternalIDs[strings.ToLower(strings.TrimSpace(user.ExternalID))] = true
		}
		filtered := make([]pro_interfaces.LDAPDirectoryUser, 0, 1)
		for _, user := range directoryUsers {
			if allowedExternalIDs[strings.ToLower(strings.TrimSpace(user.ExternalID))] {
				filtered = append(filtered, user)
			}
		}
		directoryUsers = filtered
	}
	assignments := make([]pro_interfaces.LDAPRoleAssignment, 0, len(assignmentRows))
	for _, assignment := range assignmentRows {
		if request.UserID != nil && assignment.UserID != *request.UserID {
			continue
		}
		target := pro_interfaces.LDAPRoleTarget{
			Scope: pro_interfaces.LDAPRoleScope(assignment.TargetScope), RoleID: assignment.RoleID,
		}
		if assignment.ProjectID != nil {
			target.ProjectID = *assignment.ProjectID
		}
		assignments = append(assignments, pro_interfaces.LDAPRoleAssignment{
			UserID: assignment.UserID, Target: target,
			ManagedByMappingID:     assignment.ManagedByMappingID,
			ProtectedAdministrator: assignment.ProtectedAdministrator,
		})
	}
	return pro_interfaces.ComputeLDAPGroupPreview(pro_interfaces.LDAPGroupPreviewInput{
		ProviderID: providerID, MappingRevision: mappingRevision,
		DirectoryRevision: snapshot.Revision, CapturedAt: snapshot.CapturedAt,
		Mappings: mappings, DirectoryGroupIDs: snapshot.GroupExternalIDs,
		DirectoryUsers: directoryUsers, LinkedUsers: linked, ExistingAssignments: assignments,
	})
}

func (s *ldapService) applyLDAPGroupPreview(
	preview pro_interfaces.LDAPGroupPreview,
	request pro_interfaces.LDAPGroupPreviewRequest,
) (pro_interfaces.LDAPGroupPreview, error) {
	reconciliation, err := ldapGroupReconciliationFromPreview(preview, request, "applied", "")
	if err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	appliedAt := request.Now
	reconciliation.AppliedAt = &appliedAt
	_, err = s.repository.ApplyLDAPGroupPreview(
		reconciliation, ldapGroupChangesToDB(preview.Additions), ldapGroupChangesToDB(preview.Removals))
	if errors.Is(err, db.ErrLDAPGroupPreviewStale) {
		return preview, pro_interfaces.ErrLDAPGroupPreviewStale
	}
	if errors.Is(err, db.ErrLDAPGroupMappingCollision) {
		return preview, pro_interfaces.ErrLDAPGroupMappingCollision
	}
	if errors.Is(err, db.ErrLastGlobalAdministrator) || errors.Is(err, db.ErrLastProjectAdministrator) {
		return preview, pro_interfaces.ErrLDAPGroupProtectedAdministrator
	}
	if err != nil {
		return pro_interfaces.LDAPGroupPreview{}, err
	}
	return preview, nil
}

func (s *ldapService) saveLDAPGroupPreview(
	preview pro_interfaces.LDAPGroupPreview,
	request pro_interfaces.LDAPGroupPreviewRequest,
	status string,
	errorCode string,
) (db.LDAPGroupReconciliation, error) {
	reconciliation, err := ldapGroupReconciliationFromPreview(preview, request, status, errorCode)
	if err != nil {
		return db.LDAPGroupReconciliation{}, err
	}
	return s.repository.SaveLDAPGroupReconciliation(reconciliation)
}

func (s *ldapService) recordLDAPGroupFailure(request pro_interfaces.LDAPGroupPreviewRequest, err error) {
	revision, revisionErr := s.repository.GetLDAPGroupMappingRevision(request.ProviderID)
	if revisionErr != nil {
		return
	}
	_, _ = s.repository.SaveLDAPGroupReconciliation(db.LDAPGroupReconciliation{
		ProviderID: strings.ToLower(strings.TrimSpace(request.ProviderID)), Source: request.Source,
		Status: "stale", MappingRevision: revision, ErrorCode: ldapGroupErrorCode(err),
		ActorID: request.ActorID, Created: request.Now,
	})
}

func validateApplicableLDAPGroupPreview(preview pro_interfaces.LDAPGroupPreview) error {
	if len(preview.ProtectedAdminViolations) != 0 {
		return pro_interfaces.ErrLDAPGroupProtectedAdministrator
	}
	if len(preview.Collisions) != 0 {
		return pro_interfaces.ErrLDAPGroupMappingCollision
	}
	if len(preview.Unresolved) != 0 {
		return pro_interfaces.ErrLDAPGroupUnresolved
	}
	return nil
}

func ldapGroupMappingToDB(mapping pro_interfaces.LDAPGroupMapping) db.LDAPGroupMapping {
	var projectID *int
	if mapping.Target.Scope == pro_interfaces.LDAPRoleScopeProject {
		value := mapping.Target.ProjectID
		projectID = &value
	}
	return db.LDAPGroupMapping{
		ID: mapping.ID, ProviderID: mapping.ProviderID, GroupExternalID: mapping.GroupExternalID,
		TargetScope: string(mapping.Target.Scope), ProjectID: projectID, RoleID: mapping.Target.RoleID,
		Enabled: mapping.Enabled, Revision: mapping.Revision, Created: mapping.Created, Updated: mapping.Updated,
	}
}

func ldapGroupMappingFromDB(mapping db.LDAPGroupMapping) pro_interfaces.LDAPGroupMapping {
	target := pro_interfaces.LDAPRoleTarget{
		Scope: pro_interfaces.LDAPRoleScope(mapping.TargetScope), RoleID: mapping.RoleID,
	}
	if mapping.ProjectID != nil {
		target.ProjectID = *mapping.ProjectID
	}
	return pro_interfaces.LDAPGroupMapping{
		ID: mapping.ID, ProviderID: mapping.ProviderID, GroupExternalID: mapping.GroupExternalID,
		Target: target, Enabled: mapping.Enabled, Revision: mapping.Revision,
		Created: mapping.Created, Updated: mapping.Updated,
	}
}

func ldapGroupChangesToDB(changes []pro_interfaces.LDAPGroupAssignmentChange) []db.LDAPGroupAssignmentChange {
	result := make([]db.LDAPGroupAssignmentChange, 0, len(changes))
	for _, change := range changes {
		var projectID *int
		if change.Target.Scope == pro_interfaces.LDAPRoleScopeProject {
			value := change.Target.ProjectID
			projectID = &value
		}
		result = append(result, db.LDAPGroupAssignmentChange{
			MappingID: change.MappingID, UserID: change.UserID,
			TargetScope: string(change.Target.Scope), ProjectID: projectID, RoleID: change.Target.RoleID,
		})
	}
	return result
}

func ldapGroupReconciliationFromPreview(
	preview pro_interfaces.LDAPGroupPreview,
	request pro_interfaces.LDAPGroupPreviewRequest,
	status string,
	errorCode string,
) (db.LDAPGroupReconciliation, error) {
	payload, err := json.Marshal(preview)
	if err != nil {
		return db.LDAPGroupReconciliation{}, fmt.Errorf("encode LDAP group preview: %w", err)
	}
	return db.LDAPGroupReconciliation{
		ProviderID: preview.ProviderID, Source: request.Source, Status: status, Token: preview.Token,
		MappingRevision: preview.MappingRevision, DirectoryRevision: preview.DirectoryRevision,
		PreviewJSON: string(payload), AdditionCount: len(preview.Additions), RemovalCount: len(preview.Removals),
		UnresolvedCount: len(preview.Unresolved), CollisionCount: len(preview.Collisions),
		ProtectedAdminCount: len(preview.ProtectedAdminViolations), ErrorCode: errorCode,
		ActorID: request.ActorID, Created: request.Now,
	}, nil
}

func ldapGroupErrorCode(err error) string {
	switch {
	case errors.Is(err, pro_interfaces.ErrLDAPProviderUnavailable):
		return "directory_unavailable"
	case errors.Is(err, pro_interfaces.ErrLDAPReferral):
		return "referral_rejected"
	case errors.Is(err, pro_interfaces.ErrLDAPGroupPreviewStale):
		return "stale_preview"
	case errors.Is(err, pro_interfaces.ErrLDAPGroupProtectedAdministrator):
		return "protected_administrator"
	case errors.Is(err, pro_interfaces.ErrLDAPGroupMappingCollision):
		return "assignment_collision"
	case errors.Is(err, pro_interfaces.ErrLDAPGroupUnresolved):
		return "unresolved_directory_item"
	default:
		return "operation_failed"
	}
}

var _ pro_interfaces.LDAPService = (*ldapService)(nil)
