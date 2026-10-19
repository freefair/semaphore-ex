package sql

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/tz"
	features "github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"strings"
)

type RoleMappingStoreImpl struct{ store db.Store }

func NewRoleMappingStore(store db.Store) *RoleMappingStoreImpl { return &RoleMappingStoreImpl{store} }
func compatibilityID(p, id string) int {
	d := sha256.Sum256([]byte(p + "\x00" + id))
	n := binary.BigEndian.Uint32(d[:4]) & 0x7fffffff
	if n == 0 {
		return 1
	}
	return int(n)
}
func mappingFromOIDC(m db.OIDCGroupMapping) (db.RoleMapping, bool) {
	if !strings.HasPrefix(m.ID, "upstream-role-") || m.TargetScope != "project" || m.ProjectID == nil {
		return db.RoleMapping{}, false
	}
	return db.RoleMapping{ID: compatibilityID(m.ProviderID, m.ID), Provider: m.ProviderID, Group: m.ClaimValue, ProjectID: *m.ProjectID, RoleID: db.ProjectRoleID(m.RoleID), Created: m.Created}, true
}
func mappingID(m db.RoleMapping) string {
	d := sha256.Sum256([]byte(strings.Join([]string{m.Provider, m.Group, fmt.Sprint(m.ProjectID), string(m.RoleID)}, "\x00")))
	return fmt.Sprintf("upstream-role-%x", d[:16])
}

func (c *RoleMappingStoreImpl) compatibilityMappings() ([]db.OIDCGroupMapping, error) {
	connectionStore, ok := c.store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok || connectionStore.GetConnection() == nil {
		return nil, db.ErrNotFound
	}
	mappings := make([]db.OIDCGroupMapping, 0)
	_, err := connectionStore.GetConnection().SelectAll(&mappings, connectionStore.GetConnection().PrepareQuery(
		"select * from oidc_group_mapping order by provider_id, id"))
	return mappings, err
}

func (c *RoleMappingStoreImpl) GetRoleMappings() ([]db.RoleMapping, error) {
	out := []db.RoleMapping{}
	mappings, err := c.compatibilityMappings()
	if err != nil {
		return nil, err
	}
	for _, mapping := range mappings {
		if value, ok := mappingFromOIDC(mapping); ok {
			out = append(out, value)
		}
	}
	return out, nil
}
func (c *RoleMappingStoreImpl) CreateRoleMapping(m db.RoleMapping) (db.RoleMapping, error) {
	m.Provider = strings.ToLower(strings.TrimSpace(m.Provider))
	m.Group = strings.TrimSpace(m.Group)
	if m.Provider == "" || m.Group == "" || m.ProjectID <= 0 || m.RoleID == "" {
		return db.RoleMapping{}, db.ErrInvalidOperation
	}
	var provider util.OidcProvider
	found := false
	for id, p := range util.Config.OidcProviders {
		if strings.EqualFold(id, m.Provider) {
			if found {
				return db.RoleMapping{}, db.ErrInvalidOperation
			}
			provider, found = p, true
		}
	}
	if !found {
		return db.RoleMapping{}, db.ErrNotFound
	}
	path, alias := strings.TrimSpace(provider.GroupClaimPath), strings.TrimSpace(provider.GroupsClaim)
	if path != "" && alias != "" && path != alias {
		return db.RoleMapping{}, db.ErrInvalidOperation
	}
	if path == "" {
		path = alias
	}
	cfg := pro_interfaces.OIDCGroupClaimConfiguration{Path: path, CaseInsensitive: provider.GroupClaimCaseInsensitive, MissingClaimPolicy: pro_interfaces.OIDCMissingClaimPolicy(provider.GroupClaimMissingPolicy)}
	candidate := pro_interfaces.OIDCGroupMapping{ID: "upstream-role-pending", ProviderID: m.Provider, ClaimValue: m.Group, Enabled: true, Target: pro_interfaces.OIDCRoleTarget{Scope: pro_interfaces.OIDCRoleScopeProject, ProjectID: m.ProjectID, RoleID: string(m.RoleID)}}
	if e := pro_interfaces.NormalizeOIDCGroupMapping(&candidate, cfg); e != nil {
		return db.RoleMapping{}, e
	}
	m.Provider, m.Group = candidate.ProviderID, candidate.ClaimValue
	p, now := m.ProjectID, tz.Now()
	saved, e := c.store.SaveOIDCGroupMapping(db.OIDCGroupMapping{ID: mappingID(m), ProviderID: m.Provider, ClaimValue: m.Group, TargetScope: "project", ProjectID: &p, RoleID: string(m.RoleID), Enabled: true, Revision: 1, Created: now, Updated: now}, 0)
	if e != nil {
		return db.RoleMapping{}, e
	}
	out, ok := mappingFromOIDC(saved)
	if !ok {
		return db.RoleMapping{}, db.ErrInvalidOperation
	}
	return out, nil
}
func (c *RoleMappingStoreImpl) DeleteRoleMapping(id int) (db.RoleMapping, []db.MappedRole, error) {
	var found *db.OIDCGroupMapping
	var mapping db.RoleMapping
	mappings, err := c.compatibilityMappings()
	if err != nil {
		return db.RoleMapping{}, nil, err
	}
	for _, candidate := range mappings {
		value, ok := mappingFromOIDC(candidate)
		if ok && value.ID == id {
			if found != nil {
				return db.RoleMapping{}, nil, db.ErrInvalidOperation
			}
			copy := candidate
			found, mapping = &copy, value
		}
	}
	if found == nil {
		return db.RoleMapping{}, nil, db.ErrNotFound
	}
	assignments, e := c.store.GetOIDCGroupRoleAssignments(found.ProviderID, 0)
	if e != nil {
		return db.RoleMapping{}, nil, e
	}
	removed := make([]db.MappedRole, 0)
	for _, assignment := range assignments {
		if assignment.ManagedByMappingID == found.ID && assignment.TargetScope == "project" && assignment.ProjectID != nil {
			user, err := c.store.GetUser(assignment.UserID)
			if err != nil {
				return db.RoleMapping{}, nil, err
			}
			removed = append(removed, db.MappedRole{ProjectID: *assignment.ProjectID, UserID: assignment.UserID, Username: user.Username, MappingID: mapping.ID, Provider: found.ProviderID, Group: found.ClaimValue, RoleID: db.ProjectRoleID(assignment.RoleID)})
		}
	}
	if e = c.store.DeleteOIDCGroupMappingAndRevoke(found.ProviderID, found.ID, found.Revision); e != nil {
		return db.RoleMapping{}, nil, e
	}
	return mapping, removed, nil
}
func (c *RoleMappingStoreImpl) SyncMappedRoles(userID int, providerID string, groups []string) ([]db.MappedRole, []db.MappedRole, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if userID <= 0 || providerID == "" {
		return nil, nil, db.ErrInvalidOperation
	}
	var provider util.OidcProvider
	found := false
	for id, candidate := range util.Config.OidcProviders {
		if strings.EqualFold(id, providerID) {
			if found {
				return nil, nil, db.ErrInvalidOperation
			}
			provider, found = candidate, true
		}
	}
	if !found {
		return nil, nil, db.ErrNotFound
	}
	path, alias := strings.TrimSpace(provider.GroupClaimPath), strings.TrimSpace(provider.GroupsClaim)
	if path != "" && alias != "" && path != alias {
		return nil, nil, db.ErrInvalidOperation
	}
	if path == "" {
		path = alias
	}
	configuration := pro_interfaces.OIDCGroupClaimConfiguration{Path: path, CaseInsensitive: provider.GroupClaimCaseInsensitive, MissingClaimPolicy: pro_interfaces.OIDCMissingClaimPolicy(provider.GroupClaimMissingPolicy)}
	if err := pro_interfaces.NormalizeOIDCGroupClaimConfiguration(&configuration); err != nil {
		return nil, nil, err
	}
	claimMap := map[string]any{}
	cursor := claimMap
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		next := map[string]any{}
		cursor[part] = next
		cursor = next
	}
	values := make([]any, len(groups))
	for i := range groups {
		values[i] = groups[i]
	}
	cursor[parts[len(parts)-1]] = values
	claim, err := pro_interfaces.ParseOIDCGroupClaim(claimMap, configuration)
	if err != nil {
		return nil, nil, err
	}
	service := features.NewOIDCGroupMappingService(c.store)
	preview, err := service.ReconcileGroupMappings(context.Background(), pro_interfaces.OIDCGroupPreviewRequest{ProviderID: providerID, Configuration: configuration, Claim: claim, UserID: userID, Source: "login", Now: tz.Now()})
	if err != nil {
		return nil, nil, err
	}
	mappings, err := service.GroupMappings(context.Background(), providerID)
	if err != nil {
		return nil, nil, err
	}
	byID := map[string]pro_interfaces.OIDCGroupMapping{}
	for _, m := range mappings {
		byID[m.ID] = m
	}
	convert := func(changes []pro_interfaces.OIDCGroupAssignmentChange) []db.MappedRole {
		out := []db.MappedRole{}
		for _, change := range changes {
			m, ok := byID[change.MappingID]
			if ok && change.Target.Scope == pro_interfaces.OIDCRoleScopeProject {
				out = append(out, db.MappedRole{ProjectID: change.Target.ProjectID, UserID: change.UserID, Provider: providerID, Group: m.ClaimValue, RoleID: db.ProjectRoleID(change.Target.RoleID)})
			}
		}
		return out
	}
	return convert(preview.Additions), convert(preview.Removals), nil
}

var _ db.RoleMappingStore = (*RoleMappingStoreImpl)(nil)
