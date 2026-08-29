package haresilience

import (
	"context"
	"fmt"
	"net/http"
)

func (h *Harness) collectInvariants(ctx context.Context) error {
	queries := []struct {
		name   string
		target *int
		query  string
	}{
		{
			name: "logical schedule occurrences", target: &h.report.Invariants.DuplicateSchedules,
			query: "select count(*) from (select schedule_id, intended_at from cluster__schedule_occurrence group by schedule_id, intended_at having count(*) > 1) duplicates;",
		},
		{
			name: "schedule tasks", target: &h.report.Invariants.DuplicateTasks,
			query: "select count(*) from (select schedule_occurrence_key from task where schedule_occurrence_key is not null group by schedule_occurrence_key having count(*) > 1) duplicates;",
		},
		{
			name: "workflow run nodes", target: &h.report.Invariants.DuplicateWorkflowNodes,
			query: "select count(*) from (select workflow_run_id, workflow_node_id from project__workflow_run_node group by workflow_run_id, workflow_node_id having count(*) > 1) duplicates;",
		},
		{
			name: "approval decisions", target: &h.report.Invariants.DuplicateApprovalDecisions,
			query: "select count(*) from (select workflow_run_id, workflow_node_id from project__workflow_approval where status <> 'pending' group by workflow_run_id, workflow_node_id having count(*) > 1) duplicates;",
		},
	}
	for _, invariant := range queries {
		value, err := h.sqlInt(ctx, invariant.query)
		if err != nil {
			return fmt.Errorf("query duplicate %s: %w", invariant.name, err)
		}
		*invariant.target = value
	}
	if err := h.verifySeedCardinality(ctx); err != nil {
		return err
	}
	var projects []identifier
	if err := h.api.json(ctx, http.MethodGet, h.proxyURL+"/api/projects", nil, &projects, http.StatusOK); err != nil {
		return err
	}
	existing := make(map[int]struct{}, len(projects))
	for _, project := range projects {
		existing[project.ID] = struct{}{}
	}
	h.report.Invariants.AcceptedWrites = len(h.acceptedIDs)
	for _, id := range h.acceptedIDs {
		if _, found := existing[id]; !found {
			h.report.Invariants.AcceptedWritesMissing++
		}
	}
	if len(h.acceptedIDs) == 0 {
		h.report.Invariants.AcceptedWritesMissing = 1
	}
	auditAfter, err := h.sqlInt(ctx, "select count(*) from event;")
	if err != nil {
		return err
	}
	h.report.Invariants.AuditEventsAfter = auditAfter
	h.report.Invariants.AuditContinuous = h.report.Invariants.AuditEventsBefore > 0 && auditAfter >= h.report.Invariants.AuditEventsBefore
	return nil
}

func (h *Harness) verifySeedCardinality(ctx context.Context) error {
	checks := []struct {
		name  string
		query string
	}{
		{name: "schedule occurrence", query: fmt.Sprintf("select count(*) from cluster__schedule_occurrence where schedule_id=%d and task_id is not null;", h.scheduleID)},
		{name: "schedule task", query: fmt.Sprintf("select count(*) from task where schedule_id=%d;", h.scheduleID)},
		{name: "workflow run", query: fmt.Sprintf("select count(*) from project__workflow_run where id=%d and correlation_id='ha-resilience-run-once';", h.runID)},
		{name: "workflow node", query: fmt.Sprintf("select count(*) from project__workflow_run_node where workflow_run_id=%d;", h.runID)},
		{name: "approval decision", query: fmt.Sprintf("select count(*) from project__workflow_approval where workflow_run_id=%d and status='approved';", h.runID)},
	}
	for _, check := range checks {
		count, err := h.sqlInt(ctx, check.query)
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("expected exactly one %s, got %d", check.name, count)
		}
	}
	return nil
}
