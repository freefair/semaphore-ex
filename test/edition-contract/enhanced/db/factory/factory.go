// Package factory adapts the public Community surface for the workspace fixture.
package factory

import community "github.com/semaphoreui/semaphore/community-pro/db/factory"

var (
	NewTerraformStore = community.NewTerraformStore
	NewWorkflowStore  = community.NewWorkflowStore
)
