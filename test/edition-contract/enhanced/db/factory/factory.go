// Package factory adapts the public Community surface for the workspace fixture.
package factory

import community "github.com/semaphoreui/semaphore/community-pro/db/factory"

var (
	NewAnsibleTaskRepository = community.NewAnsibleTaskRepository
	NewTerraformStore        = community.NewTerraformStore
	NewWorkflowStore         = community.NewWorkflowStore
)
