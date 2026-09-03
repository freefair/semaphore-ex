// Package api adapts the public Community surface for the workspace fixture.
package api

import community "github.com/semaphoreui/semaphore/community-pro/api"

type TerraformController = community.TerraformController

var (
	NewTerraformController = community.NewTerraformController
	VerifySessionByEmail   = community.VerifySessionByEmail
)
