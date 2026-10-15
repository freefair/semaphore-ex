package runners

import (
	"encoding/json"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	serviceRunners "github.com/semaphoreui/semaphore/services/runners"
	"github.com/stretchr/testify/assert"
)

func TestSanitizeMonorepoJobDataRemovesLegacyExecutionOverrides(t *testing.T) {
	branch := "release"
	commit := "deadbeef"
	job := serviceRunners.JobData{
		Task:       db.Task{GitBranch: &branch, CommitHash: &commit},
		Template:   db.Template{GitBranch: &branch, AllowOverrideBranchInTask: true},
		Repository: db.Repository{GitBranch: "main", Monorepo: true},
	}

	sanitizeMonorepoJobData(&job)

	assert.Nil(t, job.Task.GitBranch)
	assert.Nil(t, job.Task.CommitHash)
	assert.Nil(t, job.Template.GitBranch)
	assert.False(t, job.Template.AllowOverrideBranchInTask)
	assert.Equal(t, "main", job.Repository.GitBranch)
	assert.True(t, job.Repository.Monorepo)
}

func TestSanitizeMonorepoJobDataKeepsLegacyRunnerOnRepositoryRevision(t *testing.T) {
	branch := "release"
	commit := "deadbeef"
	job := serviceRunners.JobData{
		Task:       db.Task{GitBranch: &branch, CommitHash: &commit},
		Template:   db.Template{GitBranch: &branch, AllowOverrideBranchInTask: true},
		Repository: db.Repository{GitBranch: "main", Monorepo: true},
	}
	sanitizeMonorepoJobData(&job)

	type legacyRepository struct {
		GitBranch string `json:"git_branch"`
	}
	type legacyJob struct {
		Task       db.Task          `json:"task"`
		Template   db.Template      `json:"template"`
		Repository legacyRepository `json:"repository"`
	}
	encoded, err := json.Marshal(job)
	assert.NoError(t, err)
	var legacy legacyJob
	assert.NoError(t, json.Unmarshal(encoded, &legacy))
	assert.Equal(t, "main", legacy.Repository.GitBranch)
	assert.Nil(t, legacy.Task.GitBranch)
	assert.Nil(t, legacy.Task.CommitHash)
	assert.Nil(t, legacy.Template.GitBranch)
	assert.False(t, legacy.Template.AllowOverrideBranchInTask)
}

func TestSanitizeMonorepoJobDataKeepsOrdinaryRepositoryOverrides(t *testing.T) {
	branch := "release"
	commit := "deadbeef"
	job := serviceRunners.JobData{
		Task:       db.Task{GitBranch: &branch, CommitHash: &commit},
		Template:   db.Template{GitBranch: &branch, AllowOverrideBranchInTask: true},
		Repository: db.Repository{GitBranch: "main"},
	}

	sanitizeMonorepoJobData(&job)

	assert.Same(t, &branch, job.Task.GitBranch)
	assert.Same(t, &commit, job.Task.CommitHash)
	assert.Same(t, &branch, job.Template.GitBranch)
	assert.True(t, job.Template.AllowOverrideBranchInTask)
}

func TestSanitizeInventoryJobDataDropsStaleStaticRepository(t *testing.T) {
	repositoryID := 8
	job := serviceRunners.JobData{
		Inventory: db.Inventory{
			Type: db.InventoryStatic, RepositoryID: &repositoryID,
			Repository: &db.Repository{ID: repositoryID, SSHKeyID: 7},
		},
		InventoryRepository: &db.Repository{ID: repositoryID, SSHKeyID: 7},
	}

	sanitizeInventoryJobData(&job)

	assert.Nil(t, job.Inventory.RepositoryID)
	assert.Nil(t, job.Inventory.Repository)
	assert.Nil(t, job.InventoryRepository)
}
