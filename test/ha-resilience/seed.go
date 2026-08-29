package haresilience

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type identifier struct {
	ID int `json:"id"`
}

type templateView struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type approvalView struct {
	WorkflowNodeID int    `json:"workflow_node_id"`
	Status         string `json:"status"`
}

type runnerView struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Webhook            string `json:"webhook"`
	MaxParallelTasks   int    `json:"max_parallel_tasks"`
	Active             bool   `json:"active"`
	IsDefault          bool   `json:"is_default"`
	RegistrationPolicy string `json:"registration_policy"`
}

func (h *Harness) seed(ctx context.Context) error {
	if err := h.waitSQL(ctx, "select count(*) from runner where name='ha-resilience-runner';", 1, 90*time.Second); err != nil {
		return fmt.Errorf("wait for runner registration: %w", err)
	}
	if err := h.makeRunnerDefault(ctx); err != nil {
		return err
	}
	var project identifier
	if err := h.api.json(ctx, http.MethodPost, h.proxyURL+"/api/projects", map[string]any{
		"name": "HA resilience fixture", "demo": true,
	}, &project, http.StatusCreated); err != nil {
		return err
	}
	h.projectID = project.ID
	var templates []templateView
	if err := h.api.json(ctx, http.MethodGet, fmt.Sprintf("%s/api/project/%d/templates", h.proxyURL, h.projectID), nil, &templates, http.StatusOK); err != nil {
		return err
	}
	if len(templates) == 0 {
		return fmt.Errorf("demo project %d has no templates", h.projectID)
	}
	templateID := 0
	for _, template := range templates {
		if template.Name == "Print system info (Bash)" {
			templateID = template.ID
			break
		}
	}
	if templateID == 0 {
		return fmt.Errorf("demo project %d has no deterministic Bash template", h.projectID)
	}
	h.templateID = templateID
	if _, err := h.commands.compose(ctx, "restart", "server-b"); err != nil {
		return err
	}
	serverBURL, err := h.commands.port(ctx, "server-b", "3000")
	if err != nil {
		return err
	}
	h.serverBURL = serverBURL
	if err := h.api.waitStatus(ctx, h.serverBURL+"/api/ready", http.StatusOK); err != nil {
		return err
	}
	message := "Approve the HA resilience fixture?"
	var workflow identifier
	if err := h.api.json(ctx, http.MethodPost, fmt.Sprintf("%s/api/project/%d/workflows", h.proxyURL, h.projectID), map[string]any{
		"name": "HA approval workflow", "definition_version": 1,
		"nodes": []map[string]any{{
			"id": -1, "kind": "approval", "display_name": "HA gate", "approval_message": message,
		}},
		"edges": []any{},
	}, &workflow, http.StatusCreated); err != nil {
		return err
	}
	h.workflowID = workflow.ID
	if err := h.startWorkflowThroughBothNodes(ctx); err != nil {
		return err
	}
	if err := h.resolveApprovalThroughBothNodes(ctx); err != nil {
		return err
	}
	return h.stageRecoveryTask(ctx)
}

func (h *Harness) stageRecoveryTask(ctx context.Context) error {
	runAt := time.Now().UTC().Add(10 * time.Second).Truncate(time.Second)
	var schedule identifier
	if err := h.api.json(ctx, http.MethodPost, fmt.Sprintf("%s/api/project/%d/schedules", h.serverAURL, h.projectID), map[string]any{
		"name": "HA exactly once", "template_id": h.templateID, "active": true,
		"type": "run_at", "run_at": runAt.Format(time.RFC3339),
	}, &schedule, http.StatusCreated); err != nil {
		return err
	}
	h.scheduleID = schedule.ID
	if _, err := h.commands.compose(ctx, "stop", "runner"); err != nil {
		return fmt.Errorf("stop runner before recovery assignment: %w", err)
	}

	assignmentQuery := fmt.Sprintf(
		"select count(*) from task t join cluster__task_control c on c.task_id=t.id where t.schedule_id=%d and t.status='starting' and t.runner_id is not null and t.assignment_generation=1;",
		h.scheduleID,
	)
	if err := h.waitSQL(ctx, assignmentQuery, 1, 60*time.Second); err != nil {
		return fmt.Errorf("wait for fenced recovery assignment: %w", err)
	}
	value, err := h.commands.sql(ctx, fmt.Sprintf(
		"select t.id || '|' || n.node_id || '|' || c.owner_boot_id from task t join cluster__task_control c on c.task_id=t.id join cluster__node n on n.boot_id=c.owner_boot_id where t.schedule_id=%d and t.assignment_generation=1 order by t.id limit 1;",
		h.scheduleID,
	))
	if err != nil {
		return fmt.Errorf("read recovery task owner: %w", err)
	}
	assignment, err := parseRecoveryAssignment(value)
	if err != nil {
		return err
	}
	h.recoveryTaskID = assignment.TaskID
	h.recoveryOwnerNodeID = assignment.OwnerNodeID
	h.recoveryOwnerBootID = assignment.OwnerBootID
	return nil
}

type recoveryAssignment struct {
	TaskID      int
	OwnerNodeID string
	OwnerBootID string
}

func parseRecoveryAssignment(value string) (recoveryAssignment, error) {
	parts := strings.Split(strings.TrimSpace(value), "|")
	if len(parts) != 3 {
		return recoveryAssignment{}, fmt.Errorf("unexpected recovery assignment %q", value)
	}
	taskID, err := strconv.Atoi(parts[0])
	if err != nil || taskID <= 0 {
		return recoveryAssignment{}, fmt.Errorf("invalid recovery task id %q", parts[0])
	}
	if parts[1] != "server-a" && parts[1] != "server-b" {
		return recoveryAssignment{}, fmt.Errorf("unexpected recovery owner node %q", parts[1])
	}
	if len(parts[2]) != 32 {
		return recoveryAssignment{}, fmt.Errorf("unexpected recovery owner boot id %q", parts[2])
	}
	if _, err := strconv.ParseUint(parts[2][:16], 16, 64); err != nil {
		return recoveryAssignment{}, fmt.Errorf("invalid recovery owner boot id %q", parts[2])
	}
	if _, err := strconv.ParseUint(parts[2][16:], 16, 64); err != nil {
		return recoveryAssignment{}, fmt.Errorf("invalid recovery owner boot id %q", parts[2])
	}
	return recoveryAssignment{TaskID: taskID, OwnerNodeID: parts[1], OwnerBootID: parts[2]}, nil
}

func (h *Harness) makeRunnerDefault(ctx context.Context) error {
	var runners []runnerView
	if err := h.api.json(ctx, http.MethodGet, h.proxyURL+"/api/runners", nil, &runners, http.StatusOK); err != nil {
		return fmt.Errorf("list registered runners: %w", err)
	}
	for _, runner := range runners {
		if runner.Name != "ha-resilience-runner" {
			continue
		}
		runner.Active = true
		runner.IsDefault = true
		if err := h.api.json(ctx, http.MethodPut, fmt.Sprintf("%s/api/runners/%d", h.proxyURL, runner.ID), runner, nil, http.StatusNoContent); err != nil {
			return fmt.Errorf("mark HA runner as default: %w", err)
		}
		return nil
	}
	return fmt.Errorf("registered HA runner is absent from the admin API")
}

func (h *Harness) startWorkflowThroughBothNodes(ctx context.Context) error {
	urls := []string{h.serverAURL, h.serverBURL}
	ids := make(chan int, len(urls))
	errors := make(chan error, len(urls))
	var wait sync.WaitGroup
	for _, baseURL := range urls {
		wait.Add(1)
		go func(url string) {
			defer wait.Done()
			status, body, err := h.api.requestWithHeaders(ctx, http.MethodPost,
				fmt.Sprintf("%s/api/project/%d/workflows/%d/run", url, h.projectID, h.workflowID),
				map[string]any{}, map[string]string{"Idempotency-Key": "ha-resilience-run-once"})
			if err != nil {
				errors <- err
				return
			}
			if status != http.StatusCreated {
				errors <- fmt.Errorf("workflow start returned %d: %s", status, body)
				return
			}
			var run identifier
			if err := decodeJSON(body, &run); err != nil {
				errors <- err
				return
			}
			ids <- run.ID
		}(baseURL)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		return err
	}
	close(ids)
	for id := range ids {
		if h.runID == 0 {
			h.runID = id
		} else if h.runID != id {
			return fmt.Errorf("idempotent starts created run IDs %d and %d", h.runID, id)
		}
	}
	return h.waitSQL(ctx, fmt.Sprintf("select count(*) from project__workflow_approval where workflow_run_id=%d and status='pending';", h.runID), 1, 30*time.Second)
}

func (h *Harness) resolveApprovalThroughBothNodes(ctx context.Context) error {
	var approvals []approvalView
	url := fmt.Sprintf("%s/api/project/%d/workflows/%d/runs/%d/approvals", h.proxyURL, h.projectID, h.workflowID, h.runID)
	if err := h.api.json(ctx, http.MethodGet, url, nil, &approvals, http.StatusOK); err != nil {
		return err
	}
	if len(approvals) != 1 {
		return fmt.Errorf("expected one pending approval, got %d", len(approvals))
	}
	nodeID := approvals[0].WorkflowNodeID
	statuses := make(chan int, 2)
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for _, baseURL := range []string{h.serverAURL, h.serverBURL} {
		wait.Add(1)
		go func(url string) {
			defer wait.Done()
			status, _, err := h.api.request(ctx, http.MethodPost,
				fmt.Sprintf("%s/api/project/%d/workflows/%d/runs/%d/approvals/%d", url, h.projectID, h.workflowID, h.runID, nodeID),
				map[string]any{"status": "approved", "comment": "HA verified", "source": "user"})
			if err != nil {
				errors <- err
				return
			}
			statuses <- status
		}(baseURL)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		return err
	}
	close(statuses)
	successes := 0
	for status := range statuses {
		if status == http.StatusOK {
			successes++
		} else if status != http.StatusConflict && status != http.StatusBadRequest {
			return fmt.Errorf("concurrent approval returned unexpected status %d", status)
		}
	}
	if successes == 0 {
		return fmt.Errorf("neither concurrent approval decision succeeded")
	}
	return h.waitSQL(ctx, fmt.Sprintf("select count(*) from project__workflow_approval where workflow_run_id=%d and status='approved';", h.runID), 1, 30*time.Second)
}

func (h *Harness) waitSQL(ctx context.Context, query string, expected int, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := -1
	for {
		value, err := h.sqlInt(waitCtx, query)
		if err == nil {
			last = value
			if value == expected {
				return nil
			}
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("SQL invariant expected %d, last value %d: %w", expected, last, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func decodeJSON(data []byte, target any) error {
	return json.Unmarshal(data, target)
}
