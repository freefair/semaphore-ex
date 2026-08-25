// Package api adapts the public Community surface for the workspace fixture.
package api

import community "github.com/semaphoreui/semaphore/community-pro/api"

type RolesController = community.RolesController
type TerraformController = community.TerraformController

var (
	NewRolesController        = community.NewRolesController
	NewSubscriptionController = community.NewSubscriptionController
	NewTerraformController    = community.NewTerraformController
	VerifySessionByEmail      = community.VerifySessionByEmail
)
