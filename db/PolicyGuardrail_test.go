package db

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPolicyGuardrailScopeRequiresExactlyOneProjectBoundary(t *testing.T) {
	projectID := 7
	require.NoError(t, ValidatePolicyGuardrailScope(PolicyGuardrailScopeGlobal, nil))
	require.NoError(t, ValidatePolicyGuardrailScope(PolicyGuardrailScopeProject, &projectID))
	require.Error(t, ValidatePolicyGuardrailScope(PolicyGuardrailScopeGlobal, &projectID))
	require.Error(t, ValidatePolicyGuardrailScope(PolicyGuardrailScopeProject, nil))
	require.Error(t, ValidatePolicyGuardrailScope("tenant", &projectID))
}

func TestPolicyGuardrailRevisionIsImmutableBoundedAndInternallyConsistent(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	revision := PolicyGuardrailRevision{
		ID: 1, ScopeKey: "global", Scope: PolicyGuardrailScopeGlobal, Revision: 1,
		SourceYAML: "version: 1\nrules: []\n", CompiledJSON: `{"version":1,"rules":[]}`,
		Fingerprint:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CompilerVersion: 1, PublishedBy: 4, Created: now,
	}
	require.NoError(t, revision.Validate())

	revision.ProjectID = policyIntPointer(7)
	require.Error(t, revision.Validate())
	rollback := revision
	rollback.ScopeKey = "project:7"
	rollback.Scope = PolicyGuardrailScopeProject
	rollback.Revision = 2
	rollback.ParentRevision = policyIntPointer(1)
	rollback.RollbackOfRevision = policyIntPointer(1)
	rollback.RollbackReason = "Restore the last approved rule set."
	require.NoError(t, rollback.Validate())
}

func policyIntPointer(value int) *int { return &value }
