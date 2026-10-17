package export

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/semaphoreui/semaphore/db"
)

type TemplateRoleExporter struct {
	ValueMap[db.TemplateRolePerm]
}

type legacyRoleSlugNotFoundError struct{}

func (legacyRoleSlugNotFoundError) Error() string {
	return "legacy template role alias not found"
}

func (e *TemplateRoleExporter) load(store db.Store, exporter DataExporter, progress Progress) (err error) {

	projs, err := exporter.getLoadedKeysInt(Project, GlobalScope)
	if err != nil {
		return err
	}

	for _, projId := range projs {
		templates, err := exporter.getLoadedKeysInt(Template, strconv.Itoa(projId))
		if err != nil {
			return err
		}

		roles := make([]db.TemplateRolePerm, 0)

		for _, template := range templates {
			templateRoles, err := store.GetTemplateRoles(projId, template)
			if err != nil {
				return err
			}
			for i := range templateRoles {
				// Older exports identify built-in and global grants by slug only.
				// Resolve them while the source store is available so dependent
				// exporters can use the stable role-ID mapping during restore.
				if templateRoles[i].RoleID != nil {
					continue
				}
				role, err := store.GetProjectOrGlobalRoleBySlug(projId, templateRoles[i].RoleSlug)
				if err != nil {
					return err
				}
				roleID := role.ID
				templateRoles[i].RoleID = &roleID
			}
			roles = append(roles, templateRoles...)
		}

		err = e.appendValues(roles, strconv.Itoa(projId))
		if err != nil {
			return err
		}
	}

	return nil
}

func (e *TemplateRoleExporter) restore(store db.Store, exporter DataExporter, progress Progress) (err error) {
	return e.restoreValues(store, exporter, progress, e)
}

func (e *TemplateRoleExporter) restoreValue(val EntityObject[db.TemplateRolePerm], store db.Store, exporter DataExporter) (err error) {
	old := val.value
	sourceProjectID := old.ProjectID

	old.TemplateID, err = exporter.getNewKeyInt(Template, val.scope, old.TemplateID)
	if err != nil {
		return err
	}
	old.ProjectID, err = exporter.getNewKeyInt(Project, GlobalScope, old.ProjectID)
	if err != nil {
		return err
	}

	if old.RoleID == nil {
		if old.RoleSlug == "" {
			return fmt.Errorf("legacy template role has no slug")
		}
		// Pre-immutable-ID exports use a slug. Role restoration records a
		// private source-scope slug alias before its values are cleared, so
		// copied custom roles can be found even after their slugs regenerate.
		roleID, resolveErr := legacyRoleSlugAlias(exporter, sourceProjectID, old.RoleSlug)
		if resolveErr == nil {
			newRoleID := db.ProjectRoleID(roleID)
			old.RoleID = &newRoleID
		} else {
			var notFound legacyRoleSlugNotFoundError
			if !errors.As(resolveErr, &notFound) {
				return resolveErr
			}
			role, fallbackErr := store.GetGlobalRoleBySlug(old.RoleSlug)
			if fallbackErr != nil {
				return fmt.Errorf("resolve legacy template role %q: %w", old.RoleSlug, resolveErr)
			}
			fallbackRoleID := role.ID
			old.RoleID = &fallbackRoleID
		}
	} else {
		roleID, mapErr := exporter.getNewKey(Role, GlobalScope, string(*old.RoleID))
		if mapErr != nil {
			return mapErr
		}
		newRoleID := db.ProjectRoleID(roleID)
		old.RoleID = &newRoleID
	}

	newObj, err := store.CreateTemplateRole(old)
	if err != nil {
		return err
	}

	return exporter.mapKeys(e.getName(), val.scope, old.GetDbKey(), newObj.GetDbKey())
}

func legacyRoleSlugAlias(exporter KeyMapper, projectID int, slug string) (EntityKey, error) {
	scopes := []string{legacyRoleSlugScope(&projectID), GlobalScope}
	for _, scope := range scopes {
		roleID, err := exporter.getNewKey(legacyRoleSlugNamespace, scope, legacyRoleSlugKey(slug))
		if err == nil {
			return roleID, nil
		}
	}
	for _, scope := range scopes {
		foldedRoleID, foldedErr := exporter.getNewKey(legacyRoleSlugFoldedNamespace, scope, EntityKey(strings.ToLower(slug)))
		if foldedErr != nil {
			continue
		}
		if foldedRoleID == legacyRoleSlugAmbiguous {
			return "", fmt.Errorf("legacy template role %q is ambiguous in scope %q", slug, scope)
		}
		return foldedRoleID, nil
	}
	return "", legacyRoleSlugNotFoundError{}
}

func (e *TemplateRoleExporter) getName() string {
	return TemplateRole
}

func (e *TemplateRoleExporter) importDependsOn() []string {
	return []string{Role, Template, Project}
}

func (e *TemplateRoleExporter) exportDependsOn() []string {
	return []string{Template, Project}
}
