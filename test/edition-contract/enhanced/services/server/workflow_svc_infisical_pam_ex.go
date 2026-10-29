package server

import "github.com/semaphoreui/semaphore/pro_interfaces"

// redactCrossProjectInfisicalPAM hides the source project's PAM routing from a
// workflow node that runs a template shared from another project: host names,
// account paths and storage names belong to the source project. The private
// component digests keep the full routes, so a change still makes a review
// stale.
func redactCrossProjectInfisicalPAM(plan *pro_interfaces.ExecutionPreflightPlan) {
	for index := range plan.References {
		if plan.References[index].Kind == pro_interfaces.ExecutionReferenceSecretStorage {
			plan.References[index].BindingTarget = ""
		}
	}
	for index := range plan.Findings {
		if plan.Findings[index].Code == pro_interfaces.ExecutionReasonInfisicalPAMInvalid {
			plan.Findings[index].Message = "Infisical PAM routes of the shared template's project cannot be applied."
		}
	}
}
