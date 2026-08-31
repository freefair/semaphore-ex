package sql

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/semaphoreui/semaphore/db"
)

type kubernetesExecutionPolicyRow struct {
	ClusterAlias string `db:"cluster_alias"`
	Revision     int    `db:"revision"`
	PolicyHash   string `db:"policy_hash"`
	PolicyJSON   string `db:"policy_json"`
}

func (d *SqlDb) GetKubernetesExecutionPolicy(clusterAlias string) (db.KubernetesExecutionPolicy, error) {
	if err := db.ValidateKubernetesClusterAlias(clusterAlias); err != nil {
		return db.KubernetesExecutionPolicy{}, err
	}
	var row kubernetesExecutionPolicyRow
	err := d.selectOne(&row, "select cluster_alias, revision, policy_hash, policy_json from kubernetes_execution_policy where cluster_alias=?", clusterAlias)
	if errors.Is(err, db.ErrNotFound) {
		return db.DefaultKubernetesExecutionPolicy(clusterAlias), nil
	}
	if err != nil {
		return db.KubernetesExecutionPolicy{}, err
	}
	var policy db.KubernetesExecutionPolicy
	if err := json.Unmarshal([]byte(row.PolicyJSON), &policy); err != nil {
		return db.KubernetesExecutionPolicy{}, fmt.Errorf("decoding Kubernetes execution policy: %w", err)
	}
	if policy.ClusterAlias != row.ClusterAlias {
		return db.KubernetesExecutionPolicy{}, errors.New("stored Kubernetes execution policy alias mismatch")
	}
	policy.Revision = row.Revision
	if err := policy.Canonicalize(); err != nil {
		return db.KubernetesExecutionPolicy{}, fmt.Errorf("validating stored Kubernetes execution policy: %w", err)
	}
	if policy.Hash != row.PolicyHash {
		return db.KubernetesExecutionPolicy{}, errors.New("stored Kubernetes execution policy hash mismatch")
	}
	return policy, nil
}

func (d *SqlDb) SaveKubernetesExecutionPolicy(policy db.KubernetesExecutionPolicy, expectedRevision int) (db.KubernetesExecutionPolicy, error) {
	if expectedRevision < 0 || db.ValidateKubernetesClusterAlias(policy.ClusterAlias) != nil {
		return db.KubernetesExecutionPolicy{}, db.ErrKubernetesExecutionPolicyRevisionConflict
	}
	policy.Revision = expectedRevision + 1
	if err := policy.Canonicalize(); err != nil {
		return db.KubernetesExecutionPolicy{}, err
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return db.KubernetesExecutionPolicy{}, err
	}
	if expectedRevision == 0 {
		result, err := d.exec("insert into kubernetes_execution_policy (cluster_alias, revision, policy_hash, policy_json) values (?, ?, ?, ?)", policy.ClusterAlias, policy.Revision, policy.Hash, string(encoded))
		if err != nil {
			return db.KubernetesExecutionPolicy{}, db.ErrKubernetesExecutionPolicyRevisionConflict
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
			return db.KubernetesExecutionPolicy{}, db.ErrKubernetesExecutionPolicyRevisionConflict
		}
		return policy, nil
	}
	result, err := d.exec("update kubernetes_execution_policy set revision=?, policy_hash=?, policy_json=? where cluster_alias=? and revision=?", policy.Revision, policy.Hash, string(encoded), policy.ClusterAlias, expectedRevision)
	if err != nil {
		return db.KubernetesExecutionPolicy{}, err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		return db.KubernetesExecutionPolicy{}, db.ErrKubernetesExecutionPolicyRevisionConflict
	}
	return policy, nil
}
