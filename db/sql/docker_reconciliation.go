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

const dockerReconciliationObservationColumns = "id, revision, sequence, runner_id, runner_boot, task_id, project_id, generation, resource, container_id, container_name, state, reason, observed_at, quarantine_status, remediation, remediation_reason, quarantined_at, remediated_at"
const dockerReconciliationStateColumns = "runner_id, runner_boot, project_id, task_id, generation, resource, revision, latest_sequence, container_id, container_name, state, reason, updated_at, quarantine_status, remediation, remediation_reason, quarantined_at, remediated_at"
const dockerReconciliationScanPageSize = 100
const dockerReconciliationRemediationCommandColumns = "command_id,session_id,runner_id,action,target,expected_revision,fingerprint,daemon_id,candidate_resource,candidate_session_id,candidate_identity,runner_boot,project_id,task_id,generation,resource,status"

func dockerReconciliationFenceHash(fence string) string {
	sum := sha256.Sum256([]byte(fence))
	return fmt.Sprintf("%x", sum[:])
}

// OpenDockerReconciliationSession either resumes the currently authenticated
// reporting session or atomically replaces it. Neither a client boot ID nor a
// boolean acknowledgement participates in that decision.
func (d *SqlDb) OpenDockerReconciliationSession(authenticatedRunnerID int, resumeSessionID string, resumeFence string) (session db.DockerReconciliationSession, err error) {
	if authenticatedRunnerID <= 0 {
		return session, fmt.Errorf("Docker runner identity is invalid")
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return session, err
	}
	defer func() { _ = tx.Rollback() }()
	runnerQuery := "select id from runner where id=?"
	if d.GetDialect() != util.DbDriverSQLite {
		runnerQuery += " for update"
	}
	var runnerID int
	if err = tx.SelectOne(&runnerID, d.PrepareQuery(runnerQuery), authenticatedRunnerID); err != nil {
		return session, err
	}
	if (db.DockerReconciliationSession{SessionID: resumeSessionID, Fence: resumeFence, RunnerID: authenticatedRunnerID, TargetBoot: "resume"}).ValidateCredentials() == nil {
		query := "select session_id, runner_id, target_boot, scan_complete, scan_highest_sequence, scan_cursor from docker_reconciliation_session where session_id=? and runner_id=? and fence_hash=? and active=true"
		err = tx.SelectOne(&session, d.PrepareQuery(query), resumeSessionID, authenticatedRunnerID, dockerReconciliationFenceHash(resumeFence))
		if err == nil {
			session.Fence = resumeFence
			session.ScanTargets, session.ScanTargetCount, err = dockerReconciliationScanTargetPageTx(tx, d, session.SessionID, session.ScanCursor)
			if err != nil {
				return db.DockerReconciliationSession{}, err
			}
			if err = tx.Commit(); err != nil {
				return db.DockerReconciliationSession{}, err
			}
			return session, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return session, err
		}
	}

	if _, err = tx.Exec(d.PrepareQuery("update docker_reconciliation_session set active=false where runner_id=? and active=true"), authenticatedRunnerID); err != nil {
		return session, err
	}
	if session.SessionID, err = db.NewDockerReconciliationToken(); err != nil {
		return session, err
	}
	if session.Fence, err = db.NewDockerReconciliationToken(); err != nil {
		return session, err
	}
	if session.TargetBoot, err = db.NewDockerReconciliationToken(); err != nil {
		return session, err
	}
	session.RunnerID = authenticatedRunnerID
	if _, err = tx.Exec(d.PrepareQuery("insert into docker_reconciliation_session (session_id,runner_id,fence_hash,target_boot,active,scan_complete,scan_cursor,created_at) values (?,?,?,?,true,false,0,?)"), session.SessionID, session.RunnerID, dockerReconciliationFenceHash(session.Fence), session.TargetBoot, time.Now().UTC()); err != nil {
		return db.DockerReconciliationSession{}, err
	}
	// A process restart invalidates the previous delivery fence. Pending admin
	// intent is rebound under the same runner lock so it is delivered to this
	// authenticated session rather than being stranded on the retired one.
	if _, err = tx.Exec(d.PrepareQuery("update docker_reconciliation_remediation_command set session_id=?,fence_hash=? where runner_id=? and status=?"), session.SessionID, dockerReconciliationFenceHash(session.Fence), session.RunnerID, db.DockerReconciliationRemediationPending); err != nil {
		return db.DockerReconciliationSession{}, err
	}
	// Snapshot the prior target boots under the same runner lock. The snapshot is
	// immutable for this session, so later dispatches cannot silently shrink what
	// the runner must reconcile before it becomes eligible again.
	if err = d.snapshotDockerReconciliationTargetsTx(tx, session); err != nil {
		return db.DockerReconciliationSession{}, err
	}
	session.ScanTargets, session.ScanTargetCount, err = dockerReconciliationScanTargetPageTx(tx, d, session.SessionID, 0)
	if err != nil {
		return db.DockerReconciliationSession{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.DockerReconciliationSession{}, err
	}
	return session, nil
}

// snapshotDockerReconciliationTargetsTx assigns an immutable per-owner
// sequence before the scan starts. A restarted runner therefore resumes from
// durable server cursors instead of replaying sequence 1 for every old boot.
func (d *SqlDb) snapshotDockerReconciliationTargetsTx(tx *gorp.Transaction, session db.DockerReconciliationSession) error {
	var attempts []db.DockerReconciliationScanTarget
	_, err := tx.Select(&attempts, d.PrepareQuery("select target_boot,project_id,task_id,generation,resource,container_name,0 as sequence from docker_reconciliation_attempt where runner_id=? and target_boot<>? order by target_boot,project_id,task_id,generation,resource"), session.RunnerID, session.TargetBoot)
	if err != nil {
		return err
	}
	last := make(map[string]int64)
	type ownerCursor struct {
		RunnerBoot   string `db:"runner_boot"`
		LastSequence int64  `db:"last_sequence"`
	}
	var cursors []ownerCursor
	if _, err = tx.Select(&cursors, d.PrepareQuery("select runner_boot,last_sequence from docker_reconciliation_runner where runner_id=?"), session.RunnerID); err != nil {
		return err
	}
	for _, cursor := range cursors {
		last[cursor.RunnerBoot] = cursor.LastSequence
	}
	for _, target := range attempts {
		last[target.TargetBoot]++
		target.Sequence = last[target.TargetBoot]
		if _, err = tx.Exec(d.PrepareQuery("insert into docker_reconciliation_scan_target (session_id,target_boot,project_id,task_id,generation,resource,container_name,sequence) values (?,?,?,?,?,?,?,?)"), session.SessionID, target.TargetBoot, target.ProjectID, target.TaskID, target.Generation, target.Resource, target.ContainerName, target.Sequence); err != nil {
			return err
		}
	}
	return nil
}

func (d *SqlDb) CompleteDockerReconciliationScan(authenticatedRunnerID int, complete db.DockerReconciliationScanComplete) error {
	if authenticatedRunnerID <= 0 || complete.Validate() != nil {
		return fmt.Errorf("invalid Docker reconciliation scan completion")
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	session, err := d.authenticatedDockerReconciliationSessionTx(tx, authenticatedRunnerID, complete.SessionID, complete.Fence, true)
	if err != nil {
		return err
	}
	expected, err := dockerReconciliationScanTargetsTx(tx, d, complete.SessionID)
	if err != nil {
		return err
	}
	if !sameDockerReconciliationTargets(expected, complete.CoveredTargets) {
		return db.ErrDockerReconciliationCoverageInvalid
	}
	// Every declared prior target must have durable report evidence in this
	// session. This rejects a forged completion list and a partial/empty scan
	// whenever the server snapshot contains unfinished targets.
	for _, target := range expected {
		count, countErr := tx.SelectInt(d.PrepareQuery("select count(1) from docker_reconciliation_scan_observation where session_id=? and target_boot=? and project_id=? and task_id=? and generation=? and resource=?"), complete.SessionID, target.TargetBoot, target.ProjectID, target.TaskID, target.Generation, target.Resource)
		if countErr != nil {
			return countErr
		}
		if count == 0 {
			return db.ErrDockerReconciliationCoverageInvalid
		}
	}
	var observedHighest int64
	if err = tx.SelectOne(&observedHighest, d.PrepareQuery("select coalesce(max(sequence),0) from docker_reconciliation_scan_observation where session_id=?"), complete.SessionID); err != nil {
		return err
	}
	if observedHighest != complete.HighestSequence {
		return db.ErrDockerReconciliationCoverageInvalid
	}
	if session.Ready {
		if session.HighestSequence != complete.HighestSequence {
			return db.ErrDockerReconciliationSequenceConflict
		}
		return tx.Commit()
	}
	result, err := tx.Exec(d.PrepareQuery("update docker_reconciliation_session set scan_complete=true, scan_highest_sequence=? where session_id=? and runner_id=? and fence_hash=? and active=true and scan_complete=false"), complete.HighestSequence, complete.SessionID, authenticatedRunnerID, dockerReconciliationFenceHash(complete.Fence))
	if err != nil {
		return err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		return db.ErrDockerReconciliationSessionStale
	}
	return tx.Commit()
}

func (d *SqlDb) BindDockerReconciliationAttempt(authenticatedRunnerID int, session db.DockerReconciliationSession, target db.DockerReconciliationScanTarget) error {
	return d.BindDockerReconciliationAttempts(authenticatedRunnerID, session, []db.DockerReconciliationScanTarget{target})
}

func (d *SqlDb) BindDockerReconciliationAttempts(authenticatedRunnerID int, session db.DockerReconciliationSession, targets []db.DockerReconciliationScanTarget) error {
	if authenticatedRunnerID <= 0 || session.RunnerID != authenticatedRunnerID || session.ValidateCredentials() != nil || len(targets) == 0 || len(targets) > 2 {
		return fmt.Errorf("invalid Docker reconciliation attempt")
	}
	seen := make(map[db.DockerReconciliationResource]struct{}, len(targets))
	for _, target := range targets {
		if target.Validate() != nil || target.TargetBoot != session.TargetBoot {
			return fmt.Errorf("invalid Docker reconciliation attempt")
		}
		if _, exists := seen[target.Resource]; exists {
			return fmt.Errorf("duplicate Docker reconciliation attempt resource")
		}
		seen[target.Resource] = struct{}{}
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stored, err := d.authenticatedDockerReconciliationSessionTx(tx, authenticatedRunnerID, session.SessionID, session.Fence, true)
	if err != nil || !stored.Ready || stored.TargetBoot != session.TargetBoot {
		if err != nil {
			return err
		}
		return db.ErrDockerReconciliationSessionStale
	}
	for _, target := range targets {
		observation := db.DockerReconciliationObservation{RunnerID: authenticatedRunnerID, RunnerBoot: stored.TargetBoot, ProjectID: target.ProjectID, TaskID: target.TaskID, Generation: target.Generation, Resource: target.Resource}
		if err = d.validateDockerReconciliationAttempt(tx, observation); err != nil {
			return err
		}
		var existing struct {
			TargetBoot    string `db:"target_boot"`
			ContainerName string `db:"container_name"`
		}
		err = tx.SelectOne(&existing, d.PrepareQuery("select target_boot,container_name from docker_reconciliation_attempt where runner_id=? and project_id=? and task_id=? and generation=? and resource=?"), authenticatedRunnerID, target.ProjectID, target.TaskID, target.Generation, target.Resource)
		if err == nil {
			if existing.TargetBoot != stored.TargetBoot || existing.ContainerName != target.ContainerName {
				return db.ErrDockerReconciliationSessionStale
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(d.PrepareQuery("insert into docker_reconciliation_attempt (runner_id,target_boot,project_id,task_id,generation,resource,container_name) values (?,?,?,?,?,?,?)"), authenticatedRunnerID, stored.TargetBoot, target.ProjectID, target.TaskID, target.Generation, target.Resource, target.ContainerName); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *SqlDb) QuarantineDockerReconciliationAttempt(authenticatedRunnerID int, sessionID string, fence string, quarantine db.DockerReconciliationStopQuarantine) error {
	if authenticatedRunnerID <= 0 || quarantine.ProjectID <= 0 || quarantine.TaskID <= 0 || quarantine.Generation <= 0 || quarantine.Resource != db.DockerReconciliationResourceTask || quarantine.ContainerName == "" || len(quarantine.Reason) == 0 || len(quarantine.Reason) > 128 {
		return db.ErrDockerReconciliationCoverageInvalid
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	session, err := d.authenticatedDockerReconciliationSessionTx(tx, authenticatedRunnerID, sessionID, fence, true)
	if err != nil || !session.Ready {
		if err != nil {
			return err
		}
		return db.ErrDockerReconciliationSessionStale
	}
	var bound struct {
		TargetBoot    string `db:"target_boot"`
		ContainerName string `db:"container_name"`
	}
	err = tx.SelectOne(&bound, d.PrepareQuery("select target_boot,container_name from docker_reconciliation_attempt where runner_id=? and project_id=? and task_id=? and generation=? and resource=?"), authenticatedRunnerID, quarantine.ProjectID, quarantine.TaskID, quarantine.Generation, quarantine.Resource)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrDockerReconciliationSessionStale
		}
		return err
	}
	if bound.TargetBoot != session.TargetBoot || bound.ContainerName != quarantine.ContainerName {
		return db.ErrDockerReconciliationSessionStale
	}
	owner := db.DockerReconciliationOwner{RunnerID: authenticatedRunnerID, RunnerBoot: bound.TargetBoot}
	var existing db.DockerReconciliationStateRecord
	err = tx.SelectOne(&existing, d.PrepareQuery("select "+dockerReconciliationStateColumns+" from docker_reconciliation_state where runner_id=? and runner_boot=? and project_id=? and task_id=? and generation=? and resource=?"), authenticatedRunnerID, bound.TargetBoot, quarantine.ProjectID, quarantine.TaskID, quarantine.Generation, quarantine.Resource)
	if err == nil {
		if existing.State == db.DockerReconciliationQuarantine && existing.QuarantineStatus == db.DockerReconciliationQuarantinePending && existing.ContainerName == quarantine.ContainerName && existing.Reason == quarantine.Reason && existing.RemediationReason == quarantine.Reason {
			return tx.Commit()
		}
		return db.ErrDockerReconciliationSequenceConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = d.ensureDockerReconciliationOwner(tx, owner); err != nil {
		return err
	}
	last, err := d.lockDockerReconciliationOwner(tx, owner)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	observation := db.DockerReconciliationObservation{Sequence: last + 1, RunnerID: authenticatedRunnerID, RunnerBoot: bound.TargetBoot, ProjectID: quarantine.ProjectID, TaskID: quarantine.TaskID, Generation: quarantine.Generation, Resource: quarantine.Resource, ContainerName: quarantine.ContainerName, State: db.DockerReconciliationQuarantine, Reason: quarantine.Reason, ObservedAt: now, QuarantineStatus: db.DockerReconciliationQuarantinePending, Remediation: db.DockerReconciliationRemediationInspect, RemediationReason: quarantine.Reason, QuarantinedAt: &now}
	if err = d.appendDockerReconciliationObservationTx(tx, authenticatedRunnerID, observation); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *SqlDb) authenticatedDockerReconciliationSessionTx(tx *gorp.Transaction, runnerID int, sessionID string, fence string, activeOnly bool) (session db.DockerReconciliationSession, err error) {
	if !validDockerSessionCredentials(sessionID, fence) {
		return session, db.ErrDockerReconciliationSessionStale
	}
	query := "select session_id,runner_id,target_boot,scan_complete,scan_highest_sequence,scan_cursor from docker_reconciliation_session where session_id=? and runner_id=? and fence_hash=?"
	if activeOnly {
		query += " and active=true"
	}
	if d.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	err = tx.SelectOne(&session, d.PrepareQuery(query), sessionID, runnerID, dockerReconciliationFenceHash(fence))
	if errors.Is(err, sql.ErrNoRows) {
		return db.DockerReconciliationSession{}, db.ErrDockerReconciliationSessionStale
	}
	if err == nil {
		session.Fence = fence
	}
	return session, err
}

func validDockerSessionCredentials(sessionID string, fence string) bool {
	return sessionID != "" && fence != "" && len(sessionID) <= 128 && len(fence) <= 128
}

func dockerReconciliationScanTargetsTx(tx *gorp.Transaction, d *SqlDb, sessionID string) ([]db.DockerReconciliationScanTarget, error) {
	var targets []db.DockerReconciliationScanTarget
	_, err := tx.Select(&targets, d.PrepareQuery("select target_boot,project_id,task_id,generation,resource,container_name,sequence from docker_reconciliation_scan_target where session_id=? order by target_boot,project_id,task_id,generation,resource"), sessionID)
	return targets, err
}

func dockerReconciliationScanTargetPageTx(tx *gorp.Transaction, d *SqlDb, sessionID string, cursor int64) ([]db.DockerReconciliationScanTarget, int, error) {
	if cursor < 0 {
		return nil, 0, db.ErrDockerReconciliationCoverageInvalid
	}
	count, err := tx.SelectInt(d.PrepareQuery("select count(1) from docker_reconciliation_scan_target where session_id=?"), sessionID)
	if err != nil {
		return nil, 0, err
	}
	var targets []db.DockerReconciliationScanTarget
	_, err = tx.Select(&targets, d.PrepareQuery("select target_boot,project_id,task_id,generation,resource,container_name,sequence from docker_reconciliation_scan_target where session_id=? order by target_boot,project_id,task_id,generation,resource limit ? offset ?"), sessionID, dockerReconciliationScanPageSize, cursor)
	return targets, int(count), err
}

func sameDockerReconciliationTargets(left []db.DockerReconciliationScanTarget, right []db.DockerReconciliationScanTarget) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]struct{}, len(left))
	for _, target := range left {
		seen[target.ScanKey()] = struct{}{}
	}
	for _, target := range right {
		if _, ok := seen[target.ScanKey()]; !ok {
			return false
		}
		delete(seen, target.ScanKey())
	}
	return len(seen) == 0
}

// CreateDockerReconciliationSessionObservation is the authenticated report
// path used by the runner API. The report never supplies its runner identity:
// it is injected from the authenticated session, and the target boot must be
// one that the server bound to a concrete Docker attempt.
func (d *SqlDb) CreateDockerReconciliationSessionObservation(authenticatedRunnerID int, sessionID string, fence string, observation db.DockerReconciliationObservation) (state db.DockerReconciliationStateRecord, replayed bool, err error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return state, false, err
	}
	defer func() { _ = tx.Rollback() }()
	session, err := d.authenticatedDockerReconciliationSessionTx(tx, authenticatedRunnerID, sessionID, fence, true)
	if err != nil {
		return state, false, err
	}
	if observation.RunnerID != 0 && observation.RunnerID != authenticatedRunnerID {
		return state, false, fmt.Errorf("Docker reconciliation runner ownership is invalid")
	}
	observation.RunnerID = authenticatedRunnerID
	if err = observation.Validate(); err != nil {
		return state, false, err
	}
	var bound struct {
		TargetBoot    string `db:"target_boot"`
		ContainerName string `db:"container_name"`
	}
	err = tx.SelectOne(&bound, d.PrepareQuery("select target_boot,container_name from docker_reconciliation_attempt where runner_id=? and project_id=? and task_id=? and generation=? and resource=?"), authenticatedRunnerID, observation.ProjectID, observation.TaskID, observation.Generation, observation.Resource)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return state, false, db.ErrDockerReconciliationSessionStale
		}
		return state, false, err
	}
	if bound.TargetBoot != observation.RunnerBoot || (observation.ContainerName != "" && observation.ContainerName != bound.ContainerName) {
		return state, false, db.ErrDockerReconciliationSessionStale
	}
	if session.TargetBoot == observation.RunnerBoot && !session.Ready {
		return state, false, db.ErrDockerReconciliationSessionStale
	}
	// Commit the immutable canonical observation before recording the scan
	// evidence. The latter is an idempotent projection of the same sequence;
	// completion checks both the target snapshot and that durable projection.
	if err = tx.Commit(); err != nil {
		return state, false, err
	}
	state, replayed, err = d.CreateDockerReconciliationObservation(authenticatedRunnerID, observation)
	if err != nil {
		return state, replayed, err
	}
	if session.TargetBoot != observation.RunnerBoot {
		_, err = d.exec("insert into docker_reconciliation_scan_observation (session_id,target_boot,project_id,task_id,generation,resource,sequence) values (?,?,?,?,?,?,?)", sessionID, observation.RunnerBoot, observation.ProjectID, observation.TaskID, observation.Generation, observation.Resource, observation.Sequence)
		if err != nil && !isDuplicateDockerScanObservation(err) {
			return state, replayed, err
		}
	}
	return state, replayed, nil
}

func isDuplicateDockerScanObservation(err error) bool {
	// The existing immutable observation append is authoritative. A duplicate
	// scan-evidence insert is only an idempotent retry; database drivers expose
	// different concrete duplicate-key errors, so re-read is safer than parsing.
	return err != nil && (strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate"))
}

// IngestDockerReconciliationScan is the only scan-boundary write path. It
// validates the complete immutable snapshot before advancing any state, then
// commits observations, evidence, high-water mark, and dispatch readiness in
// the same transaction.
func (d *SqlDb) IngestDockerReconciliationScan(runnerID int, scan db.DockerReconciliationScan) error {
	if runnerID <= 0 || len(scan.Observations) > 100 {
		return db.ErrDockerReconciliationCoverageInvalid
	}
	scan.Complete.SessionID, scan.Complete.Fence = scan.SessionID, scan.Fence
	if scan.Complete.Validate() != nil {
		return db.ErrDockerReconciliationCoverageInvalid
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	session, err := d.authenticatedDockerReconciliationSessionTx(tx, runnerID, scan.SessionID, scan.Fence, true)
	if err != nil {
		return err
	}
	if scan.Complete.ScanCursor != session.ScanCursor {
		return db.ErrDockerReconciliationSessionStale
	}
	expected, total, err := dockerReconciliationScanTargetPageTx(tx, d, scan.SessionID, session.ScanCursor)
	if err != nil {
		return err
	}
	if !sameDockerReconciliationTargets(expected, scan.Complete.CoveredTargets) || len(expected) != len(scan.Observations) {
		return db.ErrDockerReconciliationCoverageInvalid
	}
	expectedByKey := make(map[string]db.DockerReconciliationScanTarget, len(expected))
	for _, target := range expected {
		expectedByKey[target.ScanKey()] = target
	}
	seen := make(map[string]struct{}, len(scan.Observations))
	var high int64
	for _, observation := range scan.Observations {
		observation.RunnerID = runnerID
		if observation.Validate() != nil {
			return db.ErrDockerReconciliationCoverageInvalid
		}
		key := db.DockerReconciliationScanTarget{TargetBoot: observation.RunnerBoot, ProjectID: observation.ProjectID, TaskID: observation.TaskID, Generation: observation.Generation, Resource: observation.Resource, ContainerName: observation.ContainerName}.ScanKey()
		target, ok := expectedByKey[key]
		if !ok {
			return db.ErrDockerReconciliationCoverageInvalid
		}
		if _, duplicate := seen[key]; duplicate {
			return db.ErrDockerReconciliationCoverageInvalid
		}
		seen[key] = struct{}{}
		if observation.Sequence != target.Sequence {
			return db.ErrDockerReconciliationCoverageInvalid
		}
		if observation.Sequence > high {
			high = observation.Sequence
		}
	}
	if high != scan.Complete.HighestSequence {
		return db.ErrDockerReconciliationCoverageInvalid
	}
	for _, observation := range scan.Observations {
		if err := d.appendDockerReconciliationObservationTx(tx, runnerID, observation); err != nil {
			return err
		}
		if _, err := tx.Exec(d.PrepareQuery("insert into docker_reconciliation_scan_observation (session_id,target_boot,project_id,task_id,generation,resource,sequence) values (?,?,?,?,?,?,?)"), scan.SessionID, observation.RunnerBoot, observation.ProjectID, observation.TaskID, observation.Generation, observation.Resource, observation.Sequence); err != nil {
			return err
		}
	}
	nextCursor := session.ScanCursor + int64(len(expected))
	ready := nextCursor == int64(total)
	if nextCursor > int64(total) {
		return db.ErrDockerReconciliationCoverageInvalid
	}
	result, err := tx.Exec(d.PrepareQuery("update docker_reconciliation_session set scan_complete=?, scan_highest_sequence=?, scan_cursor=? where session_id=? and runner_id=? and fence_hash=? and active=true and scan_complete=false and scan_cursor=?"), ready, high, nextCursor, scan.SessionID, runnerID, dockerReconciliationFenceHash(scan.Fence), session.ScanCursor)
	if err != nil {
		return err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return db.ErrDockerReconciliationSessionStale
	}
	return tx.Commit()
}

// RecordDockerReconciliationOrphanCandidates stores bounded, runner-owned
// quarantine candidates separately from the exact scan tuple transaction. A
// candidate can never make a session ready and is never acted on here.
func (d *SqlDb) RecordDockerReconciliationOrphanCandidates(runnerID int, sessionID string, fence string, candidates []db.DockerReconciliationOrphanCandidate) error {
	if runnerID <= 0 || len(candidates) == 0 || len(candidates) > 100 {
		return db.ErrDockerReconciliationCoverageInvalid
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = d.authenticatedDockerReconciliationSessionTx(tx, runnerID, sessionID, fence, true); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate.RunnerID = runnerID
		if candidate.Validate() != nil {
			return db.ErrDockerReconciliationCoverageInvalid
		}
		fingerprint := dockerReconciliationOrphanFingerprint(candidate)
		if _, duplicate := seen[fingerprint]; duplicate {
			continue
		}
		seen[fingerprint] = struct{}{}
		query := "insert into docker_reconciliation_orphan_candidate (session_id,runner_id,resource,identifier,name,reason,fingerprint,identity,observed_at) values (?,?,?,?,?,?,?,?,?) on conflict(session_id,fingerprint) do nothing"
		if d.GetDialect() == util.DbDriverMySQL {
			query = "insert into docker_reconciliation_orphan_candidate (session_id,runner_id,resource,identifier,name,reason,fingerprint,identity,observed_at) values (?,?,?,?,?,?,?,?,?) on duplicate key update fingerprint=fingerprint"
		}
		if _, err = tx.Exec(d.PrepareQuery(query), sessionID, runnerID, candidate.Resource, candidate.Identifier, candidate.Name, candidate.Reason, fingerprint, candidate.Identity, candidate.ObservedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func dockerReconciliationOrphanFingerprint(candidate db.DockerReconciliationOrphanCandidate) string {
	value := string(candidate.Resource) + "\x00" + candidate.Identifier + "\x00" + candidate.Name + "\x00" + candidate.Identity + "\x00" + string(candidate.Reason)
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

// GetDockerReconciliationCandidates exposes only the bounded candidate record
// that was stored by the authenticated runner. It never asks the daemon for a
// fresh inventory, so an administrator read cannot have side effects.
func (d *SqlDb) GetDockerReconciliationCandidates(runnerID int, sessionID string, query db.DockerReconciliationCandidateQuery) ([]db.DockerReconciliationOrphanCandidate, error) {
	if runnerID <= 0 || !validDockerSessionID(sessionID) || query.Validate() != nil {
		return nil, db.ErrDockerReconciliationCoverageInvalid
	}
	values := make([]db.DockerReconciliationOrphanCandidate, 0, query.Limit)
	_, err := d.Sql().Select(&values, d.PrepareQuery("select runner_id,resource,identifier,name,reason,fingerprint,identity,observed_at,revision,status,remediated_at from docker_reconciliation_orphan_candidate where runner_id=? and session_id=? and fingerprint>? order by fingerprint asc limit ?"), runnerID, sessionID, query.AfterFingerprint, query.Limit)
	return values, err
}

func (d *SqlDb) GetDockerReconciliationPendingStates(owner db.DockerReconciliationOwner, query db.DockerReconciliationQuery) (page db.DockerReconciliationStatePage, err error) {
	if owner.Validate() != nil || query.Validate() != nil {
		return page, db.ErrDockerReconciliationCoverageInvalid
	}
	values := make([]db.DockerReconciliationStateRecord, 0, query.Limit+1)
	_, err = d.Sql().Select(&values, d.PrepareQuery("select "+dockerReconciliationStateColumns+" from docker_reconciliation_state where runner_id=? and runner_boot=? and quarantine_status=? and latest_sequence>? order by latest_sequence asc limit ?"), owner.RunnerID, owner.RunnerBoot, db.DockerReconciliationQuarantinePending, query.AfterSequence, query.Limit+1)
	if err != nil {
		return page, err
	}
	if len(values) > query.Limit {
		cursor := values[query.Limit-1].LatestSequence
		page.NextCursor = &cursor
		values = values[:query.Limit]
	}
	page.States = values
	return page, nil
}

func (d *SqlDb) GetDockerReconciliationPendingCandidates(runnerID int, sessionID string, query db.DockerReconciliationCandidateQuery) (page db.DockerReconciliationCandidatePage, err error) {
	if runnerID <= 0 || !validDockerSessionID(sessionID) || query.Validate() != nil {
		return page, db.ErrDockerReconciliationCoverageInvalid
	}
	values := make([]db.DockerReconciliationOrphanCandidate, 0, query.Limit+1)
	_, err = d.Sql().Select(&values, d.PrepareQuery("select runner_id,resource,identifier,name,reason,fingerprint,identity,observed_at,revision,status,remediated_at from docker_reconciliation_orphan_candidate where runner_id=? and session_id=? and status=? and fingerprint>? order by fingerprint asc limit ?"), runnerID, sessionID, db.DockerReconciliationCandidatePending, query.AfterFingerprint, query.Limit+1)
	if err != nil {
		return page, err
	}
	if len(values) > query.Limit {
		cursor := values[query.Limit-1].Fingerprint
		page.NextCursor = &cursor
		values = values[:query.Limit]
	}
	page.Candidates = values
	return page, nil
}

func validDockerSessionID(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value
}

// RequestDockerReconciliationRemediation persists an admin's desired action.
// It deliberately has no Docker client dependency: only a later authenticated
// runner process can observe or alter daemon resources.
func (d *SqlDb) RequestDockerReconciliationRemediation(runnerID int, request db.DockerReconciliationRemediationRequest) (command db.DockerReconciliationRemediationCommand, err error) {
	if runnerID <= 0 || request.Validate() != nil || (request.Quarantine != nil && request.Quarantine.RunnerID != runnerID) {
		return command, db.ErrDockerReconciliationCommandStale
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return command, err
	}
	defer func() { _ = tx.Rollback() }()
	var existing db.DockerReconciliationRemediationCommand
	err = tx.SelectOne(&existing, d.PrepareQuery("select "+dockerReconciliationRemediationCommandColumns+" from docker_reconciliation_remediation_command where runner_id=? and idempotency_key=?"), runnerID, request.IdempotencyKey)
	if err == nil {
		matches := existing.Action == request.Action && existing.Target == request.Target && existing.ExpectedRevision == request.ExpectedRevision
		if request.Target == db.DockerReconciliationRemediationTargetQuarantine {
			matches = matches && request.Quarantine != nil && existing.RunnerBoot == request.Quarantine.RunnerBoot && existing.ProjectID == request.Quarantine.ProjectID && existing.TaskID == request.Quarantine.TaskID && existing.Generation == request.Quarantine.Generation && existing.Resource == request.Quarantine.Resource
			existing.Quarantine = &db.DockerReconciliationKey{DockerReconciliationOwner: db.DockerReconciliationOwner{RunnerID: runnerID, RunnerBoot: existing.RunnerBoot}, ProjectID: existing.ProjectID, TaskID: existing.TaskID, Generation: existing.Generation, Resource: existing.Resource}
		} else {
			matches = matches && existing.CandidateSessionID == request.SessionID && existing.Fingerprint == request.Fingerprint
		}
		if !matches {
			return command, db.ErrDockerReconciliationImmutableMutation
		}
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return command, err
	}
	// Serialize active-session selection with OpenDockerReconciliationSession.
	// Without this lock, a restart could retire the selected fence between this
	// read and INSERT, leaving an accepted command undeliverable.
	runnerQuery := "select id from runner where id=?"
	if d.GetDialect() != util.DbDriverSQLite {
		runnerQuery += " for update"
	}
	var lockedRunnerID int
	if err = tx.SelectOne(&lockedRunnerID, d.PrepareQuery(runnerQuery), runnerID); err != nil {
		return command, err
	}
	var session struct {
		SessionID string `db:"session_id"`
		FenceHash string `db:"fence_hash"`
	}
	err = tx.SelectOne(&session, d.PrepareQuery("select session_id,fence_hash from docker_reconciliation_session where runner_id=? and active=true"), runnerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return command, db.ErrDockerReconciliationCommandStale
		}
		return command, err
	}
	command = db.DockerReconciliationRemediationCommand{SessionID: session.SessionID, RunnerID: runnerID, Action: request.Action, Target: request.Target, ExpectedRevision: request.ExpectedRevision, Status: db.DockerReconciliationRemediationPending}
	if request.Target == db.DockerReconciliationRemediationTargetQuarantine {
		var state db.DockerReconciliationStateRecord
		if err = d.selectDockerReconciliationStateTx(tx, *request.Quarantine, &state); err != nil {
			return command, db.ErrDockerReconciliationCommandStale
		}
		if state.Revision != request.ExpectedRevision || state.QuarantineStatus != db.DockerReconciliationQuarantinePending || state.ContainerID == "" {
			return command, db.ErrDockerReconciliationCommandStale
		}
		command.Quarantine, command.DaemonID = request.Quarantine, state.ContainerID
		command.Fingerprint = dockerRemediationFingerprint(command.Target, state.ContainerID, state.RunnerBoot, state.ProjectID, state.TaskID, state.Generation, string(state.Resource))
	} else {
		var candidate db.DockerReconciliationOrphanCandidate
		err = tx.SelectOne(&candidate, d.PrepareQuery("select runner_id,resource,identifier,name,reason,fingerprint,identity,observed_at,revision,status,remediated_at from docker_reconciliation_orphan_candidate where runner_id=? and session_id=? and fingerprint=?"), runnerID, request.SessionID, request.Fingerprint)
		if err != nil || candidate.Revision != request.ExpectedRevision || candidate.Status != db.DockerReconciliationCandidatePending || candidate.Identifier == "" || (candidate.Resource == db.DockerReconciliationCandidateVolume && !db.ValidDockerReconciliationOpaqueHash(candidate.Identity)) {
			return command, db.ErrDockerReconciliationCommandStale
		}
		command.Fingerprint, command.DaemonID, command.CandidateResource, command.CandidateSessionID, command.CandidateIdentity = candidate.Fingerprint, candidate.Identifier, candidate.Resource, request.SessionID, candidate.Identity
	}
	command.CommandID, err = db.NewDockerReconciliationToken()
	if err != nil {
		return command, err
	}
	boot, projectID, taskID, generation, resource := "", 0, 0, 0, ""
	if command.Quarantine != nil {
		boot, projectID, taskID, generation, resource = command.Quarantine.RunnerBoot, command.Quarantine.ProjectID, command.Quarantine.TaskID, command.Quarantine.Generation, string(command.Quarantine.Resource)
	}
	// The runner lock prevents replacement on supported SQL engines; SQLite
	// serializes writers. Recheck the exact active fence immediately before the
	// insert so every accepted command has a current delivery session.
	var activeSession struct {
		SessionID string `db:"session_id"`
		FenceHash string `db:"fence_hash"`
	}
	err = tx.SelectOne(&activeSession, d.PrepareQuery("select session_id,fence_hash from docker_reconciliation_session where runner_id=? and session_id=? and fence_hash=? and active=true"), runnerID, session.SessionID, session.FenceHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return command, db.ErrDockerReconciliationCommandStale
		}
		return command, err
	}
	_, err = tx.Exec(d.PrepareQuery("insert into docker_reconciliation_remediation_command (command_id,session_id,runner_id,fence_hash,idempotency_key,action,target,expected_revision,fingerprint,daemon_id,candidate_resource,candidate_session_id,candidate_identity,runner_boot,project_id,task_id,generation,resource,status,created_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"), command.CommandID, command.SessionID, runnerID, session.FenceHash, request.IdempotencyKey, command.Action, command.Target, command.ExpectedRevision, command.Fingerprint, command.DaemonID, command.CandidateResource, command.CandidateSessionID, command.CandidateIdentity, boot, projectID, taskID, generation, resource, command.Status, time.Now().UTC())
	if err != nil {
		return command, err
	}
	return command, tx.Commit()
}

func dockerRemediationFingerprint(target db.DockerReconciliationRemediationTarget, daemonID, boot string, projectID, taskID, generation int, resource string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%s", target, daemonID, boot, projectID, taskID, generation, resource)))
	return fmt.Sprintf("%x", sum[:])
}

func (d *SqlDb) GetDockerReconciliationRemediationCommands(runnerID int, sessionID string, fence string, limit int) ([]db.DockerReconciliationRemediationCommand, error) {
	if runnerID <= 0 || !validDockerSessionID(sessionID) || !validDockerSessionID(fence) || limit <= 0 || limit > dockerReconciliationScanPageSize {
		return nil, db.ErrDockerReconciliationCommandStale
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = d.authenticatedDockerReconciliationSessionTx(tx, runnerID, sessionID, fence, true); err != nil {
		return nil, err
	}
	commands := make([]db.DockerReconciliationRemediationCommand, 0, limit)
	_, err = tx.Select(&commands, d.PrepareQuery("select "+dockerReconciliationRemediationCommandColumns+" from docker_reconciliation_remediation_command where runner_id=? and session_id=? and status='pending' order by created_at,command_id limit ?"), runnerID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	for i := range commands {
		if commands[i].Target == db.DockerReconciliationRemediationTargetQuarantine {
			commands[i].Quarantine = &db.DockerReconciliationKey{DockerReconciliationOwner: db.DockerReconciliationOwner{RunnerID: runnerID, RunnerBoot: commands[i].RunnerBoot}, ProjectID: commands[i].ProjectID, TaskID: commands[i].TaskID, Generation: commands[i].Generation, Resource: commands[i].Resource}
		}
	}
	return commands, tx.Commit()
}

// ReportDockerReconciliationRemediation accepts bounded runner evidence and,
// for a successful removal only, resolves the exact revision-fenced target in
// the same transaction. Replays are accepted only when they repeat the stored
// result; cross-session and stale-session reports cannot reach the target.
func (d *SqlDb) ReportDockerReconciliationRemediation(runnerID int, sessionID string, fence string, result db.DockerReconciliationRemediationResult) error {
	if runnerID <= 0 || !validDockerSessionID(sessionID) || !validDockerSessionID(fence) || result.Validate() != nil {
		return db.ErrDockerReconciliationCommandStale
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = d.authenticatedDockerReconciliationSessionTx(tx, runnerID, sessionID, fence, true); err != nil {
		return err
	}
	var command struct {
		CommandID          string                                     `db:"command_id"`
		Target             db.DockerReconciliationRemediationTarget   `db:"target"`
		ExpectedRevision   int64                                      `db:"expected_revision"`
		Fingerprint        string                                     `db:"fingerprint"`
		Status             db.DockerReconciliationRemediationStatus   `db:"status"`
		Evidence           db.DockerReconciliationRemediationEvidence `db:"evidence"`
		RunnerBoot         string                                     `db:"runner_boot"`
		ProjectID          int                                        `db:"project_id"`
		TaskID             int                                        `db:"task_id"`
		Generation         int                                        `db:"generation"`
		Resource           db.DockerReconciliationResource            `db:"resource"`
		CandidateSessionID string                                     `db:"candidate_session_id"`
	}
	err = tx.SelectOne(&command, d.PrepareQuery("select command_id,target,expected_revision,fingerprint,status,evidence,runner_boot,project_id,task_id,generation,resource,candidate_session_id from docker_reconciliation_remediation_command where command_id=? and runner_id=? and session_id=? and fence_hash=?"), result.CommandID, runnerID, sessionID, dockerReconciliationFenceHash(fence))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrDockerReconciliationCommandStale
		}
		return err
	}
	if command.Fingerprint != result.Fingerprint {
		return db.ErrDockerReconciliationCommandStale
	}
	if command.Status != db.DockerReconciliationRemediationPending {
		if command.Status == result.Status && command.Evidence == result.Evidence {
			return tx.Commit()
		}
		return db.ErrDockerReconciliationCommandStale
	}
	if result.Status == db.DockerReconciliationRemediationSucceeded {
		now := time.Now().UTC()
		if command.Target == db.DockerReconciliationRemediationTargetQuarantine {
			updated, updateErr := tx.Exec(d.PrepareQuery("update docker_reconciliation_state set revision=revision+1, quarantine_status=?, remediation=?, remediated_at=?, updated_at=? where runner_id=? and runner_boot=? and project_id=? and task_id=? and generation=? and resource=? and revision=? and quarantine_status=?"), db.DockerReconciliationQuarantineRemediated, db.DockerReconciliationRemediationRemove, now, now, runnerID, command.RunnerBoot, command.ProjectID, command.TaskID, command.Generation, command.Resource, command.ExpectedRevision, db.DockerReconciliationQuarantinePending)
			if updateErr != nil {
				return updateErr
			}
			if count, countErr := updated.RowsAffected(); countErr != nil || count != 1 {
				return db.ErrDockerReconciliationCommandStale
			}
		} else {
			updated, updateErr := tx.Exec(d.PrepareQuery("update docker_reconciliation_orphan_candidate set revision=revision+1,status=?,remediated_at=? where runner_id=? and session_id=? and fingerprint=? and revision=? and status=?"), db.DockerReconciliationCandidateResolved, now, runnerID, command.CandidateSessionID, command.Fingerprint, command.ExpectedRevision, db.DockerReconciliationCandidatePending)
			if updateErr != nil {
				return updateErr
			}
			if count, countErr := updated.RowsAffected(); countErr != nil || count != 1 {
				return db.ErrDockerReconciliationCommandStale
			}
		}
	}
	_, err = tx.Exec(d.PrepareQuery("update docker_reconciliation_remediation_command set status=?,evidence=?,reported_at=? where command_id=? and runner_id=? and session_id=? and fence_hash=? and status='pending'"), result.Status, result.Evidence, time.Now().UTC(), result.CommandID, runnerID, sessionID, dockerReconciliationFenceHash(fence))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *SqlDb) appendDockerReconciliationObservationTx(tx *gorp.Transaction, runnerID int, observation db.DockerReconciliationObservation) error {
	if observation.RunnerID != runnerID {
		return db.ErrDockerReconciliationSessionStale
	}
	if err := d.validateDockerReconciliationAttempt(tx, observation); err != nil {
		return err
	}
	if err := d.ensureDockerReconciliationOwner(tx, observation.Owner()); err != nil {
		return err
	}
	last, err := d.lockDockerReconciliationOwner(tx, observation.Owner())
	if err != nil {
		return err
	}
	var existing db.DockerReconciliationObservation
	err = tx.SelectOne(&existing, d.PrepareQuery("select "+dockerReconciliationObservationColumns+" from docker_reconciliation_observation where runner_id=? and runner_boot=? and sequence=?"), observation.RunnerID, observation.RunnerBoot, observation.Sequence)
	if err == nil {
		if !sameDockerReconciliationPayload(existing, observation) {
			return db.ErrDockerReconciliationSequenceConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) || observation.Sequence <= last {
		return db.ErrDockerReconciliationSequenceConflict
	}
	if _, err = tx.Exec(d.PrepareQuery("insert into docker_reconciliation_observation (revision,sequence,runner_id,runner_boot,task_id,project_id,generation,resource,container_id,container_name,state,reason,observed_at,quarantine_status,remediation,remediation_reason,quarantined_at,remediated_at) values (1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"), observation.Sequence, observation.RunnerID, observation.RunnerBoot, observation.TaskID, observation.ProjectID, observation.Generation, observation.Resource, observation.ContainerID, observation.ContainerName, observation.State, observation.Reason, observation.ObservedAt, observation.QuarantineStatus, observation.Remediation, observation.RemediationReason, observation.QuarantinedAt, observation.RemediatedAt); err != nil {
		return err
	}
	var state db.DockerReconciliationStateRecord
	if err = d.advanceDockerReconciliationState(tx, observation, time.Now().UTC(), &state); err != nil {
		return err
	}
	result, err := tx.Exec(d.PrepareQuery("update docker_reconciliation_runner set last_sequence=? where runner_id=? and runner_boot=? and last_sequence=?"), observation.Sequence, observation.RunnerID, observation.RunnerBoot, last)
	if err != nil {
		return err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return db.ErrDockerReconciliationSequenceConflict
	}
	return nil
}

func (d *SqlDb) RecordDockerReconciliationObservation(authenticatedRunnerID int, observation db.DockerReconciliationObservation) error {
	_, _, err := d.CreateDockerReconciliationObservation(authenticatedRunnerID, observation)
	return err
}

// CreateDockerReconciliationObservation is the sole runner-report write path.
// It accepts the authenticated runner separately from the report and validates
// the task-attempt tuple before the immutable observation and canonical state
// are advanced in one transaction.
func (d *SqlDb) CreateDockerReconciliationObservation(authenticatedRunnerID int, observation db.DockerReconciliationObservation) (state db.DockerReconciliationStateRecord, replayed bool, err error) {
	if authenticatedRunnerID <= 0 || authenticatedRunnerID != observation.RunnerID {
		return state, false, fmt.Errorf("Docker reconciliation runner ownership is invalid")
	}
	if err = observation.Validate(); err != nil {
		return state, false, err
	}
	observation.ID = 0
	observation.Revision = 0

	tx, err := d.Sql().Begin()
	if err != nil {
		return state, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = d.validateDockerReconciliationAttempt(tx, observation); err != nil {
		return state, false, err
	}
	if err = d.ensureDockerReconciliationOwner(tx, observation.Owner()); err != nil {
		return state, false, err
	}
	lastSequence, err := d.lockDockerReconciliationOwner(tx, observation.Owner())
	if err != nil {
		return state, false, err
	}

	var existing db.DockerReconciliationObservation
	err = tx.SelectOne(&existing, d.PrepareQuery("select "+dockerReconciliationObservationColumns+" from docker_reconciliation_observation where runner_id=? and runner_boot=? and sequence=?"), observation.RunnerID, observation.RunnerBoot, observation.Sequence)
	if err == nil {
		if !sameDockerReconciliationPayload(existing, observation) {
			return state, false, db.ErrDockerReconciliationSequenceConflict
		}
		if err = d.selectDockerReconciliationStateTx(tx, observation.Key(), &state); err != nil {
			return state, false, err
		}
		if err = tx.Commit(); err != nil {
			return state, false, err
		}
		return state, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return state, false, err
	}
	if observation.Sequence <= lastSequence {
		return state, false, db.ErrDockerReconciliationSequenceConflict
	}

	serverNow := time.Now().UTC()
	observation.Revision = 1
	if _, err = tx.Exec(d.PrepareQuery("insert into docker_reconciliation_observation (revision,sequence,runner_id,runner_boot,task_id,project_id,generation,resource,container_id,container_name,state,reason,observed_at,quarantine_status,remediation,remediation_reason,quarantined_at,remediated_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"), observation.Revision, observation.Sequence, observation.RunnerID, observation.RunnerBoot, observation.TaskID, observation.ProjectID, observation.Generation, observation.Resource, observation.ContainerID, observation.ContainerName, observation.State, observation.Reason, observation.ObservedAt, observation.QuarantineStatus, observation.Remediation, observation.RemediationReason, observation.QuarantinedAt, observation.RemediatedAt); err != nil {
		return state, false, err
	}
	if err = d.advanceDockerReconciliationState(tx, observation, serverNow, &state); err != nil {
		return state, false, err
	}
	result, err := tx.Exec(d.PrepareQuery("update docker_reconciliation_runner set last_sequence=? where runner_id=? and runner_boot=? and last_sequence=?"), observation.Sequence, observation.RunnerID, observation.RunnerBoot, lastSequence)
	if err != nil {
		return state, false, err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		return state, false, db.ErrDockerReconciliationSequenceConflict
	}
	if err = tx.Commit(); err != nil {
		return state, false, err
	}
	return state, false, nil
}

func (d *SqlDb) GetDockerReconciliationObservation(owner db.DockerReconciliationOwner, sequence int64) (observation db.DockerReconciliationObservation, err error) {
	if err = owner.Validate(); err != nil {
		return observation, err
	}
	if sequence <= 0 {
		return observation, fmt.Errorf("invalid Docker reconciliation sequence")
	}
	err = d.selectOne(&observation, "select "+dockerReconciliationObservationColumns+" from docker_reconciliation_observation where runner_id=? and runner_boot=? and sequence=?", owner.RunnerID, owner.RunnerBoot, sequence)
	return observation, err
}

func (d *SqlDb) GetDockerReconciliationObservations(owner db.DockerReconciliationOwner, query db.DockerReconciliationQuery) ([]db.DockerReconciliationObservation, error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	observations := make([]db.DockerReconciliationObservation, 0, query.Limit)
	_, err := d.Sql().Select(&observations, d.PrepareQuery("select "+dockerReconciliationObservationColumns+" from docker_reconciliation_observation where runner_id=? and runner_boot=? and sequence>? order by sequence asc limit ?"), owner.RunnerID, owner.RunnerBoot, query.AfterSequence, query.Limit)
	return observations, err
}

func (d *SqlDb) GetDockerReconciliationState(key db.DockerReconciliationKey) (state db.DockerReconciliationStateRecord, err error) {
	if err = key.Validate(); err != nil {
		return state, err
	}
	err = d.selectOne(&state, "select "+dockerReconciliationStateColumns+" from docker_reconciliation_state where runner_id=? and runner_boot=? and project_id=? and task_id=? and generation=? and resource=?", key.RunnerID, key.RunnerBoot, key.ProjectID, key.TaskID, key.Generation, key.Resource)
	return state, err
}

func (d *SqlDb) GetDockerReconciliationStates(owner db.DockerReconciliationOwner, query db.DockerReconciliationQuery) ([]db.DockerReconciliationStateRecord, error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	states := make([]db.DockerReconciliationStateRecord, 0, query.Limit)
	_, err := d.Sql().Select(&states, d.PrepareQuery("select "+dockerReconciliationStateColumns+" from docker_reconciliation_state where runner_id=? and runner_boot=? and latest_sequence>? order by latest_sequence asc limit ?"), owner.RunnerID, owner.RunnerBoot, query.AfterSequence, query.Limit)
	return states, err
}

func (d *SqlDb) UpdateDockerReconciliationState(state db.DockerReconciliationStateRecord, expectedRevision int) (updated db.DockerReconciliationStateRecord, err error) {
	if expectedRevision <= 0 || state.Revision != int64(expectedRevision) {
		return updated, db.ErrDockerReconciliationRevisionConflict
	}
	if err = state.Validate(); err != nil {
		return updated, err
	}
	result, err := d.exec("update docker_reconciliation_state set revision=?, state=?, reason=?, quarantine_status=?, remediation=?, remediation_reason=?, quarantined_at=?, remediated_at=?, updated_at=? where runner_id=? and runner_boot=? and project_id=? and task_id=? and generation=? and resource=? and revision=?", state.Revision+1, state.State, state.Reason, state.QuarantineStatus, state.Remediation, state.RemediationReason, state.QuarantinedAt, state.RemediatedAt, time.Now().UTC(), state.RunnerID, state.RunnerBoot, state.ProjectID, state.TaskID, state.Generation, state.Resource, expectedRevision)
	if err != nil {
		return updated, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return updated, db.ErrDockerReconciliationRevisionConflict
	}
	return d.GetDockerReconciliationState(state.Key())
}

func (d *SqlDb) validateDockerReconciliationAttempt(tx *gorp.Transaction, observation db.DockerReconciliationObservation) error {
	count, err := tx.SelectInt(d.PrepareQuery("select count(1) from task__runner_attempt where project_id=? and task_id=? and generation=? and runner_id=? and executor_type=?"), observation.ProjectID, observation.TaskID, observation.Generation, observation.RunnerID, db.RunnerExecutorDocker)
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("Docker reconciliation task ownership is invalid")
	}
	return nil
}

func (d *SqlDb) advanceDockerReconciliationState(tx *gorp.Transaction, observation db.DockerReconciliationObservation, serverNow time.Time, state *db.DockerReconciliationStateRecord) error {
	key := observation.Key()
	err := d.selectDockerReconciliationStateTx(tx, key, state)
	if errors.Is(err, sql.ErrNoRows) {
		state.RunnerID, state.RunnerBoot = key.RunnerID, key.RunnerBoot
		state.ProjectID, state.TaskID, state.Generation, state.Resource = key.ProjectID, key.TaskID, key.Generation, key.Resource
		state.Revision = 1
		state.LatestSequence = observation.Sequence
		state.ContainerID, state.ContainerName, state.State, state.Reason, state.UpdatedAt = observation.ContainerID, observation.ContainerName, observation.State, observation.Reason, serverNow
		state.QuarantineStatus, state.Remediation, state.RemediationReason, state.QuarantinedAt, state.RemediatedAt = observation.QuarantineStatus, observation.Remediation, observation.RemediationReason, observation.QuarantinedAt, observation.RemediatedAt
		_, err = tx.Exec(d.PrepareQuery("insert into docker_reconciliation_state (runner_id,runner_boot,project_id,task_id,generation,resource,revision,latest_sequence,container_id,container_name,state,reason,updated_at,quarantine_status,remediation,remediation_reason,quarantined_at,remediated_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"), state.RunnerID, state.RunnerBoot, state.ProjectID, state.TaskID, state.Generation, state.Resource, state.Revision, state.LatestSequence, state.ContainerID, state.ContainerName, state.State, state.Reason, state.UpdatedAt, state.QuarantineStatus, state.Remediation, state.RemediationReason, state.QuarantinedAt, state.RemediatedAt)
		return err
	}
	if err != nil {
		return err
	}
	result, err := tx.Exec(d.PrepareQuery("update docker_reconciliation_state set revision=revision+1, latest_sequence=?, container_id=?, container_name=?, state=?, reason=?, updated_at=?, quarantine_status=?, remediation=?, remediation_reason=?, quarantined_at=?, remediated_at=? where runner_id=? and runner_boot=? and project_id=? and task_id=? and generation=? and resource=? and revision=?"), observation.Sequence, observation.ContainerID, observation.ContainerName, observation.State, observation.Reason, serverNow, observation.QuarantineStatus, observation.Remediation, observation.RemediationReason, observation.QuarantinedAt, observation.RemediatedAt, key.RunnerID, key.RunnerBoot, key.ProjectID, key.TaskID, key.Generation, key.Resource, state.Revision)
	if err != nil {
		return err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		return db.ErrDockerReconciliationRevisionConflict
	}
	state.Revision++
	state.LatestSequence = observation.Sequence
	state.ContainerID, state.ContainerName, state.State, state.Reason, state.UpdatedAt = observation.ContainerID, observation.ContainerName, observation.State, observation.Reason, serverNow
	state.QuarantineStatus, state.Remediation, state.RemediationReason, state.QuarantinedAt, state.RemediatedAt = observation.QuarantineStatus, observation.Remediation, observation.RemediationReason, observation.QuarantinedAt, observation.RemediatedAt
	return nil
}

func (d *SqlDb) selectDockerReconciliationStateTx(tx *gorp.Transaction, key db.DockerReconciliationKey, state *db.DockerReconciliationStateRecord) error {
	query := "select " + dockerReconciliationStateColumns + " from docker_reconciliation_state where runner_id=? and runner_boot=? and project_id=? and task_id=? and generation=? and resource=?"
	if d.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	return tx.SelectOne(state, d.PrepareQuery(query), key.RunnerID, key.RunnerBoot, key.ProjectID, key.TaskID, key.Generation, key.Resource)
}

func (d *SqlDb) ensureDockerReconciliationOwner(tx *gorp.Transaction, owner db.DockerReconciliationOwner) error {
	query := "insert into docker_reconciliation_runner (runner_id,runner_boot,last_sequence) values (?,?,0) on conflict(runner_id,runner_boot) do nothing"
	if d.GetDialect() == util.DbDriverMySQL {
		query = "insert into docker_reconciliation_runner (runner_id,runner_boot,last_sequence) values (?,?,0) on duplicate key update last_sequence=last_sequence"
	}
	_, err := tx.Exec(d.PrepareQuery(query), owner.RunnerID, owner.RunnerBoot)
	return err
}

func (d *SqlDb) lockDockerReconciliationOwner(tx *gorp.Transaction, owner db.DockerReconciliationOwner) (int64, error) {
	if d.GetDialect() == util.DbDriverSQLite {
		if _, err := tx.Exec(d.PrepareQuery("update docker_reconciliation_runner set last_sequence=last_sequence where runner_id=? and runner_boot=?"), owner.RunnerID, owner.RunnerBoot); err != nil {
			return 0, err
		}
	}
	query := "select last_sequence from docker_reconciliation_runner where runner_id=? and runner_boot=?"
	if d.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var lastSequence int64
	if err := tx.SelectOne(&lastSequence, d.PrepareQuery(query), owner.RunnerID, owner.RunnerBoot); err != nil {
		return 0, err
	}
	return lastSequence, nil
}

func sameDockerReconciliationPayload(left db.DockerReconciliationObservation, right db.DockerReconciliationObservation) bool {
	return left.Sequence == right.Sequence && left.RunnerID == right.RunnerID && left.RunnerBoot == right.RunnerBoot && left.TaskID == right.TaskID && left.ProjectID == right.ProjectID && left.Generation == right.Generation && left.Resource == right.Resource && left.ContainerID == right.ContainerID && left.ContainerName == right.ContainerName && left.State == right.State && left.Reason == right.Reason && left.ObservedAt.Equal(right.ObservedAt) && left.QuarantineStatus == right.QuarantineStatus && left.Remediation == right.Remediation && left.RemediationReason == right.RemediationReason && sameTimePointer(left.QuarantinedAt, right.QuarantinedAt) && sameTimePointer(left.RemediatedAt, right.RemediatedAt)
}

func sameTimePointer(left *time.Time, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
