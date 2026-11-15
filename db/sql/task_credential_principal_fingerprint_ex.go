package sql

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// A credential principal answers for one configuration of its trigger: the
// one its user saved. The principal row keeps a fingerprint of that
// configuration, and the principal only counts while the trigger still has
// it. Any later write, from any node and at any time, ends the principal's
// responsibility without depending on claim timing.
//
// The fingerprint covers what decides which task runs and what it receives.
// Fields the system changes on its own (the last seen commit, activation by
// the scheduler) and purely descriptive ones (names) are left out, so normal
// operation does not invalidate a principal.

type scheduleCredentialFingerprint struct {
	TemplateID     int            `json:"template_id"`
	CronFormat     string         `json:"cron_format"`
	Timezone       *string        `json:"timezone"`
	Type           string         `json:"type"`
	DeleteAfterRun bool           `json:"delete_after_run"`
	RepositoryID   *int           `json:"repository_id"`
	RunAt          *string        `json:"run_at"`
	TaskParams     *db.TaskParams `json:"task_params"`
}

type integrationCredentialFingerprint struct {
	TemplateID     *int                         `json:"template_id"`
	WorkflowID     *int                         `json:"workflow_id"`
	WorkflowUserID *int                         `json:"workflow_user_id"`
	AuthMethod     db.IntegrationAuthMethod     `json:"auth_method"`
	AuthSecretID   *int                         `json:"auth_secret_id"`
	AuthHeader     string                       `json:"auth_header"`
	TaskParams     *db.TaskParams               `json:"task_params"`
	Matchers       []db.IntegrationMatcher      `json:"matchers"`
	ExtractValues  []db.IntegrationExtractValue `json:"extract_values"`
}

// credentialPrincipalFingerprint hashes the current configuration of a
// trigger.
func (d *SqlDb) credentialPrincipalFingerprint(kind db.CredentialPrincipalKind, triggerID int) (string, error) {
	var payload any
	switch kind {
	case db.CredentialPrincipalSchedule:
		projectID, err := d.triggerProjectID("project__schedule", triggerID)
		if err != nil {
			return "", err
		}
		schedule, err := d.GetSchedule(projectID, triggerID)
		if err != nil {
			return "", err
		}
		var runAt *string
		if schedule.RunAt != nil {
			value := schedule.RunAt.UTC().Format(time.RFC3339Nano)
			runAt = &value
		}
		payload = scheduleCredentialFingerprint{
			TemplateID:     schedule.TemplateID,
			CronFormat:     schedule.CronFormat,
			Timezone:       schedule.Timezone,
			Type:           schedule.Type,
			DeleteAfterRun: schedule.DeleteAfterRun,
			RepositoryID:   schedule.RepositoryID,
			RunAt:          runAt,
			TaskParams:     schedule.TaskParams,
		}
	case db.CredentialPrincipalIntegration:
		projectID, err := d.triggerProjectID("project__integration", triggerID)
		if err != nil {
			return "", err
		}
		integration, err := d.GetIntegration(projectID, triggerID)
		if err != nil {
			return "", err
		}
		matchers, err := d.GetIntegrationMatchers(projectID, db.RetrieveQueryParams{}, triggerID)
		if err != nil {
			return "", err
		}
		extractValues, err := d.GetIntegrationExtractValues(projectID, db.RetrieveQueryParams{}, triggerID)
		if err != nil {
			return "", err
		}
		sort.Slice(matchers, func(i, j int) bool { return matchers[i].ID < matchers[j].ID })
		sort.Slice(extractValues, func(i, j int) bool { return extractValues[i].ID < extractValues[j].ID })
		payload = integrationCredentialFingerprint{
			TemplateID:     integration.TemplateID,
			WorkflowID:     integration.WorkflowID,
			WorkflowUserID: integration.WorkflowUserID,
			AuthMethod:     integration.AuthMethod,
			AuthSecretID:   integration.AuthSecretID,
			AuthHeader:     integration.AuthHeader,
			TaskParams:     integration.TaskParams,
			Matchers:       matchers,
			ExtractValues:  extractValues,
		}
	default:
		return "", db.ErrInvalidOperation
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (d *SqlDb) triggerProjectID(table string, triggerID int) (int, error) {
	var projectID int
	err := d.selectOne(&projectID, "select project_id from "+table+" where id=?", triggerID)
	return projectID, err
}
