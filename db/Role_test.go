package db

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateRole(t *testing.T) {
	tests := []struct {
		name    string
		role    Role
		isNew   bool
		wantErr bool
	}{
		{"valid new role", Role{Slug: "ci_bot-2", Name: "CI"}, true, false},
		{"slug keeps its case", Role{Slug: "Deployer", Name: "D"}, true, false},
		{"longest slug", Role{Slug: strings.Repeat("a", 100), Name: "A"}, true, false},
		{"empty name", Role{Slug: "deployer"}, true, true},
		{"empty slug", Role{Name: "D"}, true, true},
		{"space in slug", Role{Slug: "with space", Name: "D"}, true, true},
		{"non-ASCII slug", Role{Slug: "dé", Name: "D"}, true, true},
		{"slash in slug", Role{Slug: "a/b", Name: "D"}, true, true},
		{"slug too long", Role{Slug: strings.Repeat("a", 101), Name: "A"}, true, true},
		{"reserved slug", Role{Slug: string(ProjectOwner), Name: "D"}, true, true},
		{"reserved slug in other case", Role{Slug: "Task_Runner", Name: "D"}, true, true},
		{"reserved slug all", Role{Slug: "ALL", Name: "D"}, true, true},
		{"unknown permission bit", Role{Slug: "d", Name: "D", Permissions: ProjectUserPermission(1 << 62)}, true, true},
		{"negative permissions", Role{Slug: "d", Name: "D", Permissions: -1}, true, true},
		{"old slug stays editable", Role{Slug: "my role", Name: "D"}, false, false},
		{"update still needs a name", Role{Slug: "my role"}, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRole(tt.role, tt.isNew)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateGlobalRole(t *testing.T) {
	assert.NoError(t, ValidateGlobalRole(Role{ID: "global_auditor", Name: "Auditor", Revision: 1, GlobalPermissions: AllGlobalPermissions}))
	assert.Error(t, ValidateGlobalRole(Role{ID: "global_missing_name", Revision: 1}))
	assert.Error(t, ValidateGlobalRole(Role{ID: "global_unknown", Name: "Unknown", Revision: 1, GlobalPermissions: 1 << 30}))
}

func TestRoleAssignments_Impact(t *testing.T) {
	assigned := RoleAssignments{
		Members:       []RoleMember{{ProjectID: 1, UserID: 1}, {ProjectID: 1, UserID: 2}, {ProjectID: 2, UserID: 1}},
		Grants:        []TemplateRolePerm{{ProjectID: 3}},
		GlobalMembers: 2,
	}

	assert.Equal(t, RoleImpact{Members: 5, Projects: 3, TemplateGrants: 1}, assigned.Impact())
	assert.Equal(t, RoleImpact{}, RoleAssignments{}.Impact())
}

func TestGlobalPermission_Can(t *testing.T) {
	mask := CanManageGlobalUsers | CanReadGlobalAudit
	assert.True(t, mask.Can(CanManageGlobalUsers))
	assert.True(t, mask.Can(CanManageGlobalUsers|CanReadGlobalAudit))
	assert.False(t, mask.Can(CanManageGlobalRoles))
	assert.False(t, mask.Can(CanManageGlobalUsers|CanManageGlobalRoles))
	assert.True(t, AllGlobalPermissions.Can(CanManageGlobalSystem))
	assert.Equal(t, GlobalPermission(511), AllGlobalPermissions, "the bit values are part of the API")
}

func TestGlobalPermissionCatalog_CoversEveryBit(t *testing.T) {
	var covered GlobalPermission
	ids := map[string]bool{}
	for _, definition := range GlobalPermissionCatalog() {
		covered |= definition.Permission
		assert.False(t, ids[definition.ID], "duplicate id %s", definition.ID)
		ids[definition.ID] = true
		assert.NotEmpty(t, definition.Description)
	}
	assert.Equal(t, AllGlobalPermissions, covered)
}

func TestGlobalRoleErrorsRemainDistinct(t *testing.T) {
	assert.NotErrorIs(t, ErrLastGlobalAdministrator, ErrInvalidOperation)
	assert.NotErrorIs(t, ErrGlobalRoleAssigned, ErrInvalidOperation)
	assert.NotErrorIs(t, ErrLastGlobalAdministrator, ErrGlobalRoleAssigned)
	assert.NotErrorIs(t, ErrInvalidOperation, ErrLastGlobalAdministrator)
}
