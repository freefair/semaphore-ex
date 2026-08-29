package haresilience

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type clusterStatus struct {
	Nodes []clusterNode `json:"nodes"`
}

type clusterNode struct {
	NodeID             string `json:"node_id"`
	BootID             string `json:"boot_id"`
	Ready              bool   `json:"ready"`
	Version            string `json:"version"`
	Build              string `json:"build"`
	CompatibilityState string `json:"compatibility_state"`
}

type readinessView struct {
	Ready                    bool   `json:"ready"`
	AcceptingCoordinatedWork bool   `json:"accepting_coordinated_work"`
	State                    string `json:"state"`
}

func (h *Harness) pauseNode(ctx context.Context) error {
	return h.scenario(ctx, "node_pause", "server process paused after baseline work converged",
		"one direct node stops responding while the proxy stays available",
		"remove the paused node from traffic, inspect it, then unpause or replace it", func() ([]Assertion, error) {
			if _, err := h.commands.compose(ctx, "pause", "server-a"); err != nil {
				return nil, err
			}
			recovered := false
			defer func() {
				if !recovered {
					_, _ = h.commands.compose(context.Background(), "unpause", "server-a")
				}
			}()
			available := h.projectRead(ctx, h.proxyURL) == nil
			if _, err := h.commands.compose(ctx, "unpause", "server-a"); err != nil {
				return nil, err
			}
			recovered = true
			ready := h.waitReadiness(ctx, h.serverAURL, true, 45*time.Second) == nil
			return []Assertion{
				{Name: "proxy_api_available", Passed: available, Evidence: "authenticated project read routed through server-b"},
				{Name: "paused_node_recovers", Passed: ready, Evidence: "server-a returned ready after unpause"},
			}, nil
		})
}

func (h *Harness) killNode(ctx context.Context) error {
	return h.scenario(ctx, "node_kill", "task-owning server process killed without drain after a fenced runner assignment",
		"the killed owner disappears while the peer recovers the unacknowledged task and serves API traffic",
		"confirm the peer transfers ownership, safely requeues once, then restart or replace the failed node", func() ([]Assertion, error) {
			owner := h.recoveryOwnerNodeID
			if h.recoveryTaskID <= 0 || h.recoveryOwnerBootID == "" || (owner != "server-a" && owner != "server-b") {
				return nil, fmt.Errorf("recovery assignment was not staged")
			}
			ownerRestarted := false
			runnerStarted := false
			defer func() {
				if !runnerStarted {
					_, _ = h.commands.compose(context.Background(), "start", "runner")
				}
				if !ownerRestarted {
					_, _ = h.commands.compose(context.Background(), "up", "--detach", "--no-deps", owner)
				}
			}()
			if _, err := h.commands.compose(ctx, "kill", owner); err != nil {
				return nil, err
			}
			available := h.projectRead(ctx, h.proxyURL) == nil
			if _, err := h.commands.compose(ctx, "start", "runner"); err != nil {
				return nil, fmt.Errorf("restart runner for post-revocation evidence: %w", err)
			}
			runnerStarted = true
			transferQuery := fmt.Sprintf(
				"select count(*) from cluster__task_control where task_id=%d and owner_boot_id <> '%s' and previous_owner_boot_id='%s' and ownership_transferred_at is not null and fencing_token > 1;",
				h.recoveryTaskID, h.recoveryOwnerBootID, h.recoveryOwnerBootID,
			)
			ownerTransferred := h.waitSQL(ctx, transferQuery, 1, 45*time.Second) == nil

			terminalQuery := fmt.Sprintf(
				"select count(*) from task where id=%d and assignment_generation=2 and status in ('success','error','stopped');",
				h.recoveryTaskID,
			)
			terminal := h.waitSQL(ctx, terminalQuery, 1, 150*time.Second) == nil
			attemptCount, err := h.sqlInt(ctx, fmt.Sprintf("select count(*) from task__runner_attempt where task_id=%d;", h.recoveryTaskID))
			if err != nil {
				return nil, err
			}
			uniqueGenerations, err := h.sqlInt(ctx, fmt.Sprintf("select count(distinct generation) from task__runner_attempt where task_id=%d and generation in (1,2);", h.recoveryTaskID))
			if err != nil {
				return nil, err
			}
			requeued, err := h.sqlInt(ctx, fmt.Sprintf("select count(*) from task__runner_attempt where task_id=%d and generation=1 and outcome='requeued';", h.recoveryTaskID))
			if err != nil {
				return nil, err
			}
			logs, err := h.commands.compose(ctx, "logs", "--no-color", "server-a", "server-b")
			if err != nil {
				return nil, err
			}
			originalRaceAbsent := !strings.Contains(logs, "duplicate key value violates unique constraint \"task__runner_attempt_task_id_generation_key\"") &&
				!strings.Contains(logs, "task assignment changed concurrently")

			if _, err := h.commands.compose(ctx, "up", "--detach", "--no-deps", owner); err != nil {
				return nil, err
			}
			ownerRestarted = true
			if err := h.refreshURLs(ctx); err != nil {
				return nil, err
			}
			ownerURL := h.serverAURL
			if owner == "server-b" {
				ownerURL = h.serverBURL
			}
			ready := h.waitReadiness(ctx, ownerURL, true, 60*time.Second) == nil
			return []Assertion{
				{Name: "proxy_api_available", Passed: available, Evidence: "authenticated read succeeded after SIGKILL"},
				{Name: "task_owner_recovered", Passed: ownerTransferred && requeued == 1, Evidence: fmt.Sprintf("task %d owner %s/%s transferred with one safe requeue", h.recoveryTaskID, owner, h.recoveryOwnerBootID)},
				{Name: "runner_attempt_generations_unique", Passed: attemptCount == 2 && uniqueGenerations == 2, Evidence: fmt.Sprintf("task %d has %d attempts across %d expected generations", h.recoveryTaskID, attemptCount, uniqueGenerations)},
				{Name: "recovered_task_terminal", Passed: terminal, Evidence: fmt.Sprintf("task %d reached a terminal state on assignment generation 2", h.recoveryTaskID)},
				{Name: "original_dispatch_race_absent", Passed: originalRaceAbsent, Evidence: "server logs contain neither duplicate runner-attempt generation nor stale assignment conflict"},
				{Name: "killed_node_rejoins", Passed: ready, Evidence: fmt.Sprintf("%s registered a new ready process lifetime", owner)},
			}, nil
		})
}

func (h *Harness) partitionNode(ctx context.Context) error {
	return h.scenario(ctx, "node_partition", "server-a detached from the shared application network",
		"server-a loses SQL, Redis, and proxy reachability while server-b remains available",
		"isolate the partitioned node, restore network reachability, and wait for readiness", func() ([]Assertion, error) {
			containerID, err := h.commands.compose(ctx, "ps", "--quiet", "server-a")
			if err != nil {
				return nil, err
			}
			if _, err := h.commands.docker(ctx, "network", "disconnect", h.config.NetworkName, containerID); err != nil {
				return nil, err
			}
			connected := false
			defer func() {
				if !connected {
					_, _ = h.commands.docker(context.Background(), "network", "connect", h.config.NetworkName, containerID)
				}
			}()
			available := h.projectRead(ctx, h.proxyURL) == nil
			if _, err := h.commands.docker(ctx, "network", "connect", h.config.NetworkName, containerID); err != nil {
				return nil, err
			}
			connected = true
			ready := h.waitReadiness(ctx, h.serverAURL, true, 60*time.Second) == nil
			return []Assertion{
				{Name: "peer_api_available", Passed: available, Evidence: "server-b served the authenticated read"},
				{Name: "partitioned_node_converges", Passed: ready, Evidence: "server-a restored SQL/Redis readiness"},
			}, nil
		})
}

func (h *Harness) restartRedis(ctx context.Context) error {
	return h.scenario(ctx, "redis_restart", "Redis stopped after SQL-authoritative work converged",
		"live events and coordinated work degrade while SQL API traffic remains ready",
		"keep SQL traffic available, restore Redis, and wait for coordinated-work readiness", func() ([]Assertion, error) {
			if _, err := h.commands.compose(ctx, "stop", "redis"); err != nil {
				return nil, err
			}
			a, errA := h.readiness(ctx, h.serverAURL)
			b, errB := h.readiness(ctx, h.serverBURL)
			degraded := errA == nil && errB == nil && a.Ready && b.Ready && !a.AcceptingCoordinatedWork && !b.AcceptingCoordinatedWork
			available := h.projectRead(ctx, h.proxyURL) == nil
			if _, err := h.commands.compose(ctx, "start", "redis"); err != nil {
				return nil, err
			}
			recovered := h.waitReadiness(ctx, h.serverAURL, true, 60*time.Second) == nil && h.waitReadiness(ctx, h.serverBURL, true, 60*time.Second) == nil
			return []Assertion{
				{Name: "traffic_ready_while_redis_down", Passed: degraded && available, Evidence: fmt.Sprintf("server states %s/%s and authenticated read succeeded", a.State, b.State)},
				{Name: "coordinated_work_recovers", Passed: recovered, Evidence: "both nodes accept coordinated work after Redis restart"},
			}, nil
		})
}

func (h *Harness) loseDatabase(ctx context.Context) error {
	return h.scenario(ctx, "database_connection_loss", "PostgreSQL stopped after all accepted writes committed",
		"liveness stays available but readiness fails closed and no writes are accepted",
		"remove nodes from write traffic, restore PostgreSQL, and wait for readiness", func() ([]Assertion, error) {
			if _, err := h.commands.compose(ctx, "stop", "postgres"); err != nil {
				return nil, err
			}
			ping := h.api.waitStatus(ctx, h.proxyURL+"/api/ping", http.StatusOK) == nil
			statusA, _, errA := h.api.request(ctx, http.MethodGet, h.serverAURL+"/api/ready", nil)
			statusB, _, errB := h.api.request(ctx, http.MethodGet, h.serverBURL+"/api/ready", nil)
			failedClosed := errA == nil && errB == nil && statusA == http.StatusServiceUnavailable && statusB == http.StatusServiceUnavailable
			if _, err := h.commands.compose(ctx, "start", "postgres"); err != nil {
				return nil, err
			}
			recovered := h.waitReadiness(ctx, h.serverAURL, true, 90*time.Second) == nil && h.waitReadiness(ctx, h.serverBURL, true, 90*time.Second) == nil
			return []Assertion{
				{Name: "liveness_without_database", Passed: ping, Evidence: "proxy /api/ping remained HTTP 200"},
				{Name: "readiness_fails_closed", Passed: failedClosed, Evidence: fmt.Sprintf("direct readiness statuses %d/%d", statusA, statusB)},
				{Name: "database_recovery_converges", Passed: recovered, Evidence: "both nodes ready after PostgreSQL restart"},
			}, nil
		})
}

func (h *Harness) reconnectRunner(ctx context.Context) error {
	return h.scenario(ctx, "runner_reconnect", "registered runner stopped between task polls",
		"the runner heartbeat stops without creating a second runner identity",
		"restart the runner with its persisted token and verify the same identity reconnects", func() ([]Assertion, error) {
			before, err := h.runnerSnapshot(ctx)
			if err != nil {
				return nil, err
			}
			if _, err := h.commands.compose(ctx, "stop", "runner"); err != nil {
				return nil, err
			}
			if _, err := h.commands.compose(ctx, "start", "runner"); err != nil {
				return nil, err
			}
			after, err := h.waitRunnerTouched(ctx, before, 60*time.Second)
			if err != nil {
				return nil, err
			}
			return []Assertion{{
				Name: "runner_identity_reused", Passed: before.ID == after.ID && after.Count == 1 && after.Touched > before.Touched,
				Evidence: fmt.Sprintf("runner id %d count %d heartbeat %d->%d", after.ID, after.Count, before.Touched, after.Touched),
			}}, nil
		})
}

func (h *Harness) rollingReplacement(ctx context.Context) error {
	return h.scenario(ctx, "rolling_replacement", "server-a drained before replacement while server-b remains ready",
		"server-a returns 503 readiness; proxy traffic stays available; a new boot identity joins",
		"drain one node, replace it with a compatible build, wait for readiness, then continue", func() ([]Assertion, error) {
			oldA, err := h.currentNode(ctx, h.proxyURL, "server-a")
			if err != nil {
				return nil, err
			}
			peer, err := h.currentNode(ctx, h.proxyURL, "server-b")
			if err != nil {
				return nil, err
			}
			skewObserved := oldA.Version != peer.Version || oldA.Build != peer.Build
			status, _, err := h.api.request(ctx, http.MethodPost, fmt.Sprintf("%s/api/cluster/nodes/%s/draining", h.serverAURL, oldA.BootID), map[string]any{"draining": true})
			if err != nil || status != http.StatusNoContent {
				return nil, fmt.Errorf("drain server-a returned %d: %w", status, err)
			}
			drainStatus, _, drainErr := h.api.request(ctx, http.MethodGet, h.serverAURL+"/api/ready", nil)
			drained := drainErr == nil && drainStatus == http.StatusServiceUnavailable
			monitor := h.startProbeMonitor(ctx)
			h.commands.environment["SEMAPHORE_HA_SERVER_A_IMAGE"] = h.config.ReplacementImage
			replaceErr := make(chan error, 1)
			go func() {
				_, commandErr := h.commands.compose(ctx, "up", "--detach", "--no-deps", "--force-recreate", "server-a")
				replaceErr <- commandErr
			}()
			for index := 0; index < 8; index++ {
				var project identifier
				err := h.api.json(ctx, http.MethodPost, h.proxyURL+"/api/projects", map[string]any{
					"name": fmt.Sprintf("HA accepted during replacement %02d", index),
				}, &project, http.StatusCreated)
				if err == nil {
					h.acceptedIDs = append(h.acceptedIDs, project.ID)
				}
				time.Sleep(100 * time.Millisecond)
			}
			if err := <-replaceErr; err != nil {
				monitor.Stop()
				return nil, err
			}
			serverAURL, err := h.commands.port(ctx, "server-a", "3000")
			if err != nil {
				monitor.Stop()
				return nil, err
			}
			h.serverAURL = serverAURL
			ready := h.waitReadiness(ctx, h.serverAURL, true, 90*time.Second) == nil
			if !ready {
				monitor.Stop()
				return nil, fmt.Errorf("replacement server-a did not become ready")
			}
			if _, err := h.commands.compose(ctx, "exec", "--no-TTY", "proxy", "nginx", "-s", "reload"); err != nil {
				monitor.Stop()
				return nil, fmt.Errorf("reload proxy service discovery after replacement: %w", err)
			}
			if err := h.refreshURLs(ctx); err != nil {
				monitor.Stop()
				return nil, err
			}
			probeFailures := monitor.Stop()
			h.report.Invariants.APIProbeFailures = probeFailures
			newA, err := h.currentNode(ctx, h.proxyURL, "server-a")
			if err != nil {
				return nil, err
			}
			history, err := h.clusterNodes(ctx, h.proxyURL)
			if err != nil {
				return nil, err
			}
			oldHistory := false
			for _, node := range history {
				if node.BootID == oldA.BootID {
					oldHistory = true
				}
			}
			return []Assertion{
				{Name: "supported_skew_observed", Passed: skewObserved, Evidence: fmt.Sprintf("server-a %s/%s; server-b %s/%s", oldA.Version, oldA.Build, peer.Version, peer.Build)},
				{Name: "drain_removes_node_from_readiness", Passed: drained, Evidence: fmt.Sprintf("direct readiness status %d", drainStatus)},
				{Name: "api_continuously_available", Passed: probeFailures == 0, Evidence: fmt.Sprintf("continuous probe failures: %d", probeFailures)},
				{Name: "replacement_boot_converged", Passed: ready && newA.Ready && newA.BootID != oldA.BootID, Evidence: fmt.Sprintf("boot %s -> %s", oldA.BootID, newA.BootID)},
				{Name: "process_history_retained", Passed: oldHistory, Evidence: "old boot identity remains in SQL-backed cluster history"},
			}, nil
		})
}

func (h *Harness) scenario(_ context.Context, name, boundary, symptom, response string, run func() ([]Assertion, error)) error {
	scenario := Scenario{Name: name, StateBoundary: boundary, ExpectedSymptom: symptom, OperatorResponse: response, StartedAt: time.Now().UTC()}
	assertions, err := run()
	scenario.CompletedAt = time.Now().UTC()
	if err != nil {
		scenario.Assertions = []Assertion{{Name: "scenario_completed", Passed: false, Evidence: err.Error()}}
		h.report.Scenarios = append(h.report.Scenarios, scenario)
		return fmt.Errorf("scenario %s: %w", name, err)
	}
	scenario.Assertions = assertions
	h.report.Scenarios = append(h.report.Scenarios, scenario)
	for _, assertion := range assertions {
		if !assertion.Passed {
			return fmt.Errorf("scenario %s assertion %s failed: %s", name, assertion.Name, assertion.Evidence)
		}
	}
	return nil
}

func (h *Harness) projectRead(ctx context.Context, baseURL string) error {
	var projects []identifier
	return h.api.json(ctx, http.MethodGet, baseURL+"/api/projects", nil, &projects, http.StatusOK)
}

func (h *Harness) readiness(ctx context.Context, baseURL string) (readinessView, error) {
	var readiness readinessView
	err := h.api.json(ctx, http.MethodGet, baseURL+"/api/ready", nil, &readiness, http.StatusOK)
	return readiness, err
}

func (h *Harness) waitReadiness(ctx context.Context, baseURL string, accepting bool, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		readiness, err := h.readiness(waitCtx, baseURL)
		if err == nil && readiness.Ready && readiness.AcceptingCoordinatedWork == accepting {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("wait for readiness at %s: %w", baseURL, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (h *Harness) clusterNodes(ctx context.Context, baseURL string) ([]clusterNode, error) {
	var status clusterStatus
	if err := h.api.json(ctx, http.MethodGet, baseURL+"/api/cluster", nil, &status, http.StatusOK); err != nil {
		return nil, err
	}
	return status.Nodes, nil
}

func (h *Harness) currentNode(ctx context.Context, baseURL, nodeID string) (clusterNode, error) {
	nodes, err := h.clusterNodes(ctx, baseURL)
	if err != nil {
		return clusterNode{}, err
	}
	for index := len(nodes) - 1; index >= 0; index-- {
		if nodes[index].NodeID == nodeID && nodes[index].Ready {
			return nodes[index], nil
		}
	}
	return clusterNode{}, fmt.Errorf("ready cluster node %s not found", nodeID)
}

type runnerState struct {
	ID      int
	Count   int
	Touched int64
}

func (h *Harness) runnerSnapshot(ctx context.Context) (runnerState, error) {
	value, err := h.commands.sql(ctx, "select id || '|' || (select count(*) from runner where name='ha-resilience-runner') || '|' || coalesce(extract(epoch from touched)::bigint,0) from runner where name='ha-resilience-runner' order by id limit 1;")
	if err != nil {
		return runnerState{}, err
	}
	parts := strings.Split(strings.TrimSpace(value), "|")
	if len(parts) != 3 {
		return runnerState{}, fmt.Errorf("unexpected runner snapshot %q", value)
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		return runnerState{}, err
	}
	count, err := strconv.Atoi(parts[1])
	if err != nil {
		return runnerState{}, err
	}
	touched, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return runnerState{}, err
	}
	return runnerState{ID: id, Count: count, Touched: touched}, nil
}

func (h *Harness) waitRunnerTouched(ctx context.Context, before runnerState, timeout time.Duration) (runnerState, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		after, err := h.runnerSnapshot(waitCtx)
		if err == nil && after.ID == before.ID && after.Count == 1 && after.Touched > before.Touched {
			return after, nil
		}
		select {
		case <-waitCtx.Done():
			return runnerState{}, fmt.Errorf("wait for runner reconnect: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

type probeMonitor struct {
	cancel   context.CancelFunc
	done     chan struct{}
	failures atomic.Int64
}

func (h *Harness) startProbeMonitor(ctx context.Context) *probeMonitor {
	probeCtx, cancel := context.WithCancel(ctx)
	monitor := &probeMonitor{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(monitor.done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			status, _, err := h.api.request(probeCtx, http.MethodGet, h.proxyURL+"/api/ping", nil)
			if (err != nil || status != http.StatusOK) && probeCtx.Err() == nil {
				monitor.failures.Add(1)
			} else if err := h.projectRead(probeCtx, h.proxyURL); err != nil && probeCtx.Err() == nil {
				monitor.failures.Add(1)
			}
			select {
			case <-probeCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return monitor
}

func (m *probeMonitor) Stop() int {
	m.cancel()
	<-m.done
	return int(m.failures.Load())
}
