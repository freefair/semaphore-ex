package runners

import serviceRunners "github.com/semaphoreui/semaphore/services/runners"

// sanitizeInventoryJobData keeps a stale repository reference on a non-file
// inventory out of the runner transport and its access-key collection.
func sanitizeInventoryJobData(job *serviceRunners.JobData) {
	if job == nil {
		return
	}
	job.Inventory.NormalizeRepositoryReference()
	job.InventoryRepository = job.Inventory.Repository
}
