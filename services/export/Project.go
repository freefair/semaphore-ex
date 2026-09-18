package export

import (
	"strconv"

	"github.com/semaphoreui/semaphore/db"
)

type ProjectExporter struct {
	ValueMap[db.Project]
	pending []projectSSHBindingRestore
}

type projectSSHBindingRestore struct {
	sourceProjectID int
	projectID       int
	defaultKeys     db.SSHKeyBindings
	alwaysKeys      db.SSHKeyBindings
}

func (e *ProjectExporter) load(store db.Store, exporter DataExporter, progress Progress) error {

	projects, err := store.GetAllProjects()
	if err != nil {
		return err
	}

	return e.appendValues(projects, GlobalScope)
}

func (e *ProjectExporter) restore(store db.Store, exporter DataExporter, progress Progress) (err error) {
	return e.restoreValues(store, exporter, progress, e)
}

func (e *ProjectExporter) restoreValue(val EntityObject[db.Project], store db.Store, exporter DataExporter) (err error) {

	old := val.value
	bindings := projectSSHBindingRestore{
		sourceProjectID: old.ID,
		defaultKeys:     old.DefaultSSHKeys,
		alwaysKeys:      old.AlwaysSSHKeys,
	}
	// AccessKey depends on Project, so source IDs cannot be remapped yet.
	old.DefaultSSHKeys = nil
	old.AlwaysSSHKeys = nil

	newObj, err := store.CreateProject(old)
	if err != nil {
		return err
	}

	bindings.projectID = newObj.ID
	e.pending = append(e.pending, bindings)
	return exporter.mapKeys(e.getName(), val.scope, old.GetDbKey(), newObj.GetDbKey())
}

func (e *ProjectExporter) postRestore(store db.Store, exporter DataExporter) error {
	for _, pending := range e.pending {
		project, err := store.GetProject(pending.projectID)
		if err != nil {
			return err
		}
		project.DefaultSSHKeys, err = remapSSHKeyBindings(exporter, strconv.Itoa(pending.sourceProjectID), pending.defaultKeys)
		if err != nil {
			return err
		}
		project.AlwaysSSHKeys, err = remapSSHKeyBindings(exporter, strconv.Itoa(pending.sourceProjectID), pending.alwaysKeys)
		if err != nil {
			return err
		}
		if err = store.UpdateProject(project); err != nil {
			return err
		}
	}
	e.pending = nil
	return nil
}

func (e *ProjectExporter) exportDependsOn() []string {
	return []string{}
}

func remapSSHKeyBindings(exporter DataExporter, scope string, bindings db.SSHKeyBindings) (db.SSHKeyBindings, error) {
	if bindings == nil {
		return nil, nil
	}
	mapped := make(db.SSHKeyBindings, len(bindings))
	for index, binding := range bindings {
		keyID, err := exporter.getNewKeyInt(AccessKey, scope, binding.AccessKeyID)
		if err != nil {
			return nil, err
		}
		mapped[index] = db.SSHKeyBinding{AccessKeyID: keyID, Hosts: append([]string(nil), binding.Hosts...)}
	}
	return mapped, nil
}

func (e *ProjectExporter) getName() string {
	return Project
}
