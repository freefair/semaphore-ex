package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	storeSql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCipher struct {
	enabled    bool
	value      string
	err        error
	decryptErr error
}

func (c *testCipher) OptionEncryptionEnabled() bool { return c.enabled }

func (c *testCipher) EncryptOption(value []byte) (string, error) {
	c.value = string(value)
	if c.err != nil {
		return "", c.err
	}
	return "sealed:" + string(value), nil
}

func (c *testCipher) DecryptOption(value string) ([]byte, error) {
	if c.decryptErr != nil {
		return nil, c.decryptErr
	}
	if !strings.HasPrefix(value, "sealed:") {
		return nil, errors.New("invalid sealed credential")
	}
	return []byte(strings.TrimPrefix(value, "sealed:")), nil
}

func TestDestinationCredentialsFailClosedAndRemainWriteOnly(t *testing.T) {
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	secret := "credential-value"
	disabled := NewGovernanceService(store, &testCipher{}).(*governanceService)
	_, err := disabled.CreateDestination(context.Background(), nil, destinationInput(&secret))
	require.ErrorIs(t, err, pro_interfaces.ErrNotificationEncryptionRequired)

	cipher := &testCipher{enabled: true}
	service := NewGovernanceService(store, cipher).(*governanceService)
	created, err := service.CreateDestination(context.Background(), nil, destinationInput(&secret))
	require.NoError(t, err)
	assert.Equal(t, secret, cipher.value)
	assert.True(t, created.CredentialConfigured)
	assert.NotContains(t, mustJSON(t, created), secret)
	assert.NotContains(t, mustJSON(t, created), "sealed:")

	persisted, err := store.GetNotificationDestination(nil, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "sealed:"+secret, persisted.EncryptedCredential)

	updated, err := service.UpdateDestination(context.Background(), nil, created.ID, created.Revision, destinationInput(nil))
	require.NoError(t, err)
	assert.True(t, updated.CredentialConfigured, "a nil credential retains the encrypted stored value")
	persisted, err = store.GetNotificationDestination(nil, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "sealed:"+secret, persisted.EncryptedCredential)
}

func TestGovernanceRejectsInvalidDestinationRuleAndPreviewInput(t *testing.T) {
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	service := NewGovernanceService(store, &testCipher{enabled: true}).(*governanceService)
	secret := "validation-secret"
	invalidDestination := destinationInput(&secret)
	invalidDestination.Name = " "
	_, err := service.CreateDestination(context.Background(), nil, invalidDestination)
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationInvalidInput)
	invalidCredential := ""
	_, err = service.CreateDestination(context.Background(), nil, destinationInput(&invalidCredential))
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationInvalidInput)

	created, err := service.CreateDestination(context.Background(), nil, destinationInput(&secret))
	require.NoError(t, err)
	invalidRule := notificationRuleInput(created.ID)
	invalidRule.SourceKinds = []pro_interfaces.NotificationSourceKind{pro_interfaces.NotificationSourceTask, pro_interfaces.NotificationSourceTask}
	_, err = service.CreateRule(context.Background(), nil, invalidRule)
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationInvalidInput)
	_, err = service.PreviewRouting(context.Background(), nil, taskEvent(nil))
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationInvalidInput)

	withoutCredential, err := service.CreateDestination(context.Background(), nil, destinationInput(nil))
	require.NoError(t, err)
	_, err = service.EnqueueTestDelivery(context.Background(), nil, withoutCredential.ID)
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationDestinationNotConfigured)
}

func TestDestinationAndRuleScopeAndRevisionBoundaries(t *testing.T) {
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "notification service scope"})
	require.NoError(t, err)
	cipher := &testCipher{enabled: true}
	service := NewGovernanceService(store, cipher).(*governanceService)
	secret := "scoped-secret"
	created, err := service.CreateDestination(context.Background(), &project.ID, destinationInput(&secret))
	require.NoError(t, err)
	_, err = service.GetDestination(context.Background(), nil, created.ID)
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationDestinationMissing)

	_, err = service.UpdateDestination(context.Background(), &project.ID, created.ID, created.Revision+1, destinationInput(nil))
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationRevisionConflict)

	rule, err := service.CreateRule(context.Background(), &project.ID, notificationRuleInput(created.ID))
	require.NoError(t, err)
	_, err = service.UpdateRule(context.Background(), &project.ID, rule.ID, rule.Revision+1, notificationRuleInput(created.ID))
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationRevisionConflict)
	_, err = service.CreateRule(context.Background(), nil, notificationRuleInput(created.ID))
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationDestinationMissing)
}

func TestDestinationAndRuleDeletionAreScopedRevisionedAndRetainHistory(t *testing.T) {
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	first, err := store.CreateProject(db.Project{Name: "notification delete first"})
	require.NoError(t, err)
	second, err := store.CreateProject(db.Project{Name: "notification delete second"})
	require.NoError(t, err)
	service := NewGovernanceService(store, &testCipher{enabled: true}).(*governanceService)
	service.now = func() time.Time { return time.Date(2026, time.August, 31, 14, 0, 0, 0, time.UTC) }
	secret := "delete-secret"
	destination, err := service.CreateDestination(context.Background(), &first.ID, destinationInput(&secret))
	require.NoError(t, err)
	rule, err := service.CreateRule(context.Background(), &first.ID, notificationRuleInput(destination.ID))
	require.NoError(t, err)
	delivery, err := service.EnqueueTestDelivery(context.Background(), &first.ID, destination.ID)
	require.NoError(t, err)

	assert.ErrorIs(t, service.DeleteDestination(context.Background(), &second.ID, destination.ID, destination.Revision), pro_interfaces.ErrNotificationDestinationMissing)
	assert.ErrorIs(t, service.DeleteRule(context.Background(), &second.ID, rule.ID, rule.Revision), pro_interfaces.ErrNotificationRuleMissing)
	assert.ErrorIs(t, service.DeleteDestination(context.Background(), &first.ID, destination.ID, destination.Revision+1), pro_interfaces.ErrNotificationRevisionConflict)
	assert.ErrorIs(t, service.DeleteRule(context.Background(), &first.ID, rule.ID, rule.Revision+1), pro_interfaces.ErrNotificationRevisionConflict)

	require.NoError(t, service.DeleteDestination(context.Background(), &first.ID, destination.ID, destination.Revision))
	_, err = service.GetDestination(context.Background(), &first.ID, destination.ID)
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationDestinationMissing)
	rules, err := service.ListRules(context.Background(), &first.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, rules, "destination deletion must remove same-scope routing rules")
	history, err := service.DeliveryHistory(context.Background(), &first.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, delivery.ID, history[0].ID)
	assert.Equal(t, destination.Name, history[0].DestinationName)

	remaining, err := service.CreateDestination(context.Background(), &first.ID, destinationInput(&secret))
	require.NoError(t, err)
	remainingRule, err := service.CreateRule(context.Background(), &first.ID, notificationRuleInput(remaining.ID))
	require.NoError(t, err)
	require.NoError(t, service.DeleteRule(context.Background(), &first.ID, remainingRule.ID, remainingRule.Revision))
	rules, err = service.ListRules(context.Background(), &first.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestPreviewUsesPureRulesAndEnabledScopedDestinations(t *testing.T) {
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "notification preview"})
	require.NoError(t, err)
	service := NewGovernanceService(store, &testCipher{enabled: true}).(*governanceService)
	secret := "preview-secret"
	first, err := service.CreateDestination(context.Background(), &project.ID, destinationInput(&secret))
	require.NoError(t, err)
	secondInput := destinationInput(&secret)
	secondInput.Name = "secondary"
	second, err := service.CreateDestination(context.Background(), &project.ID, secondInput)
	require.NoError(t, err)
	_, err = service.CreateRule(context.Background(), &project.ID, notificationRuleInput(first.ID))
	require.NoError(t, err)
	duplicate, err := service.CreateRule(context.Background(), &project.ID, notificationRuleInput(first.ID))
	require.NoError(t, err)
	_ = duplicate
	disabledRule := notificationRuleInput(second.ID)
	disabledRule.Enabled = false
	_, err = service.CreateRule(context.Background(), &project.ID, disabledRule)
	require.NoError(t, err)

	event := taskEvent(&project.ID)
	preview, err := service.PreviewRouting(context.Background(), &project.ID, event)
	require.NoError(t, err)
	require.Len(t, preview, 1)
	assert.Equal(t, first.ID, preview[0].DestinationID)
	assert.Equal(t, "pagerduty", preview[0].Provider)

	before, err := store.GetNotificationDeliveries(&project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, before, "preview must not create outbox rows")
	_, err = service.PreviewRouting(context.Background(), nil, event)
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationInvalidInput)
}

func TestTestEnqueuePauseHistoryAndScopedRetry(t *testing.T) {
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	firstProject, err := store.CreateProject(db.Project{Name: "notification test first"})
	require.NoError(t, err)
	secondProject, err := store.CreateProject(db.Project{Name: "notification test second"})
	require.NoError(t, err)
	service := NewGovernanceService(store, &testCipher{enabled: true}).(*governanceService)
	service.now = func() time.Time { return time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC) }
	secret := "test-secret"
	destination, err := service.CreateDestination(context.Background(), &firstProject.ID, destinationInput(&secret))
	require.NoError(t, err)

	paused, err := service.SetDestinationPaused(context.Background(), &firstProject.ID, destination.ID, destination.Revision, true)
	require.NoError(t, err)
	_, err = service.EnqueueTestDelivery(context.Background(), &firstProject.ID, destination.ID)
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationDestinationPaused)
	resumed, err := service.SetDestinationPaused(context.Background(), &firstProject.ID, destination.ID, paused.Revision, false)
	require.NoError(t, err)
	assert.False(t, resumed.Paused)

	delivery, err := service.EnqueueTestDelivery(context.Background(), &firstProject.ID, destination.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliveryPending, delivery.Status)
	assert.NotEmpty(t, delivery.EventID)
	assert.NotEmpty(t, delivery.IncidentKey)
	history, err := service.DeliveryHistory(context.Background(), &firstProject.ID, db.RetrieveQueryParams{Count: 1})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, delivery.ID, history[0].ID)
	assert.Equal(t, pro_interfaces.NotificationSourceSystem, history[0].SourceKind)
	assert.Equal(t, pro_interfaces.NotificationLifecycleTrigger, history[0].LifecycleAction)
	assert.Equal(t, pro_interfaces.NotificationSeverityInfo, history[0].Severity)
	assert.Equal(t, service.now(), history[0].OccurredAt)
	assert.NotContains(t, mustJSON(t, history[0]), "test-secret")
	assert.NotContains(t, mustJSON(t, history[0]), "details")

	_, err = service.RetryDelivery(context.Background(), &secondProject.ID, delivery.ID)
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationDestinationMissing)
	_, err = service.RetryDelivery(context.Background(), &firstProject.ID, delivery.ID)
	assert.ErrorIs(t, err, pro_interfaces.ErrNotificationRetryUnavailable)

	claimTime := time.Now().UTC().Add(time.Minute)
	claimed, err := store.ClaimNotificationDeliveries(claimTime, claimTime.Add(time.Minute), 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NoError(t, store.MarkNotificationDeliveryFailed(claimed[0].ID, claimed[0].LeaseToken, db.NotificationDeliveryReasonTransport, claimTime))
	retried, err := service.RetryDelivery(context.Background(), &firstProject.ID, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliveryRetrying, retried.Status)
	assert.Equal(t, db.NotificationDeliveryReasonManualRetry, retried.LastReason)
	assert.Equal(t, delivery.EventID, retried.EventID, "manual retry preserves logical event identity")
}

func TestEventHistoryMakesFilteredAndRoutedOutcomesInspectableWithoutDetails(t *testing.T) {
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	service := NewGovernanceService(store, &testCipher{enabled: true}).(*governanceService)
	service.now = func() time.Time { return time.Date(2026, time.August, 31, 15, 0, 0, 0, time.UTC) }
	filtered := pro_interfaces.NotificationEvent{
		Scope: pro_interfaces.NotificationScopeGlobal, Source: pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceSystem, ID: "system:history"},
		LifecycleID: "system:history", SourceRevision: 1, Severity: pro_interfaces.NotificationSeverityWarning,
		LifecycleAction: pro_interfaces.NotificationLifecycleUpdate, Details: pro_interfaces.NotificationDetails{Status: "degraded"},
	}
	require.NoError(t, store.RecordGlobalSystemNotification(filtered))
	secret := "history-secret"
	destination, err := service.CreateDestination(context.Background(), nil, destinationInput(&secret))
	require.NoError(t, err)
	routed, err := service.EnqueueTestDelivery(context.Background(), nil, destination.ID)
	require.NoError(t, err)

	events, err := service.EventHistory(context.Background(), nil, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, routed.EventID, events[0].EventID)
	assert.Equal(t, db.NotificationRoutingRouted, events[0].RoutingOutcome)
	assert.Equal(t, db.NotificationRoutingFiltered, events[1].RoutingOutcome)
	assert.Equal(t, pro_interfaces.NotificationSourceSystem, events[1].SourceKind)
	assert.NotContains(t, mustJSON(t, events), "details")
	assert.NotContains(t, mustJSON(t, events), secret)
}

func TestConcurrentDestinationUpdateAllowsOneRevision(t *testing.T) {
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	service := NewGovernanceService(store, &testCipher{enabled: true}).(*governanceService)
	secret := "concurrent-secret"
	created, err := service.CreateDestination(context.Background(), nil, destinationInput(&secret))
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, name := range []string{"first", "second"} {
		workers.Add(1)
		go func(name string) {
			defer workers.Done()
			<-start
			input := destinationInput(nil)
			input.Name = name
			_, updateErr := service.UpdateDestination(context.Background(), nil, created.ID, created.Revision, input)
			results <- updateErr
		}(name)
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, pro_interfaces.ErrNotificationRevisionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent update error: %v", result)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
}

func TestConcurrentDestinationDeletionAllowsOneRevision(t *testing.T) {
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	service := NewGovernanceService(store, &testCipher{enabled: true}).(*governanceService)
	secret := "concurrent-delete-secret"
	destination, err := service.CreateDestination(context.Background(), nil, destinationInput(&secret))
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- service.DeleteDestination(context.Background(), nil, destination.ID, destination.Revision)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, pro_interfaces.ErrNotificationRevisionConflict), errors.Is(result, pro_interfaces.ErrNotificationDestinationMissing):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent delete error: %v", result)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
}

func destinationInput(credential *string) pro_interfaces.NotificationDestinationInput {
	return pro_interfaces.NotificationDestinationInput{Name: "primary", Provider: "pagerduty", Environment: "production", Credential: credential, Enabled: true}
}

func notificationRuleInput(destinationID int) pro_interfaces.NotificationRuleInput {
	return pro_interfaces.NotificationRuleInput{
		DestinationID: destinationID, SourceKinds: []pro_interfaces.NotificationSourceKind{pro_interfaces.NotificationSourceTask},
		LifecycleActions: []pro_interfaces.NotificationLifecycleAction{pro_interfaces.NotificationLifecycleTrigger},
		MinimumSeverity:  pro_interfaces.NotificationSeverityError, Enabled: true,
	}
}

func taskEvent(projectID *int) pro_interfaces.NotificationEvent {
	taskID, templateID := 12, 7
	return pro_interfaces.NotificationEvent{
		Scope: pro_interfaces.NotificationScopeProject, ProjectID: projectID, SourceRevision: 1,
		Source: pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceTask, ID: "task:12"}, LifecycleID: "template:7",
		Severity: pro_interfaces.NotificationSeverityError, LifecycleAction: pro_interfaces.NotificationLifecycleTrigger,
		Details: pro_interfaces.NotificationDetails{TaskID: &taskID, TemplateID: &templateID, Status: "failed"},
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}
