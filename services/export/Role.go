package export

import (
	"strconv"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/random"
)

const legacyRoleSlugNamespace = "legacy-role-slug"
const legacyRoleSlugFoldedNamespace = "legacy-role-slug-folded"
const legacyRoleSlugAmbiguous = "__ambiguous__"

func legacyRoleSlugScope(projectID *int) string {
	if projectID == nil {
		return GlobalScope
	}
	return strconv.Itoa(*projectID)
}

func legacyRoleSlugKey(slug string) EntityKey {
	return slug
}

func mapLegacyRoleSlugAliases(exporter KeyMapper, scope string, slug string, roleID EntityKey) error {
	if slug == "" {
		return nil
	}
	if err := exporter.mapKeys(legacyRoleSlugNamespace, scope, legacyRoleSlugKey(slug), roleID); err != nil {
		return err
	}
	foldedKey := EntityKey(strings.ToLower(slug))
	existing, err := exporter.getNewKey(legacyRoleSlugFoldedNamespace, scope, foldedKey)
	if err != nil {
		return exporter.mapKeys(legacyRoleSlugFoldedNamespace, scope, foldedKey, roleID)
	}
	if existing == roleID || existing == legacyRoleSlugAmbiguous {
		return nil
	}
	return exporter.mapKeys(legacyRoleSlugFoldedNamespace, scope, foldedKey, legacyRoleSlugAmbiguous)
}

type RoleExporter struct {
	ValueMap[db.Role]
}

func (e *RoleExporter) load(store db.Store, exporter DataExporter, progress Progress) error {

	projs, err := exporter.getLoadedKeysInt(Project, GlobalScope)
	if err != nil {
		return err
	}

	for _, proj := range projs {
		roles, err := store.GetProjectRoles(proj)
		if err != nil {
			return err
		}
		// Role ids are unique across projects, so every role lives in the global scope.
		err = e.appendValues(roles, GlobalScope)
		if err != nil {
			return err
		}
	}

	roles, err := store.GetGlobalRoles()
	if err != nil {
		return err
	}

	return e.appendValues(roles, GlobalScope)
}

func (e *RoleExporter) restore(store db.Store, exporter DataExporter, progress Progress) (err error) {
	return e.restoreValues(store, exporter, progress, e)
}

func (e *RoleExporter) restoreValue(val EntityObject[db.Role], store db.Store, exporter DataExporter) (err error) {

	old := val.value
	sourceRoleSlugScope := legacyRoleSlugScope(old.ProjectID)
	sourceRoleSlug := old.Slug

	if old.Builtin {
		destRole, err := store.GetGlobalRoleBySlug(old.Slug)
		if err != nil {
			return err
		}
		if err = exporter.mapKeys(e.getName(), val.scope, string(old.ID), string(destRole.ID)); err != nil {
			return err
		}
		return mapLegacyRoleSlugAliases(exporter, sourceRoleSlugScope, sourceRoleSlug, string(destRole.ID))
	}

	old.ProjectID, err = exporter.getNewKeyIntRef(Project, GlobalScope, old.ProjectID, e)
	if err != nil {
		return err
	}
	oldID := old.ID
	// A copied role can live in the same database as its source. Both immutable
	// IDs and role slugs are unique in the role table, so regenerate them before
	// inserting and retain the old-to-new ID mapping for dependent exporters.
	old.ID = db.ProjectRoleID(random.String(32))
	old.Slug = random.String(16)

	newObj, err := store.CreateRole(old)
	if err != nil {
		return err
	}

	if err = exporter.mapKeys(e.getName(), val.scope, string(oldID), string(newObj.ID)); err != nil {
		return err
	}
	return mapLegacyRoleSlugAliases(exporter, sourceRoleSlugScope, sourceRoleSlug, string(newObj.ID))
}

func (e *RoleExporter) exportDependsOn() []string {
	return []string{Project}
}

func (e *RoleExporter) importDependsOn() []string {
	return []string{Project}
}

func (e *RoleExporter) getName() string {
	return Role
}
