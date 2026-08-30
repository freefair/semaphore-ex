package sql

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/semaphoreui/semaphore/db"
)

const dockerExecutionPolicySingletonID = 1

type dockerExecutionPolicyRow struct {
	Revision   int    `db:"revision"`
	PolicyHash string `db:"policy_hash"`
	PolicyJSON string `db:"policy_json"`
}

func (d *SqlDb) GetDockerExecutionPolicy() (db.DockerExecutionPolicy, error) {
	var row dockerExecutionPolicyRow
	err := d.selectOne(&row,
		"select revision, policy_hash, policy_json from docker_execution_policy where singleton_id=?",
		dockerExecutionPolicySingletonID,
	)
	if errors.Is(err, db.ErrNotFound) {
		return db.DefaultDockerExecutionPolicy(), nil
	}
	if err != nil {
		return db.DockerExecutionPolicy{}, err
	}
	var policy db.DockerExecutionPolicy
	if err := json.Unmarshal([]byte(row.PolicyJSON), &policy); err != nil {
		return db.DockerExecutionPolicy{}, fmt.Errorf("decoding Docker execution policy: %w", err)
	}
	policy.Revision = row.Revision
	if err := policy.Canonicalize(); err != nil {
		return db.DockerExecutionPolicy{}, fmt.Errorf("validating stored Docker execution policy: %w", err)
	}
	if policy.Hash != row.PolicyHash {
		return db.DockerExecutionPolicy{}, errors.New("stored Docker execution policy hash mismatch")
	}
	return policy, nil
}

func (d *SqlDb) SaveDockerExecutionPolicy(policy db.DockerExecutionPolicy, expectedRevision int) (db.DockerExecutionPolicy, error) {
	if expectedRevision < 0 {
		return db.DockerExecutionPolicy{}, db.ErrDockerExecutionPolicyRevisionConflict
	}
	policy.Revision = expectedRevision + 1
	if err := policy.Canonicalize(); err != nil {
		return db.DockerExecutionPolicy{}, err
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return db.DockerExecutionPolicy{}, err
	}
	if expectedRevision == 0 {
		result, err := d.exec(
			"insert into docker_execution_policy (singleton_id, revision, policy_hash, policy_json) values (?, ?, ?, ?)",
			dockerExecutionPolicySingletonID, policy.Revision, policy.Hash, string(encoded),
		)
		if err != nil {
			return db.DockerExecutionPolicy{}, db.ErrDockerExecutionPolicyRevisionConflict
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
			return db.DockerExecutionPolicy{}, db.ErrDockerExecutionPolicyRevisionConflict
		}
		return policy, nil
	}
	result, err := d.exec(
		"update docker_execution_policy set revision=?, policy_hash=?, policy_json=? where singleton_id=? and revision=?",
		policy.Revision, policy.Hash, string(encoded), dockerExecutionPolicySingletonID, expectedRevision,
	)
	if err != nil {
		return db.DockerExecutionPolicy{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return db.DockerExecutionPolicy{}, db.ErrDockerExecutionPolicyRevisionConflict
	}
	return policy, nil
}
