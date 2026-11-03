package project

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/random"
)

func getEntryByName[T BackupEntry](name *string, items []T) *T {
	if name == nil {
		return nil
	}
	for _, o := range items {
		if o.GetName() == *name {
			return &o
		}
	}
	return nil
}

func verifyDuplicate[T BackupEntry](name string, items []T) error {
	n := 0
	for _, o := range items {
		if o.GetName() == name {
			n++
		}
		if n > 2 {
			return fmt.Errorf("%s is duplicate", name)
		}
	}
	return nil
}

func verifyBackupSSHKeyBindings(bindings *[]BackupSSHKeyBinding, keys []BackupAccessKey) error {
	if bindings == nil {
		return nil
	}
	for _, binding := range *bindings {
		if binding.Key == "" || getEntryByName[BackupAccessKey](&binding.Key, keys) == nil {
			return fmt.Errorf("SSH key binding does not exist in keys[].name")
		}
	}
	return nil
}

func restoreBackupSSHKeyBindings(bindings *[]BackupSSHKeyBinding, keys []db.AccessKey) (db.SSHKeyBindings, error) {
	if bindings == nil {
		return nil, nil
	}
	result := make(db.SSHKeyBindings, len(*bindings))
	for i, binding := range *bindings {
		key := findEntityByName[db.AccessKey](&binding.Key, keys)
		if key == nil {
			return nil, fmt.Errorf("SSH key binding does not exist in restored keys")
		}
		result[i] = db.SSHKeyBinding{AccessKeyID: key.ID, Hosts: append([]string(nil), binding.Hosts...)}
	}
	return result, nil
}

func (e BackupSecretStorage) Verify(backup *BackupFormat) error {
	return verifyDuplicate[BackupSecretStorage](e.Name, backup.SecretStorages)
}

func (e BackupSecretStorage) Restore(b *BackupDB) error {
	st := e.SecretStorage
	st.ProjectID = b.meta.ID
	newStorage, err := b.store.CreateSecretStorage(st)
	if err != nil {
		return err
	}
	b.secretStorages = append(b.secretStorages, newStorage)
	return nil
}

// Verify keeps EX role names unique. Semaphore UI format 2 files also carry
// role slugs; those slugs must be unique and must not shadow a built-in role,
// because template grants in such files refer to roles by slug.
func (e BackupRole) Verify(backup *BackupFormat) error {
	if e.UpstreamSlug == nil {
		return verifyDuplicate[BackupRole](e.Name, backup.Roles)
	}
	slug := *e.UpstreamSlug
	if n := len([]rune(slug)); n == 0 || n > 100 {
		return &common_errors.ValidationError{Message: "Role slug must be 1 to 100 characters"}
	}
	if db.IsReservedRoleSlug(slug) {
		return &common_errors.ValidationError{Message: "Role slug is reserved: " + slug}
	}
	n := 0
	for _, other := range backup.Roles {
		if other.UpstreamSlug != nil && strings.EqualFold(*other.UpstreamSlug, slug) {
			n++
		}
	}
	if n > 1 {
		return &common_errors.ValidationError{Message: "Role slug is duplicate: " + slug}
	}
	return nil
}

func (e BackupRole) Restore(b *BackupDB) error {
	role := e.Role
	oldID := e.ID
	role.ID = db.ProjectRoleID(random.String(32))
	if strings.HasPrefix(string(oldID), "builtin:") {
		return fmt.Errorf("backup custom role uses reserved ID: %s", oldID)
	}
	role.ProjectID = &b.meta.ID
	role.Slug = random.String(16)
	if e.UpstreamSlug != nil {
		// Format 2 identifies roles by slug and allows repeated names, while EX
		// names are unique per project.
		role.Name = b.uniqueRoleName(role.Name, *e.UpstreamSlug)
	}
	// Unknown bits are never checked today but would turn on with a future permission.
	if b.upstreamPermissions {
		role.Permissions = db.UpstreamProjectPermissions(role.Permissions, b.version)
	} else {
		role.Permissions &= db.KnownProjectRolePermissions
		if b.version < 4 {
			role.Permissions |= db.CanReadProject | db.CanApproveWorkflows
		}
	}
	newRole, err := b.store.CreateRole(role)
	if err != nil {
		return err
	}
	b.roles = append(b.roles, newRole)
	if b.roleIDMap == nil {
		b.roleIDMap = make(map[db.ProjectRoleID]db.ProjectRoleID)
	}
	b.roleIDMap[oldID] = newRole.ID
	if e.UpstreamSlug != nil {
		if b.roleSlugMap == nil {
			b.roleSlugMap = make(map[string]db.ProjectRoleID)
		}
		b.roleSlugMap[*e.UpstreamSlug] = newRole.ID
	}
	return nil
}

// uniqueRoleName keeps name when no restored role uses it, and otherwise
// appends the file slug, then a counter.
func (b *BackupDB) uniqueRoleName(name, slug string) string {
	taken := func(candidate string) bool {
		return slices.ContainsFunc(b.roles, func(role db.Role) bool { return role.Name == candidate })
	}
	if !taken(name) {
		return name
	}
	candidate := fmt.Sprintf("%s (%s)", name, slug)
	for n := 2; taken(candidate); n++ {
		candidate = fmt.Sprintf("%s (%s %d)", name, slug, n)
	}
	return candidate
}

// unassignedRole finds or adds the report entry for a slug. Use the pointer at once, a later add may move it.
func (b *BackupDB) unassignedRole(slug, reason string) *UnassignedRole {
	for i := range b.unassigned {
		if b.unassigned[i].Slug == slug {
			return &b.unassigned[i]
		}
	}
	b.unassigned = append(b.unassigned, UnassignedRole{Slug: slug, Reason: reason, Templates: []UnassignedTemplate{}, Workflows: []UnassignedWorkflow{}})
	return &b.unassigned[len(b.unassigned)-1]
}

func (e BackupEnvironment) Verify(backup *BackupFormat) error {
	return verifyDuplicate[BackupEnvironment](e.Name, backup.Environments)
}

func (e BackupEnvironment) Restore(b *BackupDB) error {
	env := e.Environment
	env.ProjectID = b.meta.ID
	newEnv, err := b.store.CreateEnvironment(env)
	if err != nil {
		return err
	}
	b.environments = append(b.environments, newEnv)
	return nil
}

func (e BackupView) Verify(backup *BackupFormat) error {
	return verifyDuplicate[BackupView](e.Title, backup.Views)
}

func (e BackupView) Restore(b *BackupDB) error {
	v := e.View
	v.ProjectID = b.meta.ID
	newView, err := b.store.CreateView(v)
	if err != nil {
		return err
	}
	b.views = append(b.views, newView)
	return nil
}

func (e BackupSchedule) Verify(backup *BackupFormat) error {
	return verifyDuplicate[BackupSchedule](e.Name, backup.Schedules)
}

func (e BackupSchedule) Restore(b *BackupDB) error {
	v := e.Schedule
	v.ProjectID = b.meta.ID

	tpl := findEntityByName[db.Template](&e.Template, b.templates)
	if tpl == nil {
		return fmt.Errorf("template does not exist in templates[].name")
	}
	v.TemplateID = tpl.ID

	if e.CheckableRepository != nil {
		repo := findEntityByName[db.Repository](e.CheckableRepository, b.repositories)
		if repo == nil {
			return fmt.Errorf("repo does not exist in repositories[].name")
		}
		v.RepositoryID = &repo.ID
	}

	if e.TaskParams != nil {
		inv := findEntityByName[db.Inventory](e.TaskParams.InventoryName, b.inventories)
		if inv != nil {
			v.TaskParams.InventoryID = &inv.ID
		}
	}

	// Created inactive: Restore activates it only after every entry succeeded.
	v.Active = false
	newSchedule, err := b.store.CreateSchedule(v)
	if err != nil {
		return err
	}

	b.schedules = append(b.schedules, newSchedule)
	return nil
}

func (e BackupAccessKey) Verify(backup *BackupFormat) error {
	return verifyDuplicate[BackupAccessKey](e.Name, backup.Keys)
}

func (e BackupAccessKey) Restore(b *BackupDB) error {

	key := e.AccessKey
	key.ProjectID = &b.meta.ID

	if e.Storage != nil {
		storage := findEntityByName[db.SecretStorage](e.Storage, b.secretStorages)
		if storage == nil {
			return fmt.Errorf("secret storage does not exist in secret_storage[].name")
		}
		key.StorageID = &storage.ID
	}

	if e.SourceStorage != nil {
		sourceStorage := findEntityByName[db.SecretStorage](e.SourceStorage, b.secretStorages)
		if sourceStorage == nil {
			return fmt.Errorf("secret storage does not exist in secret_storage[].name")
		}
		key.SourceStorageID = &sourceStorage.ID
	}

	newKey, err := b.store.CreateAccessKey(key)

	if err != nil {
		return err
	}
	b.keys = append(b.keys, newKey)
	return nil
}

func (e BackupInventory) Verify(backup *BackupFormat) error {
	if err := verifyDuplicate[BackupInventory](e.Name, backup.Inventories); err != nil {
		return err
	}
	if e.SSHKey != nil && getEntryByName[BackupAccessKey](e.SSHKey, backup.Keys) == nil {
		return fmt.Errorf("SSHKey does not exist in keys[].Name")
	}
	if e.BecomeKey != nil && getEntryByName[BackupAccessKey](e.BecomeKey, backup.Keys) == nil {
		return fmt.Errorf("BecomeKey does not exist in keys[].Name")
	}
	return nil
}

func (e BackupInventory) Restore(b *BackupDB) error {
	var SSHKeyID *int
	if e.SSHKey == nil {
		SSHKeyID = nil
	} else if k := findEntityByName[db.AccessKey](e.SSHKey, b.keys); k == nil {
		SSHKeyID = nil
	} else {
		SSHKeyID = &((*k).ID)
	}
	var BecomeKeyID *int
	if e.BecomeKey == nil {
		BecomeKeyID = nil
	} else if k := findEntityByName[db.AccessKey](e.BecomeKey, b.keys); k == nil {
		BecomeKeyID = nil
	} else {
		BecomeKeyID = &((*k).ID)
	}

	inv := e.Inventory
	inv.ProjectID = b.meta.ID
	inv.SSHKeyID = SSHKeyID
	inv.BecomeKeyID = BecomeKeyID

	newInventory, err := b.store.CreateInventory(inv)
	if err != nil {
		return err
	}
	b.inventories = append(b.inventories, newInventory)
	return nil
}

func (e BackupRepository) Verify(backup *BackupFormat) error {
	if err := verifyDuplicate[BackupRepository](e.Name, backup.Repositories); err != nil {
		return err
	}
	if e.SSHKey != nil && getEntryByName[BackupAccessKey](e.SSHKey, backup.Keys) == nil {
		return fmt.Errorf("SSHKey does not exist in keys[].Name")
	}
	return nil
}

func (e BackupRepository) Restore(b *BackupDB) error {
	var SSHKeyID int
	if k := findEntityByName[db.AccessKey](e.SSHKey, b.keys); k == nil {
		return fmt.Errorf("SSHKey does not exist in keys[].Name")
	} else {
		SSHKeyID = (*k).ID
	}

	repo := e.Repository
	repo.ProjectID = b.meta.ID
	repo.SSHKeyID = SSHKeyID

	newRepo, err := b.store.CreateRepository(repo)
	if err != nil {
		return err
	}
	b.repositories = append(b.repositories, newRepo)
	return nil
}

func (e BackupHostConfig) GetName() string {
	return e.Name
}

func (e BackupHostConfig) Verify(backup *BackupFormat) error {
	// The credential is remapped during the restore, so only what the mapping
	// points at can be checked here — but it must be, because Restore runs after
	// the project and its keys already exist. It also trims the name, which the
	// duplicate check below compares against.
	if err := e.ValidateMapping(); err != nil {
		return err
	}

	// Not verifyDuplicate: a mapping is unique on (type, name), and the table
	// enforces it, so a duplicate must fail here rather than half way through
	// the restore with the project already created. The stored name is trimmed,
	// so two entries differing only in surrounding whitespace are one mapping.
	duplicates := 0
	for _, other := range backup.HostConfigs {
		if other.Type == e.Type && strings.TrimSpace(other.Name) == e.Name {
			duplicates++
		}
	}
	if duplicates > 1 {
		return fmt.Errorf("%s is duplicate", e.Name)
	}

	if e.SSHKey == nil {
		return fmt.Errorf("SSHKey can not be empty")
	}
	key := getEntryByName[BackupAccessKey](e.SSHKey, backup.Keys)
	if key == nil {
		return fmt.Errorf("SSHKey does not exist in keys[].Name")
	}

	// The same rule ValidateHostConfig applies, checked before anything is
	// written.
	if err := e.ValidateCredential(key.Type); err != nil {
		return err
	}

	return nil
}

func (e BackupHostConfig) Restore(b *BackupDB) error {
	k := findEntityByName[db.AccessKey](e.SSHKey, b.keys)
	if k == nil {
		return fmt.Errorf("SSHKey does not exist in keys[].Name")
	}

	hostConfig := e.HostConfig
	hostConfig.ProjectID = b.meta.ID
	hostConfig.SSHKeyID = k.ID

	// A backup is a user supplied file, so a mapping restored from one goes
	// through the same checks as one created through the API: its host reaches a
	// generated ssh config, and its key must belong to the project.
	if err := hostConfig.Validate(); err != nil {
		return err
	}

	if err := db.ValidateHostConfig(b.store, &hostConfig); err != nil {
		return err
	}

	newHostConfig, err := b.store.CreateHostConfig(hostConfig)
	if err != nil {
		return err
	}

	b.hostConfigs = append(b.hostConfigs, newHostConfig)
	return nil
}

func (e BackupTemplate) Verify(backup *BackupFormat) error {
	if err := verifyDuplicate[BackupTemplate](e.Name, backup.Templates); err != nil {
		return err
	}
	if _, err := db.NormalizeTaskGroups(e.TaskGroups); err != nil {
		return err
	}

	if getEntryByName[BackupRepository](&e.Repository, backup.Repositories) == nil {
		return fmt.Errorf("repository does not exist in repositories[].name")
	}

	if e.Inventory != nil && getEntryByName[BackupInventory](e.Inventory, backup.Inventories) == nil {
		return fmt.Errorf("inventory does not exist in inventories[].name")
	}

	if e.VaultKey != nil && getEntryByName[BackupAccessKey](e.VaultKey, backup.Keys) == nil {
		return fmt.Errorf("vault_key does not exist in keys[].name")
	}

	for _, grant := range e.Roles {
		if grant.Role == "" && grant.RoleID == "" && (grant.Slug == nil || *grant.Slug == "") {
			return fmt.Errorf("role grant names no role")
		}
	}

	if e.Vaults != nil {
		for _, vault := range e.Vaults {
			if vault.VaultKey != nil {
				if getEntryByName[BackupAccessKey](vault.VaultKey, backup.Keys) == nil {
					return fmt.Errorf("vaults[].vaultKey does not exist in keys[].name")
				}
			}
		}
	}

	if e.View != nil && getEntryByName[BackupView](e.View, backup.Views) == nil {
		return fmt.Errorf("view does not exist in views[].name")
	}
	if err := verifyBackupSSHKeyBindings(e.SSHKeyBindings, backup.Keys); err != nil {
		return err
	}

	if buildTemplate := getEntryByName[BackupTemplate](e.BuildTemplate, backup.Templates); string(e.Type) == "deploy" && buildTemplate == nil {
		return fmt.Errorf("deploy is build but build_template does not exist in templates[].name")
	}

	return nil
}

func (e BackupTemplate) Restore(b *BackupDB) error {
	var InventoryID *int
	if e.Inventory != nil {
		if k := findEntityByName[db.Inventory](e.Inventory, b.inventories); k == nil {
			return fmt.Errorf("inventory does not exist in inventories[].name")
		} else {
			id := k.GetID()
			InventoryID = &id
		}
	}

	var EnvironmentIDs []int
	for i := range e.Environments {
		envName := &e.Environments[i]
		k := findEntityByName[db.Environment](envName, b.environments)
		if k == nil {
			return fmt.Errorf("environment does not exist in environments[].name")
		}
		EnvironmentIDs = append(EnvironmentIDs, k.GetID())
	}

	var RepositoryID int
	if k := findEntityByName[db.Repository](&e.Repository, b.repositories); k == nil {
		return fmt.Errorf("repository does not exist in repositories[].name")
	} else {
		RepositoryID = k.GetID()
	}

	var BuildTemplateID *int
	if string(e.Type) != "deploy" {
		BuildTemplateID = nil
	} else if k := findEntityByName[db.Template](e.BuildTemplate, b.templates); k == nil {
		BuildTemplateID = nil
	} else {
		BuildTemplateID = &(k.ID)
	}

	var ViewID *int
	if k := findEntityByName[db.View](e.View, b.views); k == nil {
		ViewID = nil
	} else {
		ViewID = &k.ID
	}

	template := e.Template
	template.ProjectID = b.meta.ID
	template.RepositoryID = RepositoryID
	template.EnvironmentIDs = EnvironmentIDs
	template.InventoryID = InventoryID
	template.ViewID = ViewID
	template.BuildTemplateID = BuildTemplateID
	sshKeys, err := restoreBackupSSHKeyBindings(e.SSHKeyBindings, b.keys)
	if err != nil {
		return err
	}
	template.SSHKeys = sshKeys

	newTemplate, err := b.store.CreateTemplate(template)
	if err != nil {
		return err
	}
	b.templates = append(b.templates, newTemplate)

	if e.Vaults != nil {
		for _, vault := range e.Vaults {
			var VaultKeyID *int

			if vault.VaultKey != nil {
				if k := findEntityByName[db.AccessKey](vault.VaultKey, b.keys); k == nil {
					return fmt.Errorf("vaults[].vaultKey does not exist in keys[].name")
				} else {
					VaultKeyID = &k.ID
				}
			}

			tplVault := vault.TemplateVault
			tplVault.ProjectID = b.meta.ID
			tplVault.TemplateID = newTemplate.ID
			tplVault.VaultKeyID = VaultKeyID

			_, err := b.store.CreateTemplateVault(tplVault)
			if err != nil {
				return err
			}
		}
	}

	if e.Roles != nil {
		for _, role := range e.Roles {
			if role.Slug != nil && role.Role == "" && role.RoleID == "" && !role.IsGlobal {
				if err := b.restoreUpstreamGrant(newTemplate, *role.Slug, role.Permissions); err != nil {
					return err
				}
				continue
			}
			if role.IsGlobal {
				var r db.Role
				var err error
				if strings.HasPrefix(string(role.RoleID), "builtin:") {
					r, err = b.store.GetGlobalRoleBySlug(strings.TrimPrefix(string(role.RoleID), "builtin:"))
				} else if role.RoleID != "" {
					r, err = b.store.GetGlobalRoleByID(role.RoleID)
					if errors.Is(err, db.ErrNotFound) {
						// Global role IDs are instance-local. A backup records the
						// stable slug as well, so use it only after the exact ID is
						// absent and retain the global-scope lookup.
						r, err = b.store.GetGlobalRoleBySlug(role.Role)
					}
				} else {
					r, err = b.store.GetGlobalRoleBySlug(role.Role)
				}
				if errors.Is(err, db.ErrNotFound) {
					return fmt.Errorf("global role does not exist: %s", role.RoleID)
				}
				if err != nil {
					return err
				}

				_, err = b.restoreEXTemplateGrant(newTemplate.ID, r.ID, role)

				if err != nil {
					return err
				}

				continue
			}
			var k *db.Role
			if role.RoleID != "" {
				role.RoleID = b.roleIDMap[role.RoleID]
				for i := range b.roles {
					if b.roles[i].ID == role.RoleID {
						k = &b.roles[i]
						break
					}
				}
			} else {
				k = findEntityByName[db.Role](&role.Role, b.roles)
			}
			if k == nil {
				return fmt.Errorf("roles[].role does not exist in roles[].name")
			} else {
				_, err = b.restoreEXTemplateGrant(newTemplate.ID, k.ID, role)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// restoreUpstreamGrant applies a Semaphore UI format 2 template grant. The
// slug resolves to a role from the same file or to a built-in role. Any other
// role of this instance is never matched by slug: that would hand the grant to
// an unrelated role, so the grant is reported for the user to assign instead.
func (b *BackupDB) restoreUpstreamGrant(template db.Template, slug string, permissions db.ProjectUserPermission) error {
	roleID, ok := b.roleSlugMap[slug]
	if !ok {
		role, err := b.store.GetGlobalRoleBySlug(slug)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return err
		}
		if err != nil || !role.Builtin {
			unassigned := b.unassignedRole(slug, ReasonNotInFile)
			unassigned.Templates = append(unassigned.Templates, UnassignedTemplate{ID: template.ID, Name: template.Name, Permissions: db.ProjectUserPermission(db.UpstreamTemplatePermissions(permissions, b.version))})
			return nil
		}
		roleID = role.ID
	}
	_, err := b.store.CreateTemplateRole(db.TemplateRolePerm{
		TemplateID:         template.ID,
		RoleID:             &roleID,
		ProjectID:          b.meta.ID,
		AllowedPermissions: db.UpstreamTemplatePermissions(permissions, b.version),
	})
	return err
}

func (e BackupIntegration) Restore(b *BackupDB) error {
	var authSecretID *int

	if e.AuthSecret == nil {
		authSecretID = nil
	} else if k := findEntityByName[db.AccessKey](e.AuthSecret, b.keys); k == nil {
		authSecretID = nil
	} else {
		authSecretID = &((*k).ID)
	}

	integration := e.Integration
	integration.ProjectID = b.meta.ID
	integration.AuthSecretID = authSecretID
	integration.TemplateID = nil
	integration.WorkflowID = nil
	integration.WorkflowUserID = nil
	if e.Template != nil {
		tpl := findEntityByName[db.Template](e.Template, b.templates)
		if tpl == nil {
			return fmt.Errorf("template does not exist in templates[].name")
		}
		integration.TemplateID = &tpl.ID
	} else if e.Workflow != nil {
		workflow := findEntityByName[db.WorkflowTemplate](e.Workflow, b.workflows)
		if workflow == nil {
			return fmt.Errorf("workflow does not exist in workflows[].name")
		}
		integration.WorkflowID = &workflow.ID
	} else {
		return fmt.Errorf("integration target is missing")
	}

	if integration.TaskParams != nil {
		inv := findEntityByName[db.Inventory](e.TaskParams.InventoryName, b.inventories)
		if inv != nil {
			integration.TaskParams.InventoryID = &inv.ID
		}
	}

	newIntegration, err := b.store.CreateIntegration(integration)
	if err != nil {
		return err
	}
	b.integrations = append(b.integrations, newIntegration)

	for i, m := range e.Matchers {
		m.IntegrationID = newIntegration.ID
		if _, err := b.store.CreateIntegrationMatcher(b.meta.ID, m); err != nil {
			return fmt.Errorf("matchers[%d]: %w", i, err)
		}
	}

	for i, v := range e.ExtractValues {
		v.IntegrationID = newIntegration.ID
		if _, err := b.store.CreateIntegrationExtractValue(b.meta.ID, v); err != nil {
			return fmt.Errorf("extract_values[%d]: %w", i, err)
		}
	}

	// Aliases are instance-wide bearer URLs. One already taken, for example by
	// the source project on the same instance, is skipped rather than moved.

	for _, a := range e.Aliases {
		alias := db.IntegrationAlias{
			Alias:         a,
			ProjectID:     b.meta.ID,
			IntegrationID: &newIntegration.ID,
		}
		_, _ = b.store.CreateIntegrationAlias(alias)
	}

	return nil
}

func (e BackupIntegration) Verify(backup *BackupFormat) error {
	if (e.Template == nil && e.Workflow == nil) || (e.Template != nil && e.Workflow != nil) {
		return errors.New("integration must reference either a template or a workflow")
	}
	if e.Template != nil && findEntityByName[BackupTemplate](e.Template, backup.Templates) == nil {
		return errors.New("integration template does not exist")
	}
	if e.Workflow != nil && findEntityByName[BackupWorkflow](e.Workflow, backup.Workflows) == nil {
		return errors.New("integration workflow does not exist")
	}
	return nil
}

func (e BackupRunner) Verify(backup *BackupFormat) error {
	return verifyDuplicate[BackupRunner](e.Name, backup.Runners)
}

func (e BackupRunner) Restore(b *BackupDB) error {
	runner := e.Runner
	runner.ProjectID = &b.meta.ID
	runner.Token = "" // Unregistered runner
	newRunner, err := b.store.CreateRunner(runner)
	if err != nil {
		return err
	}
	b.runners = append(b.runners, newRunner)
	return nil
}

func (e BackupWorkflow) Verify(backup *BackupFormat) error {
	for _, grant := range e.Roles {
		if grant.Slug == "" && grant.RoleID == "" {
			return fmt.Errorf("role grant has no slug")
		}
	}
	if err := verifyDuplicate[BackupWorkflow](e.Name, backup.Workflows); err != nil {
		return err
	}

	for _, n := range e.Nodes {
		if n.Template != nil && getEntryByName[BackupTemplate](n.Template, backup.Templates) == nil {
			return fmt.Errorf("template does not exist in templates[].name")
		}
	}

	return nil
}

func (e BackupWorkflow) Restore(b *BackupDB) error {
	workflow := e.WorkflowTemplate
	workflow.ID = 0
	workflow.ProjectID = b.meta.ID

	nodes := make([]db.WorkflowNode, len(e.Nodes))
	for i, bn := range e.Nodes {
		// Keep the node ID (used by edges) but clear all project-scoped
		// references; they are re-resolved from names below.
		node := bn.WorkflowNode
		node.WorkflowTemplateID = 0
		node.TemplateID = 0
		node.TaskParamsID = nil

		if bn.Template != nil {
			tpl := findEntityByName[db.Template](bn.Template, b.templates)
			if tpl == nil {
				return fmt.Errorf("template does not exist in templates[].name")
			}
			node.TemplateID = tpl.ID
		}

		if node.TaskParams != nil {
			node.TaskParams.InventoryID = nil
			inv := findEntityByName[db.Inventory](node.TaskParams.InventoryName, b.inventories)
			if inv != nil {
				node.TaskParams.InventoryID = &inv.ID
			}
		}

		nodes[i] = node
	}

	workflow.Nodes = nodes
	// workflow.Edges is carried by the embedded WorkflowTemplate and references
	// nodes by the IDs preserved above; writeWorkflowGraph remaps them.

	newWorkflow, err := b.workflowStore.CreateWorkflowTemplate(workflow)
	if err != nil {
		return err
	}

	for _, grant := range e.Roles {
		permissions := grant.Permissions & db.WorkflowGrantPermissions
		if b.upstreamPermissions {
			permissions = db.UpstreamWorkflowPermissions(grant.Permissions, b.version)
		}
		role, err := b.workflowGrantRole(grant)
		if errors.Is(err, db.ErrNotFound) {
			unassigned := b.unassignedRole(grant.Slug, ReasonNotInFile)
			unassigned.Workflows = append(unassigned.Workflows, UnassignedWorkflow{ID: newWorkflow.ID, Name: newWorkflow.Name, Permissions: permissions})
			continue
		}
		if err != nil {
			return err
		}
		_, err = b.store.CreateWorkflowRole(db.WorkflowRolePerm{
			WorkflowID:  newWorkflow.ID,
			RoleID:      role.ID,
			ProjectID:   b.meta.ID,
			Permissions: permissions,
		})
		if err != nil {
			return err
		}
	}

	b.workflows = append(b.workflows, newWorkflow)
	return nil
}

func (backup *BackupFormat) Verify() error {
	if backup.PermissionSchema != "" && backup.PermissionSchema != "semaphore-ex" {
		return fmt.Errorf("unsupported backup permission schema")
	}
	if backup.PermissionSchema == "semaphore-ex" && (backup.Version == nil || *backup.Version != BackupFormatVersion) {
		return fmt.Errorf("semaphore-ex permission schema requires backup version %d", BackupFormatVersion)
	}
	if backup.Version != nil && *backup.Version < 2 {
		return fmt.Errorf("unsupported backup version")
	}
	if backup.Version != nil && *backup.Version > BackupFormatVersion {
		return fmt.Errorf("backup was written by a newer version of Semaphore")
	}
	for i, o := range backup.Environments {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at environments[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.Views {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at views[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.Schedules {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at schedules[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.Keys {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at keys[%d]: %s", i, err.Error())
		}
	}
	if err := verifyBackupSSHKeyBindings(backup.Meta.DefaultSSHKeyBindings, backup.Keys); err != nil {
		return err
	}
	if err := verifyBackupSSHKeyBindings(backup.Meta.AlwaysSSHKeyBindings, backup.Keys); err != nil {
		return err
	}
	for i, o := range backup.Repositories {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at repositories[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.HostConfigs {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at host_configs[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.Inventories {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at inventories[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.SecretStorages {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at secret storage[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.Templates {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at templates[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.Roles {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at roles[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.Runners {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at runners[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.Workflows {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at workflows[%d]: %s", i, err.Error())
		}
	}
	for i, o := range backup.Integration {
		if err := o.Verify(backup); err != nil {
			return fmt.Errorf("error at integrations[%d]: %s", i, err.Error())
		}
	}

	return nil
}

// Restore creates the project whole or not at all: a failed entry removes it again.
func (backup *BackupFormat) Restore(user db.User, store db.Store, workflowStore db.WorkflowManager) (restored *RestoredProject, err error) {
	if err := backup.rejectTaskGroupsForRestore(); err != nil {
		return nil, err
	}
	if err := backup.Verify(); err != nil {
		return nil, err
	}
	version := 0
	if backup.Version != nil {
		version = *backup.Version
	}
	var b = BackupDB{store: store, workflowStore: workflowStore, unassigned: []UnassignedRole{}, version: version, upstreamPermissions: backup.Version != nil && backup.PermissionSchema == ""}
	project := backup.Meta.Project

	// Prevent importing a project with a name that already exists
	existingProjects, err := b.store.GetAllProjects()
	if err == nil {
		for _, p := range existingProjects {
			if p.Name == project.Name { // exact name match
				return nil, common_errors.NewValidationError(fmt.Sprintf("project with name '%s' already exists", project.Name))
			}
		}
	}

	newProject, err := b.store.CreateProject(project)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err == nil {
			return
		}
		if deleteErr := b.store.DeleteProject(newProject.ID); deleteErr != nil {
			err = &CleanupError{ProjectID: newProject.ID, Err: err, DeleteErr: deleteErr}
		}
	}()

	ownerRole, err := b.store.GetGlobalRoleBySlug(string(db.ProjectOwner))
	if err != nil {
		return nil, err
	}

	_, err = b.store.CreateProjectUserWithRoleRefs(db.ProjectUser{
		ProjectID: newProject.ID,
		UserID:    user.ID,
		Role:      db.ProjectOwner,
		Revision:  1,
	}, []db.ProjectRoleID{ownerRole.ID})
	if err != nil {
		return nil, err
	}

	b.meta = newProject
	for i, o := range backup.SecretStorages {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at secret storage[%d]: %w", i, err)
		}
	}

	for i, o := range backup.Roles {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at roles[%d]: %w", i, err)
		}
	}

	for i, o := range backup.Environments {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at environments[%d]: %w", i, err)
		}
	}

	for i, o := range backup.Views {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at views[%d]: %w", i, err)
		}
	}

	for i, o := range backup.Keys {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at keys[%d]: %w", i, err)
		}
	}
	defaultSSHKeys, err := restoreBackupSSHKeyBindings(backup.Meta.DefaultSSHKeyBindings, b.keys)
	if err != nil {
		return nil, err
	}
	alwaysSSHKeys, err := restoreBackupSSHKeyBindings(backup.Meta.AlwaysSSHKeyBindings, b.keys)
	if err != nil {
		return nil, err
	}
	newProject.DefaultSSHKeys = defaultSSHKeys
	newProject.AlwaysSSHKeys = alwaysSSHKeys
	if err = b.store.UpdateProject(newProject); err != nil {
		return nil, err
	}

	for i, o := range backup.Repositories {
		// A repository can become shared after templates with branch settings
		// already exist. Restore that same order without dropping saved settings.
		o.Monorepo = false
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at repositories[%d]: %w", i, err)
		}
	}

	for i, o := range backup.HostConfigs {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at host_configs[%d]: %w", i, err)
		}
	}

	for i, o := range backup.Inventories {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at inventories[%d]: %w", i, err)
		}
	}

	deployTemplates := make([]int, 0)
	for i, o := range backup.Templates {
		if string(o.Type) == "deploy" {
			deployTemplates = append(deployTemplates, i)
			continue
		}
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at templates[%d]: %w", i, err)
		}
	}

	for _, i := range deployTemplates {
		o := backup.Templates[i]
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at templates[%d]: %w", i, err)
		}
	}
	if err := backup.restoreMonorepoPolicies(&b); err != nil {
		return nil, err
	}

	for i, o := range backup.Runners {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at runners[%d]: %w", i, err)
		}
	}

	// Workflows depend on templates, inventories and environments, while
	// workflow integrations depend on the restored workflow IDs. Restore the
	// workflow graph between those two dependency layers.
	for i, o := range backup.Workflows {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at workflows[%d]: %w", i, err)
		}
	}

	for i, o := range backup.Integration {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at integrations[%d]: %w", i, err)
		}
	}

	for _, o := range backup.IntegrationAliases {
		alias := db.IntegrationAlias{
			Alias:     o,
			ProjectID: b.meta.ID,
		}
		_, _ = b.store.CreateIntegrationAlias(alias)
	}

	// An active schedule can start a task on any HA node as soon as it exists.
	// Schedules are therefore created inactive after every other entry and
	// activated only once the whole restore succeeded.
	for i, o := range backup.Schedules {
		if err := o.Restore(&b); err != nil {
			return nil, fmt.Errorf("error at schedules[%d]: %w", i, err)
		}
	}
	for i, o := range backup.Schedules {
		if !o.Active {
			continue
		}
		if err := b.store.SetScheduleActive(b.meta.ID, b.schedules[i].ID, true); err != nil {
			return nil, fmt.Errorf("error at schedules[%d]: %w", i, err)
		}
	}

	return &RestoredProject{Project: newProject, UnassignedRoles: b.unassigned}, nil
}

// rejectTaskGroupsForRestore keeps a backup from silently weakening a
// template's execution policy. Group definitions and grants are not included
// in this backup format, so IDs cannot be safely remapped at the destination.
func (backup *BackupFormat) rejectTaskGroupsForRestore() error {
	for index := range backup.Templates {
		groups, err := db.NormalizeTaskGroups(backup.Templates[index].TaskGroups)
		if err != nil {
			return err
		}
		if len(groups) > 0 {
			return fmt.Errorf("template %q has task group memberships; this backup format cannot restore managed groups, so recreate and explicitly rebind them before restoring", backup.Templates[index].Name)
		}
	}
	return nil
}
