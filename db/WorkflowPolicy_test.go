package db

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWorkflowAccessPolicyValidationAcceptsOnlyStableDistinctReferences(t *testing.T) {
	assert.NoError(t, (WorkflowAccessPolicy{
		Revision:     1,
		ViewRoleIDs:  []ProjectRoleReference{BuiltinProjectRoleReferenceOwner},
		StartRoleIDs: []ProjectRoleReference{ProjectRoleReferenceForCustomRole("role_deployer")},
	}).Validate())
	assert.Error(t, (WorkflowAccessPolicy{
		ViewRoleIDs: []ProjectRoleReference{BuiltinProjectRoleReferenceOwner, BuiltinProjectRoleReferenceOwner},
	}).Validate())
	assert.Error(t, (WorkflowAccessPolicy{
		ViewRoleIDs: []ProjectRoleReference{"owner"},
	}).Validate())
}

func TestWorkflowApprovalContributionRequiresBoundedImmutableEvidence(t *testing.T) {
	contribution := WorkflowApprovalContribution{
		ApprovalID: 1, ActorUserID: 2, RoleID: BuiltinProjectRoleReferenceOwner,
		RoleRevision: 1, RoleOrigin: WorkflowApprovalRoleOriginBuiltIn,
		Decision: WorkflowApprovalApproved, Created: time.Now().UTC(),
		PolicyRevision: 1, CorrelationID: "approval-correlation",
	}
	assert.NoError(t, contribution.Validate())

	contribution.RoleRevision = 0
	assert.Error(t, contribution.Validate())
	contribution.RoleRevision = 1
	contribution.Decision = WorkflowApprovalExpired
	assert.Error(t, contribution.Validate())
	contribution.Decision = WorkflowApprovalApproved
	contribution.RoleOrigin = WorkflowApprovalRoleOriginLDAP
	contribution.DirectoryProviderID = "corp"
	contribution.DirectoryMappingID = "approvers"
	contribution.DirectoryMappingRevision = 1
	contribution.DirectoryRevisionFingerprint = "f0e1d2c3f0e1d2c3f0e1d2c3f0e1d2c3f0e1d2c3f0e1d2c3f0e1d2c3f0e1d2c3"
	assert.NoError(t, contribution.Validate())
	contribution.DirectoryRevisionFingerprint = "F0E1D2C3F0E1D2C3F0E1D2C3F0E1D2C3F0E1D2C3F0E1D2C3F0E1D2C3F0E1D2C3"
	assert.Error(t, contribution.Validate())
	contribution.DirectoryRevisionFingerprint = string(make([]byte, MaxWorkflowApprovalDirectoryRevisionFingerprintBytes+1))
	assert.Error(t, contribution.Validate())
}

func TestWorkflowApprovalRolePolicyRequiresMeaningfulDistinctApprovalRules(t *testing.T) {
	valid := WorkflowApprovalRolePolicy{
		Revision: 1, Mode: WorkflowApprovalRoleModeAllOf,
		RoleIDs:                  []ProjectRoleReference{BuiltinProjectRoleReferenceOwner, BuiltinProjectRoleReferenceManager},
		MinimumDistinctApprovers: 2,
	}
	assert.NoError(t, valid.Validate())

	valid.MinimumDistinctApprovers = 1
	assert.Error(t, valid.Validate(), "all-of cannot be completed by fewer actors than required roles")
	valid.Mode = WorkflowApprovalRoleModeAnyOf
	valid.MinimumDistinctApprovers = 0
	assert.Error(t, valid.Validate())
	valid.MinimumDistinctApprovers = 1
	valid.RoleIDs = nil
	assert.Error(t, valid.Validate())
}
