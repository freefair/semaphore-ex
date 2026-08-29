package haresilience

import (
	"errors"
	"fmt"
	"time"
)

type UpgradePolicy struct {
	Edition            string `json:"edition"`
	Protocol           string `json:"protocol"`
	Schema             string `json:"schema"`
	Capabilities       string `json:"capabilities"`
	ApplicationVersion string `json:"application_version"`
	Build              string `json:"build"`
}

type Assertion struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

type Scenario struct {
	Name             string      `json:"name"`
	StateBoundary    string      `json:"state_boundary"`
	ExpectedSymptom  string      `json:"expected_symptom"`
	OperatorResponse string      `json:"operator_response"`
	StartedAt        time.Time   `json:"started_at"`
	CompletedAt      time.Time   `json:"completed_at"`
	Assertions       []Assertion `json:"assertions"`
}

type Topology struct {
	DatabaseImage    string   `json:"database_image"`
	RedisImage       string   `json:"redis_image"`
	ProxyImage       string   `json:"proxy_image"`
	InitialImages    []string `json:"initial_server_images"`
	ReplacementImage string   `json:"replacement_server_image"`
	RunnerImage      string   `json:"runner_image"`
	NodeIDs          []string `json:"node_ids"`
}

type InvariantSummary struct {
	DuplicateSchedules         int  `json:"duplicate_logical_schedules"`
	DuplicateTasks             int  `json:"duplicate_schedule_tasks"`
	DuplicateWorkflowNodes     int  `json:"duplicate_workflow_nodes"`
	DuplicateApprovalDecisions int  `json:"duplicate_approval_decisions"`
	AcceptedWrites             int  `json:"accepted_writes"`
	AcceptedWritesMissing      int  `json:"accepted_writes_missing"`
	APIProbeFailures           int  `json:"api_probe_failures_during_supported_replacement"`
	AuditEventsBefore          int  `json:"audit_events_before"`
	AuditEventsAfter           int  `json:"audit_events_after"`
	AuditContinuous            bool `json:"audit_continuous"`
}

type Report struct {
	SchemaVersion int              `json:"schema_version"`
	Result        string           `json:"result"`
	StartedAt     time.Time        `json:"started_at"`
	CompletedAt   time.Time        `json:"completed_at"`
	Topology      Topology         `json:"topology"`
	UpgradePolicy UpgradePolicy    `json:"upgrade_policy"`
	Scenarios     []Scenario       `json:"scenarios"`
	Invariants    InvariantSummary `json:"invariants"`
}

func RequiredScenarioNames() []string {
	return []string{
		"node_pause",
		"node_kill",
		"node_partition",
		"redis_restart",
		"database_connection_loss",
		"runner_reconnect",
		"rolling_replacement",
	}
}

func (r Report) Validate() error {
	if r.Result != "passed" {
		return fmt.Errorf("report result is %q", r.Result)
	}
	seen := make(map[string]bool, len(r.Scenarios))
	for _, scenario := range r.Scenarios {
		seen[scenario.Name] = true
		if len(scenario.Assertions) == 0 {
			return fmt.Errorf("scenario %s has no assertions", scenario.Name)
		}
		for _, assertion := range scenario.Assertions {
			if !assertion.Passed {
				return fmt.Errorf("scenario %s assertion %s failed: %s", scenario.Name, assertion.Name, assertion.Evidence)
			}
		}
	}
	for _, required := range RequiredScenarioNames() {
		if !seen[required] {
			return fmt.Errorf("required scenario %s is missing", required)
		}
	}
	if r.Invariants.DuplicateSchedules+r.Invariants.DuplicateTasks+r.Invariants.DuplicateWorkflowNodes+r.Invariants.DuplicateApprovalDecisions != 0 {
		return errors.New("one or more uniqueness invariants failed")
	}
	if r.Invariants.AcceptedWrites == 0 || r.Invariants.AcceptedWritesMissing != 0 || r.Invariants.APIProbeFailures != 0 {
		return errors.New("accepted operations or supported-replacement probes did not converge")
	}
	if !r.Invariants.AuditContinuous {
		return errors.New("audit continuity was not proven")
	}
	return nil
}
