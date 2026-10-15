package tasks

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
)

func TestRewriteContainerPath_StaticInventoryWithStaleRepositoryUsesHostsFile(t *testing.T) {
	previous := util.Config
	util.Config = &util.ConfigType{TmpPath: t.TempDir()}
	t.Cleanup(func() { util.Config = previous })
	repositoryID := 9
	executor := LocalExecutor{
		Task:      db.Task{ID: 7},
		Template:  db.Template{ID: 3, ProjectID: 2},
		Inventory: db.Inventory{ID: 4, Type: db.InventoryStatic, RepositoryID: &repositoryID, Repository: &db.Repository{ID: repositoryID}},
	}

	assert.Equal(t, "/semaphore/bundle/inventory/hosts", executor.rewriteContainerPath(executor.tmpInventoryFullPath()))
}
