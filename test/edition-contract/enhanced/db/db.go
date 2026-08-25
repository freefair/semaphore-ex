// Package db adapts the public Community surface for the workspace fixture.
package db

import community "github.com/semaphoreui/semaphore/community-pro/db"

var (
	ValidateWorkflowTemplate = community.ValidateWorkflowTemplate
	WorkflowConditionMatches = community.WorkflowConditionMatches
	WorkflowRootNode         = community.WorkflowRootNode
)
