package api

import (
	"errors"
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/util"
	"net/http"
	"strconv"
	"strings"
)

type RoleMappingsController struct {
	mappings db.RoleMappingStore
	store    db.Store
}

type roleMappingListItem struct {
	db.RoleMapping
	ProviderConfigured bool `json:"provider_configured"`
}

func NewRoleMappingsController(m db.RoleMappingStore, store db.Store) *RoleMappingsController {
	return &RoleMappingsController{mappings: m, store: store}
}
func (c *RoleMappingsController) GetMappings(w http.ResponseWriter, _ *http.Request) {
	m, e := c.mappings.GetRoleMappings()
	if e != nil {
		helpers.WriteError(w, e)
		return
	}
	for i := range m {
		if project, err := c.store.GetProject(m[i].ProjectID); err == nil {
			m[i].ProjectName = project.Name
		}
		if strings.HasPrefix(string(m[i].RoleID), "builtin:") {
			m[i].RoleSlug = strings.TrimPrefix(string(m[i].RoleID), "builtin:")
			m[i].RoleName = strings.ReplaceAll(strings.Title(strings.ReplaceAll(m[i].RoleSlug, "_", " ")), "_", " ")
			continue
		}
		if role, err := c.store.GetProjectRoleByID(m[i].ProjectID, m[i].RoleID); err == nil {
			m[i].RoleSlug, m[i].RoleName = role.Slug, role.Name
		}
	}
	items := make([]roleMappingListItem, 0, len(m))
	for _, mapping := range m {
		items = append(items, roleMappingListItem{
			RoleMapping:        mapping,
			ProviderConfigured: roleMappingProviderConfigured(mapping.Provider),
		})
	}
	helpers.WriteJSON(w, http.StatusOK, items)
}

func roleMappingProviderConfigured(providerID string) bool {
	if util.Config == nil {
		return false
	}
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	var provider util.OidcProvider
	found := false
	for configuredID, candidate := range util.Config.OidcProviders {
		if strings.ToLower(strings.TrimSpace(configuredID)) != providerID {
			continue
		}
		if found {
			return false
		}
		provider, found = candidate, true
	}
	if !found {
		return false
	}
	path := strings.TrimSpace(provider.GroupClaimPath)
	alias := strings.TrimSpace(provider.GroupsClaim)
	if path != "" && alias != "" && path != alias {
		return false
	}
	if path == "" {
		path = alias
	}
	configuration := pro_interfaces.OIDCGroupClaimConfiguration{
		Path: path, CaseInsensitive: provider.GroupClaimCaseInsensitive,
		MissingClaimPolicy: pro_interfaces.OIDCMissingClaimPolicy(provider.GroupClaimMissingPolicy),
	}
	return pro_interfaces.NormalizeOIDCGroupClaimConfiguration(&configuration) == nil
}
func (c *RoleMappingsController) AddMapping(w http.ResponseWriter, r *http.Request) {
	var m db.RoleMapping
	if !helpers.Bind(w, r, &m) {
		return
	}
	if m.RoleID == "" {
		switch m.RoleSlug {
		case "owner", "manager", "task_runner", "guest":
			m.RoleID = db.ProjectRoleID("builtin:" + m.RoleSlug)
		default:
			role, err := c.store.GetProjectRole(m.ProjectID, m.RoleSlug)
			if err != nil {
				helpers.WriteError(w, err)
				return
			}
			m.RoleID = role.ID
		}
	}
	out, e := c.mappings.CreateRoleMapping(m)
	if e != nil {
		writeRoleMappingError(w, e)
		return
	}
	c.recordRoleMappingAudit(r, audit.IAMRoleMappingCreate, out)
	helpers.WriteJSON(w, http.StatusCreated, out)
}
func (c *RoleMappingsController) DeleteMapping(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.Atoi(mux.Vars(r)["mapping_id"])
	if e != nil || id <= 0 {
		helpers.WriteErrorStatus(w, "invalid role mapping ID", http.StatusBadRequest)
		return
	}
	m, removed, e := c.mappings.DeleteRoleMapping(id)
	if e != nil {
		writeRoleMappingError(w, e)
		return
	}
	c.recordRoleMappingAudit(r, audit.IAMRoleMappingDelete, m)
	role := c.roleRef(m)
	for index := range removed {
		removed[index].RoleSlug = role.Slug
		helpers.Audit(r).Record(r.Context(), audit.NewMappedRoleEvent(
			audit.IAMMappedRoleRemove,
			removed[index],
			audit.MappedRoleCauseMappingDeleted,
		))
	}
	helpers.WriteJSON(w, http.StatusOK, struct {
		Mapping db.RoleMapping  `json:"mapping"`
		Removed []db.MappedRole `json:"removed"`
	}{m, removed})
}

func writeRoleMappingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrOIDCGroupMappingRevisionConflict):
		helpers.WriteErrorStatus(w, "ROLE_MAPPING_REVISION_CONFLICT", http.StatusConflict)
	case errors.Is(err, db.ErrLastProjectAdministrator), errors.Is(err, db.ErrLastGlobalAdministrator):
		helpers.WriteErrorStatus(w, "ROLE_MAPPING_PROTECTED_ADMINISTRATOR", http.StatusConflict)
	case errors.Is(err, db.ErrOIDCGroupMappingCollision):
		helpers.WriteErrorStatus(w, "ROLE_MAPPING_OWNERSHIP_CONFLICT", http.StatusConflict)
	default:
		helpers.WriteError(w, err)
	}
}

func (c *RoleMappingsController) recordRoleMappingAudit(r *http.Request, kind audit.Kind, mapping db.RoleMapping) {
	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      kind,
		Target:    audit.ResourceTarget(audit.TargetRoleMapping, mapping.ID, audit.TruncateName(mapping.Group, audit.MaxNameBytes)),
		ProjectID: mapping.ProjectID,
		Metadata: audit.RoleMappingMetadata{
			Provider: mapping.Provider,
			Group:    mapping.Group,
			Role:     c.roleRef(mapping),
		},
	})
}

func (c *RoleMappingsController) roleRef(mapping db.RoleMapping) audit.RoleRef {
	if strings.HasPrefix(string(mapping.RoleID), "builtin:") {
		return audit.RoleRef{ID: mapping.RoleID, Slug: strings.TrimPrefix(string(mapping.RoleID), "builtin:")}
	}
	if role, err := c.store.GetProjectRoleByID(mapping.ProjectID, mapping.RoleID); err == nil {
		return audit.NewRoleRef(role)
	}
	return audit.RoleRef{ID: mapping.RoleID, Slug: audit.TruncateName(mapping.RoleSlug, audit.MaxNameBytes)}
}
