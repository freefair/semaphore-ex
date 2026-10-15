package runners

import serviceRunners "github.com/semaphoreui/semaphore/services/runners"

// sanitizeMonorepoJobData retains the repository branch as the only revision
// choice sent to a runner. Older runner binaries ignore Repository.Monorepo,
// so clearing legacy overrides here keeps them on the repository branch too.
func sanitizeMonorepoJobData(job *serviceRunners.JobData) {
	if job == nil || !job.Repository.Monorepo {
		return
	}

	job.Template.GitBranch = nil
	job.Template.AllowOverrideBranchInTask = false
	job.Task.GitBranch = nil
	job.Task.CommitHash = nil
}
