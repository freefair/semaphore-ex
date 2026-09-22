package export

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
)

func TestTemplateImportRejectsManagedTaskGroupMemberships(t *testing.T) {
	template := db.Template{TaskGroups: db.TaskGroupBindings{42}}
	_, err := db.NormalizeTaskGroups(template.TaskGroups)
	assert.NoError(t, err)

	err = rejectTemplateTaskGroupsForImport(template.TaskGroups)
	assert.ErrorContains(t, err, "cannot restore templates with task group memberships")
	assert.Equal(t, db.TaskGroupBindings{42}, template.TaskGroups)
}
