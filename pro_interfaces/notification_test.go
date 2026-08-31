package pro_interfaces_test

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationEventRetainsStableIdentityAcrossRetry(t *testing.T) {
	now := time.Date(2026, time.August, 31, 9, 0, 0, 0, time.UTC)
	event, err := (pro_interfaces.NotificationEvent{
		Scope:           pro_interfaces.NotificationScopeProject,
		ProjectID:       notificationInt(17),
		Source:          pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceTask, ID: "task:42"},
		LifecycleID:     "template:9",
		SourceRevision:  7,
		Severity:        pro_interfaces.NotificationSeverityCritical,
		LifecycleAction: pro_interfaces.NotificationLifecycleTrigger,
		Details:         pro_interfaces.NotificationDetails{TaskID: notificationInt(42), TemplateID: notificationInt(9), Status: "failed"},
	}).EnsureIdentity(now)
	require.NoError(t, err)

	retried, err := event.EnsureIdentity(now.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, event.EventID, retried.EventID)
	assert.Equal(t, event.SourceEventKey, retried.SourceEventKey)
	assert.Equal(t, event.IncidentKey, retried.IncidentKey)
	assert.Equal(t, event.OccurredAt, retried.OccurredAt)
}

func TestNotificationEventRejectsUnboundedOrCrossScopeDetails(t *testing.T) {
	valid := pro_interfaces.NotificationEvent{
		Scope:           pro_interfaces.NotificationScopeGlobal,
		Source:          pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceSystem, ID: "system:node"},
		LifecycleID:     "system:node",
		SourceRevision:  1,
		Severity:        pro_interfaces.NotificationSeverityWarning,
		LifecycleAction: pro_interfaces.NotificationLifecycleUpdate,
		Details:         pro_interfaces.NotificationDetails{Status: "degraded"},
	}
	require.NoError(t, valid.Validate())

	invalidProject := valid
	invalidProject.ProjectID = notificationInt(17)
	assert.Error(t, invalidProject.Validate())

	invalidDetails := valid
	invalidDetails.Details.Message = "arbitrary user content is not an allow-listed notification detail"
	assert.Error(t, invalidDetails.Validate())
}

func TestNotificationRoutingUsesTypedFiltersAndSeverityFloor(t *testing.T) {
	rule := pro_interfaces.NotificationRoutingRule{
		SourceKinds:     []pro_interfaces.NotificationSourceKind{pro_interfaces.NotificationSourceWorkflow},
		Actions:         []pro_interfaces.NotificationLifecycleAction{pro_interfaces.NotificationLifecycleTrigger},
		MinimumSeverity: pro_interfaces.NotificationSeverityWarning,
		Enabled:         true,
	}
	event := pro_interfaces.NotificationEvent{
		Scope:           pro_interfaces.NotificationScopeProject,
		ProjectID:       notificationInt(2),
		Source:          pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceWorkflow, ID: "workflow_run:9"},
		LifecycleID:     "workflow:5",
		SourceRevision:  1,
		Severity:        pro_interfaces.NotificationSeverityCritical,
		LifecycleAction: pro_interfaces.NotificationLifecycleTrigger,
		Details:         pro_interfaces.NotificationDetails{WorkflowID: notificationInt(5), WorkflowRunID: notificationInt(9), Status: "failed"},
	}
	assert.True(t, rule.Matches(event))
	rule.MinimumSeverity = pro_interfaces.NotificationSeverityCritical
	assert.True(t, rule.Matches(event))
	event.Severity = pro_interfaces.NotificationSeverityError
	assert.False(t, rule.Matches(event))
	rule.MinimumSeverity = pro_interfaces.NotificationSeverityError
	assert.True(t, rule.Matches(event))
	event.Severity = pro_interfaces.NotificationSeverityWarning
	assert.False(t, rule.Matches(event))
}

func TestNotificationSourceEventKeyDeduplicatesOneTransitionWithoutSplittingIncident(t *testing.T) {
	source := pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceTask, ID: "task:42"}
	projectID := notificationInt(17)
	incident, err := pro_interfaces.NotificationIncidentKey(pro_interfaces.NotificationScopeProject, projectID, "template:9")
	require.NoError(t, err)
	first, err := pro_interfaces.NotificationSourceEventKey(pro_interfaces.NotificationScopeProject, projectID, source, 4)
	require.NoError(t, err)
	replay, err := pro_interfaces.NotificationSourceEventKey(pro_interfaces.NotificationScopeProject, projectID, source, 4)
	require.NoError(t, err)
	next, err := pro_interfaces.NotificationSourceEventKey(pro_interfaces.NotificationScopeProject, projectID, source, 5)
	require.NoError(t, err)
	assert.Equal(t, first, replay)
	assert.NotEqual(t, first, next)
	assert.NotEqual(t, incident, first)
}

func TestDistinctTaskSourcesShareLifecycleIncidentButNotSourceEventKeys(t *testing.T) {
	projectID := notificationInt(17)
	firstSource := pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceTask, ID: "task:41"}
	secondSource := pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceTask, ID: "task:42"}
	incident, err := pro_interfaces.NotificationIncidentKey(pro_interfaces.NotificationScopeProject, projectID, "template:9")
	require.NoError(t, err)
	firstEvent, err := pro_interfaces.NotificationSourceEventKey(pro_interfaces.NotificationScopeProject, projectID, firstSource, 1)
	require.NoError(t, err)
	secondEvent, err := pro_interfaces.NotificationSourceEventKey(pro_interfaces.NotificationScopeProject, projectID, secondSource, 1)
	require.NoError(t, err)
	assert.NotEmpty(t, incident)
	assert.NotEqual(t, firstEvent, secondEvent)
}

func notificationInt(value int) *int { return &value }
