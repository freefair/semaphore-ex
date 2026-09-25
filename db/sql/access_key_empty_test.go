package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAccessKeyMatchesListEmptyState(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "access key empty state"})
	require.NoError(t, err)

	for _, test := range []struct {
		name     string
		key      db.AccessKey
		expected bool
	}{
		{"empty", db.AccessKey{Name: "empty", Type: db.AccessKeyString, ProjectID: &project.ID}, true},
		{"populated", db.AccessKey{Name: "populated", Type: db.AccessKeyString, ProjectID: &project.ID, Secret: new("stored value")}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			created, createErr := store.CreateAccessKey(test.key)
			require.NoError(t, createErr)

			detail, detailErr := store.GetAccessKey(project.ID, created.ID)
			require.NoError(t, detailErr)
			assert.Equal(t, test.expected, detail.Empty)

			keys, listErr := store.GetAccessKeys(project.ID, db.GetAccessKeyOptions{}, db.RetrieveQueryParams{})
			require.NoError(t, listErr)
			for _, key := range keys {
				if key.ID == created.ID {
					assert.Equal(t, detail.Empty, key.Empty)
					return
				}
			}
			assert.Fail(t, "created key missing from list")
		})
	}
}
