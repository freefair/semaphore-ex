package server

import (
	"context"
	"fmt"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"strings"
)

type RoleSyncServiceImpl struct {
	oidc     pro_interfaces.OIDCGroupMappingService
	mappings db.RoleMappingStore
}

func NewRoleSyncService(store db.RoleMappingStore) pro_interfaces.RoleSyncService {
	return &RoleSyncServiceImpl{mappings: store}
}
func NewRoleSyncServiceWithOIDC(oidc pro_interfaces.OIDCGroupMappingService) pro_interfaces.RoleSyncService {
	return &RoleSyncServiceImpl{oidc: oidc}
}
func (s *RoleSyncServiceImpl) SyncLoginRoles(userID int, providerID string, claims map[string]any) ([]db.MappedRole, []db.MappedRole, error) {
	if userID <= 0 {
		return nil, nil, db.ErrInvalidOperation
	}
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID == "" {
		return []db.MappedRole{}, []db.MappedRole{}, nil
	}
	configuration, err := roleSyncOIDCConfiguration(providerID)
	if err != nil {
		return nil, nil, err
	}
	claim, err := pro_interfaces.ParseOIDCGroupClaim(claims, configuration)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", pro_interfaces.ErrGroupsClaim, err)
	}
	if !claim.Trusted {
		return []db.MappedRole{}, []db.MappedRole{}, nil
	}
	if s.mappings != nil {
		return s.mappings.SyncMappedRoles(userID, providerID, claim.Values)
	}
	if s.oidc == nil {
		return nil, nil, db.ErrInvalidOperation
	}
	preview, err := s.oidc.ReconcileGroupMappings(context.Background(), pro_interfaces.OIDCGroupPreviewRequest{ProviderID: providerID, Configuration: configuration, Claim: claim, UserID: userID, Source: "login", Now: tz.Now()})
	if err != nil {
		return nil, nil, err
	}
	mappings, err := s.oidc.GroupMappings(context.Background(), providerID)
	if err != nil {
		return nil, nil, err
	}
	return mappedRolesFromChanges(providerID, mappings, preview.Additions), mappedRolesFromChanges(providerID, mappings, preview.Removals), nil
}
func roleSyncOIDCConfiguration(providerID string) (pro_interfaces.OIDCGroupClaimConfiguration, error) {
	if util.Config == nil {
		return pro_interfaces.OIDCGroupClaimConfiguration{}, db.ErrNotFound
	}
	var provider util.OidcProvider
	found := false
	for id, candidate := range util.Config.OidcProviders {
		if strings.ToLower(strings.TrimSpace(id)) != providerID {
			continue
		}
		if found {
			return pro_interfaces.OIDCGroupClaimConfiguration{}, common_errors.NewValidationError("OIDC provider ID is ambiguous after normalization")
		}
		provider, found = candidate, true
	}
	if !found {
		return pro_interfaces.OIDCGroupClaimConfiguration{}, db.ErrNotFound
	}
	path, alias := strings.TrimSpace(provider.GroupClaimPath), strings.TrimSpace(provider.GroupsClaim)
	if path != "" && alias != "" && path != alias {
		return pro_interfaces.OIDCGroupClaimConfiguration{}, common_errors.NewValidationError("group_claim_path and groups_claim must match when both are configured")
	}
	if path == "" {
		path = alias
	}
	c := pro_interfaces.OIDCGroupClaimConfiguration{Path: path, CaseInsensitive: provider.GroupClaimCaseInsensitive, MissingClaimPolicy: pro_interfaces.OIDCMissingClaimPolicy(provider.GroupClaimMissingPolicy)}
	if err := pro_interfaces.NormalizeOIDCGroupClaimConfiguration(&c); err != nil {
		return pro_interfaces.OIDCGroupClaimConfiguration{}, err
	}
	return c, nil
}
func mappedRolesFromChanges(provider string, mappings []pro_interfaces.OIDCGroupMapping, changes []pro_interfaces.OIDCGroupAssignmentChange) []db.MappedRole {
	byID := map[string]pro_interfaces.OIDCGroupMapping{}
	for _, mapping := range mappings {
		byID[mapping.ID] = mapping
	}
	result := make([]db.MappedRole, 0, len(changes))
	for _, change := range changes {
		if change.Target.Scope == pro_interfaces.OIDCRoleScopeProject {
			mapping, ok := byID[change.MappingID]
			if !ok {
				continue
			}
			result = append(result, db.MappedRole{ProjectID: change.Target.ProjectID, UserID: change.UserID, Provider: provider, Group: mapping.ClaimValue, RoleID: db.ProjectRoleID(change.Target.RoleID)})
		}
	}
	return result
}

var _ pro_interfaces.RoleSyncService = (*RoleSyncServiceImpl)(nil)
