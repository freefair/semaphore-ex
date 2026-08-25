// Package projects adapts the public Community surface for the workspace fixture.
package projects

import community "github.com/semaphoreui/semaphore/community-pro/api/projects"

type ProjectRunnerControllerImpl = community.ProjectRunnerControllerImpl

var (
	NewProjectRunnerController      = community.NewProjectRunnerController
	NewTerraformInventoryController = community.NewTerraformInventoryController
	NewWorkflowController           = community.NewWorkflowController
)
