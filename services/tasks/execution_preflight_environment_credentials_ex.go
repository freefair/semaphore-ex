package tasks

import (
	"strconv"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// appendEnvironmentCredentialReferences lists a Variable Group's global
// credential references in the execution review and binds them into the
// reviewed state, so a changed reference makes a reviewed start stale. Only
// value-free metadata is used: display name, target and credential ID.
func (p *TaskPool) appendEnvironmentCredentialReferences(plan *pro_interfaces.ExecutionPreflightPlan, state *[]any, environment db.Environment) {
	for _, reference := range environment.Credentials {
		name := "Global credential #" + strconv.Itoa(reference.CredentialID)
		if credential, err := p.store.GetGlobalCredential(reference.CredentialID); err == nil {
			name = "Global credential: " + credential.DisplayName
		}
		plan.References = append(plan.References, pro_interfaces.ExecutionPreflightReference{
			Kind: pro_interfaces.ExecutionReferenceCredential, ID: reference.CredentialID,
			Name: name, Revision: "global", BindingTarget: string(reference.Target) + ":" + reference.Name, Visible: true,
		})
		*state = append(*state, "environment_credential", environment.ID, reference.Name, string(reference.Target), reference.CredentialID)
	}
}
