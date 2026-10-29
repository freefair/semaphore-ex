package tasks

import (
	"errors"
	"strconv"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/server"
)

// appendInfisicalPAMPreflight lists the SSH hosts the task will route through
// each Infisical storage, so the reviewer sees the PAM accounts before start.
// The routes and the storage credential state join the reference digest, so a
// route or secret change after review makes the review stale. Configuration
// the task runner would reject becomes a denial with its reason.
func (p *TaskPool) appendInfisicalPAMPreflight(
	plan *pro_interfaces.ExecutionPreflightPlan,
	state *[]any,
	projectID int,
) ([]db.TaskInfisicalPAM, error) {
	configs, err := server.LoadProjectInfisicalPAM(p.store, p.store, projectID)
	if err != nil {
		var validation *common_errors.ValidationError
		if !errors.As(err, &validation) {
			return nil, err
		}
		plan.Findings = append(plan.Findings, pro_interfaces.ExecutionPreflightFinding{
			Severity: pro_interfaces.ExecutionFindingDenial,
			Code:     pro_interfaces.ExecutionReasonInfisicalPAMInvalid,
			Message:  "Infisical PAM routes cannot be applied: " + validation.Error(),
		})
		*state = append(*state, "infisical_pam_invalid", validation.Error())
		return nil, nil
	}
	if len(configs) == 0 {
		return nil, nil
	}
	names, err := p.infisicalPAMStorageNames(projectID)
	if err != nil {
		return nil, err
	}
	for _, config := range configs {
		name := names[config.StorageID]
		if name == "" {
			name = "Infisical storage #" + strconv.Itoa(config.StorageID)
		}
		plan.References = append(plan.References, pro_interfaces.ExecutionPreflightReference{
			Kind: pro_interfaces.ExecutionReferenceSecretStorage, ID: config.StorageID,
			Name: name, BindingTarget: infisicalPAMRoutesTarget(config.Routes), Visible: true,
		})
		*state = append(*state, config.StorageID, config.SiteURL, config.ClientID, config.OrganizationSlug,
			config.CACertificate, config.SessionDuration, config.Routes)
		credentialID := config.CredentialKeyID
		if err = p.appendExecutionPreflightCredentialState(state, projectID, &credentialID); err != nil {
			return nil, err
		}
	}
	return configs, nil
}

// infisicalPAMRoutesTarget lists a storage's routes in one reference, so a
// storage costs one reference however many hosts it routes. A list beyond
// the display string bound is replaced by the host count; the digest still
// covers every route.
func infisicalPAMRoutesTarget(routes []db.InfisicalPAMRoute) string {
	entries := make([]string, 0, len(routes))
	for _, route := range routes {
		entries = append(entries, route.Host+" → "+route.Account)
	}
	target := "ssh: " + strings.Join(entries, ", ")
	if len(target) > pro_interfaces.MaxExecutionPreflightStringBytes {
		return "ssh: " + strconv.Itoa(len(routes)) + " hosts"
	}
	return target
}

func (p *TaskPool) infisicalPAMStorageNames(projectID int) (map[int]string, error) {
	storages, err := p.store.GetSecretStorages(projectID)
	if err != nil {
		return nil, err
	}
	names := make(map[int]string, len(storages))
	for _, storage := range storages {
		if storage.Type == db.SecretStorageTypeInfisical {
			names[storage.ID] = storage.Name
		}
	}
	return names, nil
}

// appendInfisicalPAMContainerFinding warns that a container executor skips
// the routes; the task log says the same once the task runs.
func appendInfisicalPAMContainerFinding(plan *pro_interfaces.ExecutionPreflightPlan, configs []db.TaskInfisicalPAM, container bool) {
	if !container || len(configs) == 0 {
		return
	}
	plan.Findings = append(plan.Findings, pro_interfaces.ExecutionPreflightFinding{
		Severity: pro_interfaces.ExecutionFindingWarning,
		Code:     pro_interfaces.ExecutionReasonInfisicalPAMSkipped,
		Message:  "Infisical PAM routes are not applied: container tasks cannot reach the runner's Infisical PAM broker.",
	})
}
