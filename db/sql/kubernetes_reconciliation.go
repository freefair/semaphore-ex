package sql

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
)

func kubernetesReconciliationFenceHash(fence string) string {
	sum := sha256.Sum256([]byte(fence))
	return fmt.Sprintf("%x", sum[:])
}

// OpenKubernetesReconciliationSession is the sole fence issuer. Namespace is
// checked against the installed server policy by the authenticated controller;
// this store persists it in the session and never derives it from object data.
func (d *SqlDb) OpenKubernetesReconciliationSession(runnerID int, clusterAlias, namespace, resumeSessionID, resumeFence string) (session db.KubernetesReconciliationSession, err error) {
	if runnerID <= 0 || db.ValidateKubernetesClusterAlias(clusterAlias) != nil || namespace == "" {
		return session, db.ErrKubernetesReconciliationSessionStale
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return session, err
	}
	defer func() { _ = tx.Rollback() }()
	lock := "select id from runner where id=?"
	if d.GetDialect() != util.DbDriverSQLite {
		lock += " for update"
	}
	var id int
	if err = tx.SelectOne(&id, d.PrepareQuery(lock), runnerID); err != nil {
		return session, err
	}
	if resumeSessionID != "" && resumeFence != "" {
		err = tx.SelectOne(&session, d.PrepareQuery("select session_id,runner_id,cluster_alias,namespace,scan_complete,scan_revision from kubernetes_reconciliation_session where session_id=? and runner_id=? and fence_hash=? and active=true"), resumeSessionID, runnerID, kubernetesReconciliationFenceHash(resumeFence))
		if err == nil && session.ClusterAlias == clusterAlias && session.Namespace == namespace {
			session.Fence = resumeFence
			session.Targets, err = kubernetesReconciliationTargetsTx(tx, d, session.SessionID)
			if err != nil {
				return session, err
			}
			return session, tx.Commit()
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return session, err
		}
	}
	if _, err = tx.Exec(d.PrepareQuery("update kubernetes_reconciliation_session set active=false where runner_id=? and active=true"), runnerID); err != nil {
		return session, err
	}
	if session.SessionID, err = db.NewKubernetesReconciliationToken(); err != nil {
		return session, err
	}
	if session.Fence, err = db.NewKubernetesReconciliationToken(); err != nil {
		return session, err
	}
	session.RunnerID, session.ClusterAlias, session.Namespace = runnerID, clusterAlias, namespace
	if _, err = tx.Exec(d.PrepareQuery("insert into kubernetes_reconciliation_session (session_id,runner_id,cluster_alias,namespace,fence_hash,active,scan_complete,scan_revision,created_at) values (?,?,?,?,?,true,false,0,?)"), session.SessionID, runnerID, clusterAlias, namespace, kubernetesReconciliationFenceHash(session.Fence), time.Now().UTC()); err != nil {
		return session, err
	}
	if err = d.snapshotKubernetesReconciliationTargetsTx(tx, session); err != nil {
		return session, err
	}
	session.Targets, err = kubernetesReconciliationTargetsTx(tx, d, session.SessionID)
	if err != nil {
		return session, err
	}
	return session, tx.Commit()
}

func (d *SqlDb) snapshotKubernetesReconciliationTargetsTx(tx *gorp.Transaction, session db.KubernetesReconciliationSession) error {
	var targets []db.KubernetesReconciliationTarget
	serverNow := time.Now().UTC()
	_, err := tx.Select(&targets, d.PrepareQuery("select project_id,task_id,generation,k8s_job_name as job_name,k8s_job_uid as job_uid,k8s_pod_name as pod_name,k8s_pod_uid as pod_uid,k8s_secret_name as secret_name,k8s_secret_uid as secret_uid,k8s_network_policy_name as network_policy_name,k8s_network_policy_uid as network_policy_uid,k8s_retention_deadline as retention_deadline,k8s_retention_state as retention_state from task__runner_attempt where runner_id=? and executor_type=? and k8s_cluster_alias=? and k8s_namespace=? and (outcome=? or (outcome in (?,?,?) and k8s_retention_state='terminal' and k8s_retention_deadline is not null and k8s_retention_deadline<?)) order by project_id,task_id,generation"), session.RunnerID, db.RunnerExecutorK8s, session.ClusterAlias, session.Namespace, db.RunnerAttemptActive, db.RunnerAttemptSucceeded, db.RunnerAttemptFailed, db.RunnerAttemptStopped, serverNow)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if target.Validate() != nil {
			// Never drop malformed durable provenance from a restart snapshot:
			// the candidate makes the session permanently non-ready until an
			// administrator remediates it, rather than silently permitting a
			// replacement dispatch for an ambiguous active attempt.
			if _, err = tx.Exec(d.PrepareQuery("insert into kubernetes_reconciliation_candidate (session_id,resource,name,uid,reason,revision,status,observed_at) values (?,?,?,?,?,?,?,?)"), session.SessionID, "job", target.JobName, target.JobUID, "malformed_provenance", 0, "pending", time.Now().UTC()); err != nil {
				return err
			}
			continue
		}
		if _, err = tx.Exec(d.PrepareQuery("insert into kubernetes_reconciliation_target (session_id,project_id,task_id,generation,job_name,job_uid,pod_name,pod_uid,secret_name,secret_uid,network_policy_name,network_policy_uid,retention_deadline,retention_state) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?)"), session.SessionID, target.ProjectID, target.TaskID, target.Generation, target.JobName, target.JobUID, target.PodName, target.PodUID, target.SecretName, target.SecretUID, target.NetworkPolicyName, target.NetworkPolicyUID, target.RetentionDeadline, target.RetentionState); err != nil {
			return err
		}
	}
	return nil
}

func kubernetesReconciliationTargetsTx(tx *gorp.Transaction, d *SqlDb, sessionID string) ([]db.KubernetesReconciliationTarget, error) {
	var targets []db.KubernetesReconciliationTarget
	_, err := tx.Select(&targets, d.PrepareQuery("select project_id,task_id,generation,job_name,job_uid,pod_name,pod_uid,secret_name,secret_uid,network_policy_name,network_policy_uid,retention_deadline,retention_state from kubernetes_reconciliation_target where session_id=? order by project_id,task_id,generation"), sessionID)
	return targets, err
}

// IngestKubernetesReconciliationScan accepts exactly the immutable target
// snapshot. A missing, duplicate, UID/label conflict or foreign managed object
// is represented as a durable quarantine/candidate and leaves Ready false.
func (d *SqlDb) IngestKubernetesReconciliationScan(runnerID int, scan db.KubernetesReconciliationScan) error {
	if runnerID <= 0 || scan.SessionID == "" || scan.Fence == "" || scan.Revision < 0 || len(scan.Observations) > 100 || len(scan.Candidates) > 100 {
		return db.ErrKubernetesReconciliationCoverageInvalid
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var session db.KubernetesReconciliationSession
	err = tx.SelectOne(&session, d.PrepareQuery("select session_id,runner_id,cluster_alias,namespace,scan_complete,scan_revision from kubernetes_reconciliation_session where session_id=? and runner_id=? and fence_hash=? and active=true"), scan.SessionID, runnerID, kubernetesReconciliationFenceHash(scan.Fence))
	if err != nil {
		return db.ErrKubernetesReconciliationSessionStale
	}
	session.Fence = scan.Fence
	if session.Ready {
		if session.Revision == scan.Revision {
			return tx.Commit()
		}
		return db.ErrKubernetesReconciliationRevisionConflict
	}
	if session.Revision != scan.Revision {
		return db.ErrKubernetesReconciliationRevisionConflict
	}
	targets, err := kubernetesReconciliationTargetsTx(tx, d, scan.SessionID)
	if err != nil {
		return err
	}
	if len(targets) != len(scan.Observations) {
		return db.ErrKubernetesReconciliationCoverageInvalid
	}
	seen := map[string]bool{}
	quarantined := len(scan.Candidates) > 0
	persistedCandidates, err := tx.SelectInt(d.PrepareQuery("select count(1) from kubernetes_reconciliation_candidate where session_id=? and status=?"), scan.SessionID, "pending")
	if err != nil {
		return err
	}
	quarantined = quarantined || persistedCandidates > 0
	for _, observation := range scan.Observations {
		if observation.Validate() != nil {
			return db.ErrKubernetesReconciliationCoverageInvalid
		}
		key := fmt.Sprintf("%d/%d/%d", observation.ProjectID, observation.TaskID, observation.Generation)
		if seen[key] {
			return db.ErrKubernetesReconciliationCoverageInvalid
		}
		seen[key] = true
		matched := false
		var target db.KubernetesReconciliationTarget
		for index := range targets {
			candidateTarget := targets[index]
			if candidateTarget.ProjectID == observation.ProjectID && candidateTarget.TaskID == observation.TaskID && candidateTarget.Generation == observation.Generation {
				matched = true
				target = candidateTarget
				// Keep the immutable target copy for state-specific validation
				// below; a runner must not convert an active object into an
				// absence acknowledgement and unlock a replacement dispatch.
				break
			}
		}
		if !matched {
			return db.ErrKubernetesReconciliationCoverageInvalid
		}
		if observation.State == db.KubernetesReconciliationAbsent && (target.RetentionState != "terminal" || target.RetentionDeadline == nil || !target.RetentionDeadline.Before(time.Now().UTC())) {
			return db.ErrKubernetesReconciliationCoverageInvalid
		}
		if observation.State == db.KubernetesReconciliationQuarantined {
			quarantined = true
		}
		var existing struct {
			State    db.KubernetesReconciliationState `db:"state"`
			Reason   string                           `db:"reason"`
			Revision int64                            `db:"revision"`
		}
		err = tx.SelectOne(&existing, d.PrepareQuery("select state,reason,revision from kubernetes_reconciliation_observation where session_id=? and project_id=? and task_id=? and generation=?"), scan.SessionID, observation.ProjectID, observation.TaskID, observation.Generation)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err = tx.Exec(d.PrepareQuery("insert into kubernetes_reconciliation_observation (session_id,project_id,task_id,generation,state,reason,revision,observed_at) values (?,?,?,?,?,?,?,?)"), scan.SessionID, observation.ProjectID, observation.TaskID, observation.Generation, observation.State, observation.Reason, observation.Revision, time.Now().UTC()); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if existing.State != observation.State || existing.Reason != observation.Reason || existing.Revision != observation.Revision {
			return db.ErrKubernetesReconciliationRevisionConflict
		}
	}
	for _, candidate := range scan.Candidates {
		if candidate.Validate() != nil {
			return db.ErrKubernetesReconciliationCoverageInvalid
		}
		var existing struct {
			Name     string `db:"name"`
			Reason   string `db:"reason"`
			Revision int64  `db:"revision"`
		}
		err = tx.SelectOne(&existing, d.PrepareQuery("select name,reason,revision from kubernetes_reconciliation_candidate where session_id=? and resource=? and uid=?"), scan.SessionID, candidate.Resource, candidate.UID)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err = tx.Exec(d.PrepareQuery("insert into kubernetes_reconciliation_candidate (session_id,resource,name,uid,reason,revision,status,observed_at) values (?,?,?,?,?,?,?,?)"), scan.SessionID, candidate.Resource, candidate.Name, candidate.UID, candidate.Reason, candidate.Revision, "pending", time.Now().UTC()); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if existing.Name != candidate.Name || existing.Reason != candidate.Reason || existing.Revision != candidate.Revision {
			return db.ErrKubernetesReconciliationRevisionConflict
		}
	}
	// Scanning a non-empty target set with any ambiguity never acknowledges the
	// session. An empty clean snapshot is safe and permits first dispatch.
	if quarantined {
		return tx.Commit()
	}
	result, err := tx.Exec(d.PrepareQuery("update kubernetes_reconciliation_session set scan_complete=true,scan_revision=scan_revision+1 where session_id=? and runner_id=? and fence_hash=? and active=true and scan_complete=false and scan_revision=?"), scan.SessionID, runnerID, kubernetesReconciliationFenceHash(scan.Fence), scan.Revision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return db.ErrKubernetesReconciliationSessionStale
	}
	return tx.Commit()
}

func (d *SqlDb) GetKubernetesReconciliationCommands(runnerID int, sessionID, fence string, limit int) ([]db.KubernetesReconciliationRemediationCommand, error) {
	if runnerID <= 0 || !validKubernetesSessionID(sessionID) || !validKubernetesSessionID(fence) || limit <= 0 || limit > dbMaxKubernetesReconciliationPageSize() {
		return nil, db.ErrKubernetesReconciliationSessionStale
	}
	var count int
	if err := d.selectOne(&count, "select count(1) from kubernetes_reconciliation_session where session_id=? and runner_id=? and fence_hash=? and active=true", sessionID, runnerID, kubernetesReconciliationFenceHash(fence)); err != nil || count != 1 {
		return nil, db.ErrKubernetesReconciliationSessionStale
	}
	type commandRow struct {
		CommandID         string                            `db:"command_id"`
		SessionID         string                            `db:"session_id"`
		Action            db.KubernetesReconciliationAction `db:"action"`
		ProjectID         int                               `db:"project_id"`
		TaskID            int                               `db:"task_id"`
		Generation        int                               `db:"generation"`
		ExpectedRevision  int64                             `db:"expected_revision"`
		JobName           string                            `db:"job_name"`
		JobUID            string                            `db:"job_uid"`
		PodName           string                            `db:"pod_name"`
		PodUID            string                            `db:"pod_uid"`
		SecretName        string                            `db:"secret_name"`
		SecretUID         string                            `db:"secret_uid"`
		NetworkPolicyName string                            `db:"network_policy_name"`
		NetworkPolicyUID  string                            `db:"network_policy_uid"`
		RetentionDeadline *time.Time                        `db:"retention_deadline"`
		RetentionState    string                            `db:"retention_state"`
	}
	rows := make([]commandRow, 0, limit)
	_, err := d.Sql().Select(&rows, d.PrepareQuery("select command_id,session_id,action,project_id,task_id,generation,expected_revision,job_name,job_uid,pod_name,pod_uid,secret_name,secret_uid,network_policy_name,network_policy_uid,retention_deadline,retention_state from kubernetes_reconciliation_command where runner_id=? and session_id=? and status='pending' order by created_at,command_id limit ?"), runnerID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	commands := make([]db.KubernetesReconciliationRemediationCommand, 0, len(rows))
	for _, row := range rows {
		target := db.KubernetesReconciliationTarget{ProjectID: row.ProjectID, TaskID: row.TaskID, Generation: row.Generation, JobName: row.JobName, JobUID: row.JobUID, PodName: row.PodName, PodUID: row.PodUID, SecretName: row.SecretName, SecretUID: row.SecretUID, NetworkPolicyName: row.NetworkPolicyName, NetworkPolicyUID: row.NetworkPolicyUID, RetentionDeadline: row.RetentionDeadline, RetentionState: row.RetentionState}
		commands = append(commands, db.KubernetesReconciliationRemediationCommand{CommandID: row.CommandID, SessionID: row.SessionID, Action: row.Action, ProjectID: row.ProjectID, TaskID: row.TaskID, Generation: row.Generation, ExpectedRevision: row.ExpectedRevision, Target: target})
	}
	return commands, nil
}

func validKubernetesSessionID(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value
}

func dbMaxKubernetesReconciliationPageSize() int { return 100 }

// GetKubernetesReconciliationPendingDiagnostics returns only durable blocked
// state for one runner. Candidate objects deliberately have no remediation
// action; administrators can inspect them but cannot turn labels into delete
// authority.
func (d *SqlDb) GetKubernetesReconciliationPendingDiagnostics(runnerID int, limit int) ([]db.KubernetesReconciliationDiagnostic, error) {
	if runnerID <= 0 || limit <= 0 || limit > dbMaxKubernetesReconciliationPageSize() {
		return nil, db.ErrKubernetesReconciliationCoverageInvalid
	}
	var sessionID string
	if err := d.selectOne(&sessionID, "select session_id from kubernetes_reconciliation_session where runner_id=? and active=true", runnerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
			return []db.KubernetesReconciliationDiagnostic{}, nil
		}
		return nil, err
	}
	type targetRow struct {
		db.KubernetesReconciliationTarget
		State  db.KubernetesReconciliationState `db:"state"`
		Reason string                           `db:"reason"`
	}
	rows := make([]targetRow, 0, limit)
	_, err := d.Sql().Select(&rows, d.PrepareQuery("select t.project_id,t.task_id,t.generation,t.job_name,t.job_uid,t.pod_name,t.pod_uid,t.secret_name,t.secret_uid,t.network_policy_name,t.network_policy_uid,t.retention_deadline,t.retention_state,o.state,o.reason from kubernetes_reconciliation_target t join kubernetes_reconciliation_observation o on o.session_id=t.session_id and o.project_id=t.project_id and o.task_id=t.task_id and o.generation=t.generation where t.session_id=? and o.state='quarantined' order by t.project_id,t.task_id,t.generation limit ?"), sessionID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]db.KubernetesReconciliationDiagnostic, 0, limit)
	for _, row := range rows {
		result = append(result, db.KubernetesReconciliationDiagnostic{SessionID: sessionID, Target: row.KubernetesReconciliationTarget, State: row.State, Reason: row.Reason})
	}
	if len(result) == limit {
		return result, nil
	}
	candidates := make([]db.KubernetesReconciliationCandidate, 0, limit-len(result))
	_, err = d.Sql().Select(&candidates, d.PrepareQuery("select resource,name,uid,reason,revision from kubernetes_reconciliation_candidate where session_id=? and status='pending' order by resource,uid limit ?"), sessionID, limit-len(result))
	if err != nil {
		return nil, err
	}
	for index := range candidates {
		result = append(result, db.KubernetesReconciliationDiagnostic{SessionID: sessionID, Candidate: &candidates[index], State: db.KubernetesReconciliationQuarantined, Reason: candidates[index].Reason})
	}
	return result, nil
}

func (d *SqlDb) RequestKubernetesReconciliationRemediation(runnerID int, request db.KubernetesReconciliationRemediationRequest) (command db.KubernetesReconciliationRemediationCommand, err error) {
	if runnerID <= 0 || request.Validate() != nil {
		return command, db.ErrKubernetesReconciliationRevisionConflict
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return command, err
	}
	defer func() { _ = tx.Rollback() }()
	lock := "select id from runner where id=?"
	if d.GetDialect() != util.DbDriverSQLite {
		lock += " for update"
	}
	var lockedRunner int
	if err = tx.SelectOne(&lockedRunner, d.PrepareQuery(lock), runnerID); err != nil {
		return command, err
	}
	type commandRow struct {
		CommandID         string                            `db:"command_id"`
		SessionID         string                            `db:"session_id"`
		Action            db.KubernetesReconciliationAction `db:"action"`
		ProjectID         int                               `db:"project_id"`
		TaskID            int                               `db:"task_id"`
		Generation        int                               `db:"generation"`
		ExpectedRevision  int64                             `db:"expected_revision"`
		JobName           string                            `db:"job_name"`
		JobUID            string                            `db:"job_uid"`
		PodName           string                            `db:"pod_name"`
		PodUID            string                            `db:"pod_uid"`
		SecretName        string                            `db:"secret_name"`
		SecretUID         string                            `db:"secret_uid"`
		NetworkPolicyName string                            `db:"network_policy_name"`
		NetworkPolicyUID  string                            `db:"network_policy_uid"`
		RetentionDeadline *time.Time                        `db:"retention_deadline"`
		RetentionState    string                            `db:"retention_state"`
	}
	var existing commandRow
	err = tx.SelectOne(&existing, d.PrepareQuery("select command_id,session_id,action,project_id,task_id,generation,expected_revision,job_name,job_uid,pod_name,pod_uid,secret_name,secret_uid,network_policy_name,network_policy_uid,retention_deadline,retention_state from kubernetes_reconciliation_command where runner_id=? and idempotency_key=?"), runnerID, request.IdempotencyKey)
	if err == nil {
		if existing.Action != request.Action || existing.ProjectID != request.ProjectID || existing.TaskID != request.TaskID || existing.Generation != request.Generation || existing.ExpectedRevision != request.ExpectedRevision {
			return command, db.ErrKubernetesReconciliationRevisionConflict
		}
		command = db.KubernetesReconciliationRemediationCommand{CommandID: existing.CommandID, SessionID: existing.SessionID, Action: existing.Action, ProjectID: existing.ProjectID, TaskID: existing.TaskID, Generation: existing.Generation, ExpectedRevision: existing.ExpectedRevision, Target: db.KubernetesReconciliationTarget{ProjectID: existing.ProjectID, TaskID: existing.TaskID, Generation: existing.Generation, JobName: existing.JobName, JobUID: existing.JobUID, PodName: existing.PodName, PodUID: existing.PodUID, SecretName: existing.SecretName, SecretUID: existing.SecretUID, NetworkPolicyName: existing.NetworkPolicyName, NetworkPolicyUID: existing.NetworkPolicyUID, RetentionDeadline: existing.RetentionDeadline, RetentionState: existing.RetentionState}}
		return command, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return command, err
	}
	var session struct {
		SessionID string `db:"session_id"`
		FenceHash string `db:"fence_hash"`
		Revision  int64  `db:"scan_revision"`
		Ready     bool   `db:"scan_complete"`
	}
	err = tx.SelectOne(&session, d.PrepareQuery("select session_id,fence_hash,scan_revision,scan_complete from kubernetes_reconciliation_session where runner_id=? and active=true"), runnerID)
	if err != nil {
		return command, db.ErrKubernetesReconciliationSessionStale
	}
	var target db.KubernetesReconciliationTarget
	err = tx.SelectOne(&target, d.PrepareQuery("select project_id,task_id,generation,job_name,job_uid,pod_name,pod_uid,secret_name,secret_uid,network_policy_name,network_policy_uid,retention_deadline,retention_state from kubernetes_reconciliation_target where session_id=? and project_id=? and task_id=? and generation=?"), session.SessionID, request.ProjectID, request.TaskID, request.Generation)
	if err != nil || target.Validate() != nil || target.RetentionState != "terminal" || target.RetentionDeadline == nil || !target.RetentionDeadline.Before(time.Now().UTC()) {
		return command, db.ErrKubernetesReconciliationRevisionConflict
	}
	var observation struct {
		State    db.KubernetesReconciliationState `db:"state"`
		Revision int64                            `db:"revision"`
	}
	err = tx.SelectOne(&observation, d.PrepareQuery("select state,revision from kubernetes_reconciliation_observation where session_id=? and project_id=? and task_id=? and generation=?"), session.SessionID, target.ProjectID, target.TaskID, target.Generation)
	if err != nil || !session.Ready || observation.State != db.KubernetesReconciliationObserved || observation.Revision != request.ExpectedRevision {
		return command, db.ErrKubernetesReconciliationRevisionConflict
	}
	commandID, err := db.NewKubernetesReconciliationToken()
	if err != nil {
		return command, err
	}
	command = db.KubernetesReconciliationRemediationCommand{CommandID: commandID, SessionID: session.SessionID, Action: request.Action, ProjectID: target.ProjectID, TaskID: target.TaskID, Generation: target.Generation, ExpectedRevision: request.ExpectedRevision, Target: target}
	_, err = tx.Exec(d.PrepareQuery("insert into kubernetes_reconciliation_command (command_id,session_id,runner_id,fence_hash,idempotency_key,action,project_id,task_id,generation,expected_revision,job_name,job_uid,pod_name,pod_uid,secret_name,secret_uid,network_policy_name,network_policy_uid,retention_deadline,retention_state,status,created_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"), command.CommandID, command.SessionID, runnerID, session.FenceHash, request.IdempotencyKey, command.Action, command.ProjectID, command.TaskID, command.Generation, command.ExpectedRevision, target.JobName, target.JobUID, target.PodName, target.PodUID, target.SecretName, target.SecretUID, target.NetworkPolicyName, target.NetworkPolicyUID, target.RetentionDeadline, target.RetentionState, db.KubernetesReconciliationRemediationPending, time.Now().UTC())
	if err != nil {
		return command, err
	}
	return command, tx.Commit()
}

func (d *SqlDb) ReportKubernetesReconciliationRemediation(runnerID int, sessionID, fence string, result db.KubernetesReconciliationRemediationResult) error {
	if runnerID <= 0 || !validKubernetesSessionID(sessionID) || !validKubernetesSessionID(fence) || result.Validate() != nil {
		return db.ErrKubernetesReconciliationSessionStale
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var active int
	if err = tx.SelectOne(&active, d.PrepareQuery("select count(1) from kubernetes_reconciliation_session where session_id=? and runner_id=? and fence_hash=? and active=true"), sessionID, runnerID, kubernetesReconciliationFenceHash(fence)); err != nil || active != 1 {
		return db.ErrKubernetesReconciliationSessionStale
	}
	var command struct {
		Status           db.KubernetesReconciliationRemediationStatus   `db:"status"`
		Evidence         db.KubernetesReconciliationRemediationEvidence `db:"evidence"`
		ProjectID        int                                            `db:"project_id"`
		TaskID           int                                            `db:"task_id"`
		Generation       int                                            `db:"generation"`
		ExpectedRevision int64                                          `db:"expected_revision"`
	}
	err = tx.SelectOne(&command, d.PrepareQuery("select status,evidence,project_id,task_id,generation,expected_revision from kubernetes_reconciliation_command where command_id=? and runner_id=? and session_id=? and fence_hash=?"), result.CommandID, runnerID, sessionID, kubernetesReconciliationFenceHash(fence))
	if err != nil {
		return db.ErrKubernetesReconciliationSessionStale
	}
	if command.Status != db.KubernetesReconciliationRemediationPending {
		if command.Status == result.Status && command.Evidence == result.Evidence {
			return tx.Commit()
		}
		return db.ErrKubernetesReconciliationRevisionConflict
	}
	if result.Status == db.KubernetesReconciliationRemediationBlocked {
		updated, updateErr := tx.Exec(d.PrepareQuery("update kubernetes_reconciliation_observation set state='quarantined',reason=?,revision=revision+1,observed_at=? where session_id=? and project_id=? and task_id=? and generation=? and revision=?"), string(result.Evidence), time.Now().UTC(), sessionID, command.ProjectID, command.TaskID, command.Generation, command.ExpectedRevision)
		if updateErr != nil {
			return updateErr
		}
		rows, rowsErr := updated.RowsAffected()
		if rowsErr != nil || rows != 1 {
			return db.ErrKubernetesReconciliationRevisionConflict
		}
	}
	_, err = tx.Exec(d.PrepareQuery("update kubernetes_reconciliation_command set status=?,evidence=?,reported_at=? where command_id=? and runner_id=? and session_id=? and fence_hash=? and status='pending'"), result.Status, result.Evidence, time.Now().UTC(), result.CommandID, runnerID, sessionID, kubernetesReconciliationFenceHash(fence))
	if err != nil {
		return err
	}
	return tx.Commit()
}
