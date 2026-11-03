package projects

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/semaphoreui/semaphore/util"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
	projectService "github.com/semaphoreui/semaphore/services/project"
	log "github.com/sirupsen/logrus"
)

// BackupController serves project backup/restore. Workflows live outside
// db.Store, so the workflow store is injected and threaded into the
// backup/restore routines.
type BackupController struct {
	workflowStore db.WorkflowManager
}

func NewBackupController(workflowStore db.WorkflowManager) *BackupController {
	return &BackupController{workflowStore: workflowStore}
}

// canExportProject checks all current objects before generating a backup.
func (c *BackupController) canExportProject(r *http.Request, projectID int) bool {
	user := helpers.UserFromContext(r)
	if user == nil {
		return false
	}
	if user.Admin {
		return true
	}
	if c.workflowStore == nil {
		return false
	}
	store := helpers.Store(r)
	templates, err := store.GetTemplates(projectID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	if err != nil {
		return false
	}
	workflows, err := c.workflowStore.GetWorkflowTemplates(projectID, nil, db.RetrieveQueryParams{})
	if err != nil {
		return false
	}
	templateIDs := make([]int, 0, len(templates))
	workflowIDs := make([]int, 0, len(workflows))
	for _, template := range templates {
		templateIDs = append(templateIDs, template.ID)
	}
	for _, workflow := range workflows {
		workflowIDs = append(workflowIDs, workflow.ID)
	}
	return c.canExportObjects(r, projectID, templateIDs, workflowIDs)
}

// canExportObjects rechecks the exact objects in the generated backup, so a
// deletion or permission change during generation fails closed.
func (c *BackupController) canExportObjects(r *http.Request, projectID int, templateIDs, workflowIDs []int) bool {
	user := helpers.UserFromContext(r)
	if user == nil {
		return false
	}
	if user.Admin {
		return true
	}
	if c.workflowStore == nil {
		return false
	}
	store := helpers.Store(r)
	member, err := store.GetProjectMember(projectID, user.ID)
	if err != nil {
		return false
	}
	var base db.ProjectUserPermission
	for _, role := range member.Roles {
		base |= role.Permissions
	}
	for _, role := range member.MappedRoles {
		base |= role.RolePermissions
	}
	if !base.Can(db.CanReadProject | db.CanViewProjectResources) {
		return false
	}
	for _, id := range templateIDs {
		if _, err = store.GetTemplate(projectID, id); err != nil {
			return false
		}
		permission, permissionErr := store.GetTemplatePermissionContext(projectID, id, user.ID)
		if permissionErr != nil || !permission.EffectivePermissions.Can(db.CanReadTemplate) {
			return false
		}
	}
	for _, id := range workflowIDs {
		workflow, workflowErr := c.workflowStore.GetWorkflowTemplate(projectID, id)
		if workflowErr != nil {
			return false
		}
		visible, _, permissionErr := AuthorizeWorkflowRequest(r, workflow, pro_interfaces.PermissionViewWorkflow)
		if permissionErr != nil || !visible {
			return false
		}
	}
	return true
}

func (c *BackupController) canExportSnapshot(r *http.Request, projectID int, backup *projectService.BackupFormat) bool {
	if backup == nil {
		return false
	}
	templateIDs := make([]int, 0, len(backup.Templates))
	workflowIDs := make([]int, 0, len(backup.Workflows))
	for _, template := range backup.Templates {
		templateIDs = append(templateIDs, template.ID)
	}
	for _, workflow := range backup.Workflows {
		workflowIDs = append(workflowIDs, workflow.ID)
	}
	return c.canExportObjects(r, projectID, templateIDs, workflowIDs)
}

func (c *BackupController) GetBackup(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)

	if !c.canExportProject(r, project.ID) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	store := helpers.Store(r)

	backup, err := projectService.GetBackup(project.ID, store, c.workflowStore)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	if !c.canExportSnapshot(r, project.ID, backup) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	str, err := backup.Marshal()
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      audit.ResourceProjectBackupExport,
		Target:    audit.ResourceTarget(audit.TargetProject, project.ID, project.Name),
		ProjectID: project.ID,
	})

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(str))
}

func (c *BackupController) Restore(w http.ResponseWriter, r *http.Request) {
	user := helpers.GetFromContext(r, "user").(*db.User)

	if !user.Admin && !util.Config.NonAdminCanCreateProject {
		log.Warn(user.Username + " is not permitted to restore the project")
		helpers.RecordDenied(r, "admin", 0)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var backup projectService.BackupFormat

	buf := new(strings.Builder)
	if _, err := io.Copy(buf, r.Body); err != nil {
		log.Error(err)
		helpers.WriteError(w, err)
		return
	}

	str := buf.String()

	if err := backup.Unmarshal(str); err != nil {
		log.WithFields(log.Fields{
			"context": "backup_api",
		}).WithError(err).Error("failed to unmarshal backup")
		helpers.WriteError(w, err)
		return
	}

	store := helpers.Store(r)

	if err := backup.Verify(); err != nil {
		log.WithFields(log.Fields{
			"context": "backup_api",
		}).WithError(err).Error("failed to verify backup")
		helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
		return
	}

	p, err := backup.Restore(*user, store, c.workflowStore)
	if err != nil {
		log.WithFields(log.Fields{
			"context": "backup_api",
		}).WithError(err).Error("failed to restore backup")
		if cleanup, ok := errors.AsType[*projectService.CleanupError](err); ok {
			writeCleanupFailure(w, cleanup.Err, cleanup.ProjectID)
			return
		}
		if _, ok := errors.AsType[*common_errors.ValidationError](err); ok {
			helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
			return
		}
		helpers.WriteError(w, err)
		return
	}

	integrationAliases := len(backup.IntegrationAliases)
	for _, integration := range backup.Integration {
		integrationAliases += len(integration.Aliases)
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      audit.ResourceProjectBackupRestore,
		Target:    audit.ResourceTarget(audit.TargetProject, p.ID, p.Name),
		ProjectID: p.ID,
		Metadata: audit.BackupRestoreMetadata{Objects: map[string]int{
			"templates":           len(backup.Templates),
			"repositories":        len(backup.Repositories),
			"host_configs":        len(backup.HostConfigs),
			"keys":                len(backup.Keys),
			"views":               len(backup.Views),
			"inventories":         len(backup.Inventories),
			"environments":        len(backup.Environments),
			"integrations":        len(backup.Integration),
			"integration_aliases": integrationAliases,
			"schedules":           len(backup.Schedules),
			"secret_storages":     len(backup.SecretStorages),
			"roles":               len(backup.Roles),
			"runners":             len(backup.Runners),
			"workflows":           len(backup.Workflows),
		}},
	})

	helpers.WriteJSON(w, http.StatusOK, p)
}
