package pro_interfaces

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditEventAcceptsOnlyAllowlistedContext(t *testing.T) {
	actorID := 7
	event := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		ActorID:       &actorID,
		Action:        AuditActionCapabilityWrite,
		TargetType:    AuditTargetCapability,
		TargetID:      string(CapabilityLifecycleTest),
		Outcome:       AuditOutcomeDenied,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonDisabledByAdmin),
	}

	require.NoError(t, event.Validate())
	assert.Equal(t, map[string]any{
		"correlation_id": event.CorrelationID,
		"actor_id":       7,
		"action":         AuditActionCapabilityWrite,
		"target_type":    AuditTargetCapability,
		"target_id":      string(CapabilityLifecycleTest),
		"outcome":        AuditOutcomeDenied,
		"source":         AuditSourceAPI,
		"reason":         string(CapabilityReasonDisabledByAdmin),
	}, event.SafeFields())
}

func TestAuditWebhookEnvelopeUsesVersionedAllowlist(t *testing.T) {
	actorID := 7
	projectID := 42
	event := AuditEvent{
		EventID:       "0123456789abcdef0123456789abcdef",
		OccurredAt:    time.Date(2026, time.August, 27, 10, 11, 12, 0, time.UTC),
		CorrelationID: "abcdef0123456789abcdef0123456789",
		ActorID:       &actorID,
		ProjectID:     &projectID,
		Action:        AuditActionProjectRunnerUpdate,
		TargetType:    AuditTargetProjectRunner,
		TargetID:      "runner:42",
		Outcome:       AuditOutcomeAllowed,
		Source:        AuditSourceAPI,
		SourceIP:      "192.0.2.10",
		UserAgent:     "audit-client/1.0",
		Reason:        string(CapabilityReasonActive),
	}

	envelope, err := NewAuditWebhookEnvelope(event)
	require.NoError(t, err)
	payload, err := json.Marshal(envelope)
	require.NoError(t, err)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(payload, &fields))
	assert.Equal(t, AuditWebhookSchemaVersion, fields["schema_version"])
	assert.Equal(t, event.EventID, fields["event_id"])
	assert.Equal(t, event.CorrelationID, fields["correlation_id"])
	assert.Equal(t, map[string]any{"id": float64(actorID)}, fields["actor"])
	assert.Equal(t, map[string]any{
		"id":         event.TargetID,
		"project_id": float64(projectID),
		"type":       string(event.TargetType),
	}, fields["target"])
	for _, forbidden := range []string{"credential", "token", "task_args", "request_body", "description"} {
		assert.NotContains(t, fields, forbidden)
	}
	securityfixtures.AssertTripwiresAbsent(t, string(payload))
}

func TestAuditDeliveryMetadataPreservesStableIdentity(t *testing.T) {
	now := time.Date(2026, time.August, 27, 10, 11, 12, 0, time.UTC)
	event, err := validAuditEventWithoutDeliveryMetadata().EnsureDeliveryMetadata(now)
	require.NoError(t, err)
	assert.Regexp(t, eventIDPattern, event.EventID)
	assert.Equal(t, now, event.OccurredAt)

	later := now.Add(time.Hour)
	retry, err := event.EnsureDeliveryMetadata(later)
	require.NoError(t, err)
	assert.Equal(t, event.EventID, retry.EventID)
	assert.Equal(t, event.OccurredAt, retry.OccurredAt)
}

func TestAuditRequestMetadataIsBoundedAndValidated(t *testing.T) {
	assert.Equal(t, "192.0.2.10", NormalizeAuditSourceIP("192.0.2.10:4242"))
	assert.Equal(t, "2001:db8::1", NormalizeAuditSourceIP("[2001:db8::1]:4242"))
	assert.Empty(t, NormalizeAuditSourceIP("not-an-address"))

	userAgent := SanitizeAuditUserAgent(" agent\nsecret " + string(make([]byte, 300)))
	assert.Equal(t, "agentsecret", userAgent)
	assert.LessOrEqual(t, len(userAgent), AuditUserAgentMaxLength)
	unicodeUserAgent := SanitizeAuditUserAgent(strings.Repeat("ä", AuditUserAgentMaxLength))
	assert.True(t, utf8.ValidString(unicodeUserAgent))
	assert.Equal(t, AuditUserAgentMaxLength, len(unicodeUserAgent))

	event := validAuditEventWithoutDeliveryMetadata()
	event.SourceIP = "not-an-address"
	assert.Error(t, event.Validate())
	event.SourceIP = "192.0.2.10"
	event.UserAgent = "agent\n"
	assert.Error(t, event.Validate())
}

func validAuditEventWithoutDeliveryMetadata() AuditEvent {
	return AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		Action:        AuditActionCapabilityWrite,
		TargetType:    AuditTargetCapability,
		TargetID:      string(CapabilityLifecycleTest),
		Outcome:       AuditOutcomeDenied,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonDisabledByAdmin),
	}
}

func TestAuditEventRejectsTripwiresInEveryStringSlot(t *testing.T) {
	base := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		Action:        AuditActionCapabilityWrite,
		TargetType:    AuditTargetCapability,
		TargetID:      string(CapabilityLifecycleTest),
		Outcome:       AuditOutcomeDenied,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonDisabledByAdmin),
	}

	for _, tripwire := range securityfixtures.TripwireValues {
		tests := []AuditEvent{
			withAuditCorrelation(base, tripwire),
			withAuditTarget(base, tripwire),
			withAuditReason(base, tripwire),
		}
		for _, event := range tests {
			assert.Error(t, event.Validate())
			fields, err := json.Marshal(event.SafeFields())
			require.NoError(t, err)
			securityfixtures.AssertTripwiresAbsent(t, string(fields))
		}
	}
}

func TestAuditEventAcceptsBoundedProjectRunnerTarget(t *testing.T) {
	projectID := 42
	event := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		ProjectID:     &projectID,
		Action:        AuditActionProjectRunnerCreate,
		TargetType:    AuditTargetProjectRunner,
		TargetID:      "runner:42",
		Outcome:       AuditOutcomeAllowed,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonActive),
	}

	require.NoError(t, event.Validate())
	event.TargetID = "runner:" + securityfixtures.TripwireValues[0]
	assert.Error(t, event.Validate())
}

func TestAuditEventAcceptsScopedProjectRoleAndMembershipTargets(t *testing.T) {
	projectID := 42
	events := []AuditEvent{
		{
			CorrelationID: "0123456789abcdef0123456789abcdef",
			ProjectID:     &projectID,
			Action:        AuditActionProjectRoleUpdate,
			TargetType:    AuditTargetProjectRole,
			TargetID:      "role:role_0123456789abcdef0123456789abcdef",
			Outcome:       AuditOutcomeAllowed,
			Source:        AuditSourceAPI,
			Reason:        string(CapabilityReasonActive),
		},
		{
			CorrelationID: "fedcba9876543210fedcba9876543210",
			ProjectID:     &projectID,
			Action:        AuditActionProjectRoleAssign,
			TargetType:    AuditTargetProjectMembership,
			TargetID:      "member:7",
			Outcome:       AuditOutcomeAllowed,
			Source:        AuditSourceAPI,
			Reason:        string(CapabilityReasonActive),
		},
	}
	for _, event := range events {
		require.NoError(t, event.Validate())
	}
}

func TestAuditEventAcceptsImmutableLDAPGroupTargets(t *testing.T) {
	event := validAuditEventWithoutDeliveryMetadata()
	event.Action = AuditActionLDAPGroupMappingWrite
	event.TargetType = AuditTargetLDAPGroupMapping
	event.TargetID = "entryuuid:40f1c82a-b773-4d41-a587-7c4cf7f3cd67"

	assert.NoError(t, event.Validate())
	event.TargetID = "cn=engineering,ou=groups,dc=example,dc=test"
	assert.Error(t, event.Validate())
}

func TestAuditEventRequiresConsistentProjectScope(t *testing.T) {
	projectID := 42
	otherProjectID := 43
	projectRunner := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		ProjectID:     &projectID,
		Action:        AuditActionProjectRunnerCreate,
		TargetType:    AuditTargetProjectRunner,
		TargetID:      "project:42",
		Outcome:       AuditOutcomeAllowed,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonActive),
	}

	require.NoError(t, projectRunner.Validate())
	assert.Equal(t, 42, projectRunner.SafeFields()["project_id"])

	missingScope := projectRunner
	missingScope.ProjectID = nil
	assert.Error(t, missingScope.Validate())
	missingScope.Outcome = AuditOutcomeDenied
	missingScope.Reason = AuditReasonUnauthenticated
	require.NoError(t, missingScope.Validate())
	missingScope.Reason = AuditReasonCrossOrigin
	require.NoError(t, missingScope.Validate())
	missingScope.Outcome = AuditOutcomeFailure
	assert.Error(t, missingScope.Validate())

	invalidScope := projectRunner
	invalidProjectID := 0
	invalidScope.ProjectID = &invalidProjectID
	assert.Error(t, invalidScope.Validate())

	mismatchedScope := projectRunner
	mismatchedScope.ProjectID = &otherProjectID
	assert.Error(t, mismatchedScope.Validate())

	globalCapability := AuditEvent{
		CorrelationID: "fedcba9876543210fedcba9876543210",
		Action:        AuditActionCapabilityRead,
		TargetType:    AuditTargetCapability,
		TargetID:      string(CapabilityLifecycleTest),
		Outcome:       AuditOutcomeAllowed,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonActive),
	}
	require.NoError(t, globalCapability.Validate())
	globalCapability.ProjectID = &projectID
	assert.Error(t, globalCapability.Validate())
}

func TestAuditEventPersistsBoundedWorkflowApprovalProvenance(t *testing.T) {
	projectID := 42
	event := AuditEvent{
		CorrelationID:          "0123456789abcdef0123456789abcdef",
		ProjectID:              &projectID,
		Action:                 AuditActionWorkflowApprovalContribute,
		TargetType:             AuditTargetWorkflowApproval,
		TargetID:               "approval:17",
		Outcome:                AuditOutcomeAllowed,
		Source:                 AuditSourceAPI,
		Reason:                 AuditReasonWorkflowApprovalApproved,
		WorkflowPolicyRevision: 3,
		RoleProvenance: []AuditRoleProvenance{{
			RoleID:                       "role:release_manager",
			RoleRevision:                 7,
			Origin:                       AuditRoleOriginOIDC,
			DirectoryProviderID:          "corp",
			DirectoryMappingID:           "release-approvers",
			DirectoryMappingRevision:     4,
			DirectoryRevisionFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		}},
	}

	require.NoError(t, event.Validate())
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"occurred_at":"0001-01-01T00:00:00Z",
		"correlation_id":"0123456789abcdef0123456789abcdef",
		"project_id":42,
		"action":"workflow_approval_contribute",
		"target_type":"workflow_approval",
		"target_id":"approval:17",
		"outcome":"allowed",
		"source":"api",
		"reason":"workflow_approval_approved",
		"workflow_policy_revision":3,
		"role_provenance":[{
			"role_id":"role:release_manager",
			"role_revision":7,
			"origin":"oidc",
			"directory_provider_id":"corp",
			"directory_mapping_id":"release-approvers",
			"directory_mapping_revision":4,
			"directory_revision_fingerprint":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		}]
	}`, string(payload))
	assert.Equal(t, event.RoleProvenance, event.SafeFields()["role_provenance"])
}

func TestAuditEventRejectsUnboundedOrInconsistentWorkflowProvenance(t *testing.T) {
	projectID := 42
	base := AuditEvent{
		CorrelationID:          "0123456789abcdef0123456789abcdef",
		ProjectID:              &projectID,
		Action:                 AuditActionWorkflowApprovalContribute,
		TargetType:             AuditTargetWorkflowApproval,
		TargetID:               "approval:17",
		Outcome:                AuditOutcomeAllowed,
		Source:                 AuditSourceAPI,
		Reason:                 AuditReasonWorkflowApprovalApproved,
		WorkflowPolicyRevision: 1,
		RoleProvenance: []AuditRoleProvenance{{
			RoleID:       "builtin:manager",
			RoleRevision: 1,
			Origin:       AuditRoleOriginBuiltin,
		}},
	}

	require.NoError(t, base.Validate())
	missingProvenance := base
	missingProvenance.RoleProvenance = nil
	assert.Error(t, missingProvenance.Validate())

	withDirectoryClaims := base
	withDirectoryClaims.RoleProvenance = append([]AuditRoleProvenance(nil), base.RoleProvenance...)
	withDirectoryClaims.RoleProvenance[0].DirectoryProviderID = "corp"
	assert.Error(t, withDirectoryClaims.Validate())

	withRawDirectoryRevision := base
	withRawDirectoryRevision.RoleProvenance = append([]AuditRoleProvenance(nil), base.RoleProvenance...)
	withRawDirectoryRevision.RoleProvenance[0] = AuditRoleProvenance{
		RoleID:                       "role:release_manager",
		RoleRevision:                 1,
		Origin:                       AuditRoleOriginOIDC,
		DirectoryProviderID:          "corp",
		DirectoryMappingID:           "release-approvers",
		DirectoryMappingRevision:     1,
		DirectoryRevisionFingerprint: "release-approvers",
	}
	assert.Error(t, withRawDirectoryRevision.Validate())

	wrongTarget := base
	wrongTarget.TargetType = AuditTargetWorkflowRun
	wrongTarget.TargetID = "run:17"
	assert.Error(t, wrongTarget.Validate())

	genericReason := base
	genericReason.Reason = string(CapabilityReasonActive)
	assert.Error(t, genericReason.Validate())

	tooMany := base
	tooMany.RoleProvenance = make([]AuditRoleProvenance, AuditRoleProvenanceMaxEntries+1)
	for index := range tooMany.RoleProvenance {
		tooMany.RoleProvenance[index] = base.RoleProvenance[0]
	}
	assert.Error(t, tooMany.Validate())
}

func TestAuditEventBindsCrossProjectTemplateProvenanceToActionScopeAndTarget(t *testing.T) {
	ownerProjectID := 11
	consumerProjectID := 12
	grantEvent := func(action AuditAction, projectID int) AuditEvent {
		return AuditEvent{
			CorrelationID: "0123456789abcdef0123456789abcdef",
			ProjectID:     &projectID,
			Action:        action,
			TargetType:    AuditTargetCrossProjectTemplateGrant,
			TargetID:      "grant:41",
			Outcome:       AuditOutcomeAllowed,
			Source:        AuditSourceAPI,
			Reason:        AuditReasonCrossProjectTemplateGrantActive,
			CrossProjectTemplateProvenance: &AuditCrossProjectTemplateProvenance{
				OwnerProjectID: ownerProjectID, ConsumerProjectID: consumerProjectID, TemplateID: 21,
				GrantID: 41, GrantRevision: 3, Operation: int(db.CrossProjectTemplateGrantReference),
				MinTemplateVersion: 1, MaxTemplateVersion: 2,
			},
		}
	}

	for _, test := range []struct {
		name    string
		action  AuditAction
		project int
	}{
		{"create", AuditActionCrossProjectTemplateGrantCreate, ownerProjectID},
		{"update", AuditActionCrossProjectTemplateGrantUpdate, ownerProjectID},
		{"delete", AuditActionCrossProjectTemplateGrantDelete, ownerProjectID},
		{"accept", AuditActionCrossProjectTemplateGrantAccept, consumerProjectID},
		{"revoke owner", AuditActionCrossProjectTemplateGrantRevoke, ownerProjectID},
		{"revoke consumer", AuditActionCrossProjectTemplateGrantRevoke, consumerProjectID},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, grantEvent(test.action, test.project).Validate())
		})
	}

	reference := grantEvent(AuditActionCrossProjectTemplateReferenceResolve, consumerProjectID)
	reference.CrossProjectTemplateProvenance.TemplateVersionID = 31
	reference.CrossProjectTemplateProvenance.TemplateVersionNumber = 2
	reference.CrossProjectTemplateProvenance.TemplateVersionFingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	require.NoError(t, reference.Validate())

	publish := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		ProjectID:     &ownerProjectID,
		Action:        AuditActionCrossProjectTemplateVersionPublish,
		TargetType:    AuditTargetCrossProjectTemplateVersion,
		TargetID:      "template-version:31",
		Outcome:       AuditOutcomeAllowed,
		Source:        AuditSourceAPI,
		Reason:        AuditReasonCrossProjectTemplateGrantActive,
		CrossProjectTemplateProvenance: &AuditCrossProjectTemplateProvenance{
			OwnerProjectID: ownerProjectID, TemplateID: 21, TemplateVersionID: 31, TemplateVersionNumber: 2,
			TemplateVersionFingerprint: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	}
	require.NoError(t, publish.Validate())

	wrongProject := grantEvent(AuditActionCrossProjectTemplateGrantAccept, ownerProjectID)
	assert.Error(t, wrongProject.Validate())
	wrongTarget := grantEvent(AuditActionCrossProjectTemplateGrantCreate, ownerProjectID)
	wrongTarget.TargetID = "grant:42"
	assert.Error(t, wrongTarget.Validate())
	wrongOperation := reference
	wrongOperation.CrossProjectTemplateProvenance.Operation = int(db.CrossProjectTemplateGrantRun)
	assert.Error(t, wrongOperation.Validate())
}

func TestAuditWebhookV1OmitsWorkflowPolicyAndRoleProvenance(t *testing.T) {
	projectID := 42
	event := AuditEvent{
		EventID:                "0123456789abcdef0123456789abcdef",
		OccurredAt:             time.Date(2026, time.August, 31, 10, 11, 12, 0, time.UTC),
		CorrelationID:          "abcdef0123456789abcdef0123456789",
		ProjectID:              &projectID,
		Action:                 AuditActionWorkflowRead,
		TargetType:             AuditTargetWorkflow,
		TargetID:               "workflow:17",
		Outcome:                AuditOutcomeDenied,
		Source:                 AuditSourceAPI,
		Reason:                 AuditReasonWorkflowPolicyDenied,
		WorkflowPolicyRevision: 3,
	}

	envelope, err := NewAuditWebhookEnvelope(event)
	require.NoError(t, err)
	payload, err := json.Marshal(envelope)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "workflow_policy_revision")
	assert.NotContains(t, string(payload), "role_provenance")
}

func TestExecutionPreflightAuditUsesStrictValueFreeAllowlist(t *testing.T) {
	actorID, projectID := 7, 42
	selectedRunnerID := 11
	plan := ExecutionPreflightPlan{
		Intent:      ExecutionPreflightTask,
		Fingerprint: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Placements: []ExecutionPreflightPlacement{{
			Provisional: true, Decision: ExecutionReasonSelected,
			Candidates:       []ExecutionPreflightCandidate{{RunnerID: 11}},
			SelectedRunnerID: &selectedRunnerID,
		}},
		Findings: []ExecutionPreflightFinding{{Code: ExecutionReasonPolicyDenied}},
	}
	event := NewExecutionPreflightAuditEvent(
		actorID, projectID, "0123456789abcdef0123456789abcdef", "192.0.2.10:443", "audit-client",
		AuditActionExecutionPreflightPreview, AuditOutcomeAllowed, AuditReasonExecutionPreflightPreviewed,
		ExecutionPreflightTask, 9, NewExecutionPreflightAuditProvenance(plan, nil),
	)
	require.NoError(t, event.Validate())
	assert.Equal(t, "task-template:9", event.TargetID)
	assert.Equal(t, 1, event.ExecutionPreflightProvenance.CandidateCount)
	assert.Equal(t, 11, event.ExecutionPreflightProvenance.SelectedRunnerID)
	assert.Contains(t, event.SafeFields(), "execution_preflight_provenance")

	unsafe := event
	unsafe.ExecutionPreflightProvenance.Fingerprint = "sha256:not-a-canonical-fingerprint"
	assert.Error(t, unsafe.Validate())
	unsafe = event
	unsafe.TargetID = "workflow:9"
	assert.Error(t, unsafe.Validate())
	unsafe = event
	unsafe.Action = AuditActionWorkflowStart
	assert.Error(t, unsafe.Validate())
	event.ExecutionPreflightProvenance.Fingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	event.EventID = "0123456789abcdef0123456789abcdef"
	event.OccurredAt = time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	envelope, err := NewAuditWebhookEnvelope(event)
	require.NoError(t, err)
	payload, err := json.Marshal(envelope)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "execution_preflight_provenance")
	assert.NotContains(t, string(payload), "review_token")
}

func TestDeploymentWindowAuditUsesStrictBoundedProvenance(t *testing.T) {
	projectID, actorID := 42, 7
	category := "incident"
	record := db.DeploymentWindowDecisionRecord{
		ID: 99, ProjectID: projectID, Source: string(DeploymentWindowSourceManual), Origin: string(DeploymentWindowOriginUser),
		PolicyRevision: 3, EffectiveTimezone: "Europe/Berlin", EvaluatedAt: time.Date(2026, time.September, 3, 10, 0, 0, 0, time.UTC),
		State: string(DeploymentWindowDecisionOverridden), Reason: string(DeploymentWindowReasonOverride),
		OverrideActorID: &actorID, OverrideCategory: &category,
		MatchedRulesJSON: `[{"id":11,"revision":4,"kind":"freeze"}]`,
	}
	event, err := NewDeploymentWindowAuditEvent(record, AuditActionDeploymentWindowAdmission, "0123456789abcdef0123456789abcdef", &actorID)
	require.NoError(t, err)
	require.NoError(t, event.Validate())
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	for _, forbidden := range []string{"decision_key", "override_reference", "actor_user_id", "rule_name", "recurrence", "effective_from", "effective_until", "headers", "credential", "SECRET_MARKER"} {
		assert.NotContains(t, string(payload), forbidden)
	}
	assert.Contains(t, string(payload), `"matched_rules"`)
	assert.Equal(t, AuditReasonDeploymentWindowOverridden, event.Reason)

	invalid := event
	invalid.DeploymentWindowProvenance.MatchedRules = append(invalid.DeploymentWindowProvenance.MatchedRules, AuditDeploymentWindowRule{ID: 11, Revision: 4, Kind: db.DeploymentWindowFreeze})
	assert.Error(t, invalid.Validate(), "matching rules must remain a unique bounded tuple set")
	invalid = event
	invalid.DeploymentWindowProvenance.OverrideCategory = "unbounded-free-text"
	assert.Error(t, invalid.Validate())
}

func TestDeploymentWindowAuditRejectsInvalidActionTargetAndForbiddenOverrideCarriesNoReference(t *testing.T) {
	projectID, actorID := 42, 7
	forbidden := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef", ActorID: &actorID, ProjectID: &projectID,
		Action: AuditActionDeploymentWindowAdmission, TargetType: AuditTargetDeploymentWindow, TargetID: "project:42",
		Outcome: AuditOutcomeDenied, Source: AuditSourceAPI, Reason: AuditReasonDeploymentWindowOverrideForbidden,
	}
	require.NoError(t, forbidden.Validate())
	payload, err := json.Marshal(forbidden)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "override_reference")
	assert.NotContains(t, string(payload), "INC-1234")
	wrong := forbidden
	wrong.TargetID = "decision:1"
	assert.Error(t, wrong.Validate())
	wrong = forbidden
	wrong.Source = AuditSourceWorker
	assert.Error(t, wrong.Validate())
}

func TestAuditWebhookV1OmitsDeploymentWindowProvenance(t *testing.T) {
	projectID, actorID := 42, 7
	record := db.DeploymentWindowDecisionRecord{
		ID: 99, ProjectID: projectID, Source: string(DeploymentWindowSourceManual), Origin: string(DeploymentWindowOriginUser),
		PolicyRevision: 3, EffectiveTimezone: "UTC", EvaluatedAt: time.Date(2026, time.September, 3, 10, 0, 0, 0, time.UTC),
		State: string(DeploymentWindowDecisionAllowed), Reason: string(DeploymentWindowReasonDefaultAllow), MatchedRulesJSON: "[]",
	}
	event, err := NewDeploymentWindowAuditEvent(record, AuditActionDeploymentWindowAdmission, "0123456789abcdef0123456789abcdef", &actorID)
	require.NoError(t, err)
	event.EventID = "0123456789abcdef0123456789abcdef"
	event.OccurredAt = time.Date(2026, time.September, 3, 10, 1, 0, 0, time.UTC)
	envelope, err := NewAuditWebhookEnvelope(event)
	require.NoError(t, err)
	payload, err := json.Marshal(envelope)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "deployment_window_provenance")
	assert.NotContains(t, string(payload), "policy_revision")
	assert.NotContains(t, string(payload), "matched_rules")
}

func TestDeploymentWindowBindingAllowsOnlyDurableTargetsOrBlockedTrigger(t *testing.T) {
	projectID, actorID := 42, 7
	record := db.DeploymentWindowDecisionRecord{
		ID: 99, ProjectID: projectID, Source: string(DeploymentWindowSourceWebhook), Origin: string(DeploymentWindowOriginWorkflowTrigger),
		PolicyRevision: 3, EffectiveTimezone: "UTC", EvaluatedAt: time.Date(2026, time.September, 3, 10, 0, 0, 0, time.UTC),
		State: string(DeploymentWindowDecisionBlocked), Reason: string(DeploymentWindowReasonFreezeActive), MatchedRulesJSON: "[]",
	}
	event, err := NewDeploymentWindowAuditEvent(record, AuditActionDeploymentWindowBinding, "internal", &actorID)
	require.NoError(t, err)
	require.NoError(t, event.Validate(), "a blocked trigger invocation has no permitted invocation-ID field")
	assert.Nil(t, event.ActorID, "automatic execution identities stay private")

	record.Origin = string(DeploymentWindowOriginIntegration)
	_, err = NewDeploymentWindowAuditEvent(record, AuditActionDeploymentWindowBinding, "internal", nil)
	require.Error(t, err, "a non-trigger binding must carry one of the permitted durable linkage IDs")

	manual := record
	manual.Source, manual.Origin = string(DeploymentWindowSourceManual), string(DeploymentWindowOriginUser)
	manual.State, manual.Reason, manual.ActorUserID = string(DeploymentWindowDecisionAllowed), string(DeploymentWindowReasonDefaultAllow), &actorID
	manual.TaskID = &actorID
	event, err = NewDeploymentWindowAuditEvent(manual, AuditActionDeploymentWindowBinding, "internal", &actorID)
	require.NoError(t, err)
	require.NoError(t, event.Validate())
}

func withAuditCorrelation(event AuditEvent, value string) AuditEvent {
	event.CorrelationID = value
	return event
}

func withAuditTarget(event AuditEvent, value string) AuditEvent {
	event.TargetID = value
	return event
}

func withAuditReason(event AuditEvent, value string) AuditEvent {
	event.Reason = value
	return event
}
