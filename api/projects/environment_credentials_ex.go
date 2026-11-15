package projects

import (
	"errors"
	"net/http"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

// validateEnvironmentCredentialChanges checks the references a request adds
// or changes. Unchanged references are not re-validated, so a Variable Group
// stays editable after a grant expires; the task is still blocked at dispatch
// because the resolver authorizes every use again.
func validateEnvironmentCredentialChanges(r *http.Request, env db.Environment, previous []db.EnvironmentCredential) (int, error) {
	if err := db.ValidateEnvironmentCredentials(env.Credentials); err != nil {
		return http.StatusBadRequest, err
	}
	unchanged := make(map[db.EnvironmentCredential]struct{}, len(previous))
	for _, reference := range previous {
		unchanged[reference] = struct{}{}
	}
	var changed []db.EnvironmentCredential
	for _, reference := range env.Credentials {
		if _, ok := unchanged[reference]; !ok {
			changed = append(changed, reference)
		}
	}
	if len(changed) == 0 {
		return http.StatusOK, nil
	}

	user := helpers.UserFromContext(r)
	permissions, _ := helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)
	if user == nil || (!user.Admin && !permissions.Can(db.CanListGrantedCredentials)) {
		return http.StatusForbidden, errors.New("referencing global credentials requires the permission to list granted credentials")
	}

	store := helpers.Store(r)
	now := time.Now().UTC()
	for _, reference := range changed {
		credential, err := store.GetGlobalCredential(reference.CredentialID)
		if err != nil {
			return http.StatusBadRequest, common_errors.NewValidationError("global credential for " + reference.Name + " is not granted to this project")
		}
		grant, err := store.GetGlobalCredentialGrantForProject(credential.ID, env.ProjectID)
		if err != nil || !grant.IsEffectiveAt(env.ProjectID, db.GlobalCredentialGrantOperationReference, credential.Enabled, now) {
			return http.StatusBadRequest, common_errors.NewValidationError("global credential for " + reference.Name + " is not granted to this project")
		}
	}
	return http.StatusOK, nil
}

// writeEnvironmentCredentialError answers a rejected reference change. The
// message names the reference, never the credential's value or provider path.
func writeEnvironmentCredentialError(w http.ResponseWriter, status int, err error) {
	helpers.WriteJSON(w, status, map[string]string{"error": err.Error()})
}
