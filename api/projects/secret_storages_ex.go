package projects

import (
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"net/http"
	"strconv"
)

func (c *SecretStorageController) TestConnection(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessExecute) {
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	storage := helpers.GetFromContext(r, "secretStorage").(db.SecretStorage)
	health, err := c.secretStorageService.TestConnection(r.Context(), project.ID, storage.ID)
	if err != nil {
		helpers.WriteJSON(w, http.StatusServiceUnavailable, health)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, health)
}

func (c *SecretStorageController) requireCapability(
	w http.ResponseWriter,
	r *http.Request,
	access pro_interfaces.CapabilityAccess,
) bool {
	if c.capabilityProvider == nil {
		writeRuntimeSecretCapabilityError(w, pro_interfaces.CapabilityDeniedError{
			Decision: pro_interfaces.NewCapabilityDecision(
				pro_interfaces.CapabilityRuntimeSecrets,
				pro_interfaces.CapabilityStateUnavailable,
				pro_interfaces.CapabilityReasonProviderUnavailable,
				nil,
				nil,
			),
			Required: access,
		})
		return false
	}
	user := helpers.UserFromContext(r)
	snapshot, err := c.capabilityProvider.Resolve(r.Context(), pro_interfaces.CapabilityRequest{
		UserID: user.ID, IsAdmin: user.Admin, At: tz.Now(),
	})
	if err == nil {
		err = snapshot.Require(pro_interfaces.CapabilityRuntimeSecrets, access)
	}
	if err != nil {
		writeRuntimeSecretCapabilityError(w, err)
		return false
	}
	return true
}

func writeRuntimeSecretCapabilityError(w http.ResponseWriter, err error) {
	var denied pro_interfaces.CapabilityDeniedError
	if errors.As(err, &denied) {
		status := http.StatusForbidden
		if denied.Decision.State() == pro_interfaces.CapabilityStateUnavailable {
			status = http.StatusNotFound
		}
		helpers.WriteJSON(w, status, map[string]any{
			"error": "CAPABILITY_DENIED", "capability": denied.Decision.ID(),
			"state": denied.Decision.State(), "reason": denied.Decision.Reason(),
			"required_access": denied.Required,
		})
		return
	}
	helpers.WriteErrorStatus(w, "CAPABILITY_PROVIDER_ERROR", http.StatusServiceUnavailable)
}

func (c *SecretStorageController) GetSyncHistory(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	storage := helpers.GetFromContext(r, "secretStorage").(db.SecretStorage)
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			helpers.WriteErrorStatus(w, "history limit must be between 1 and 100", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	operations, err := c.secretStorageService.GetSecretSyncHistory(
		storage.ProjectID, storage.ID, limit,
	)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, operations)
}
