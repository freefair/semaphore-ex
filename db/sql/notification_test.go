package sql

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationMigrationAddsDurableGovernanceTables(t *testing.T) {
	legacyVersion := "2.20.50"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	require.NoError(t, db.Migrate(store, nil))
	for _, table := range []string{"notification_destination", "notification_rule", "notification_event", "notification_delivery"} {
		assert.Contains(t, sqliteTableNames(t, store), table)
	}
	for _, column := range []string{"lease_token", "idempotency_key", "last_reason"} {
		assert.Contains(t, sqliteColumnNames(t, store, "notification_delivery"), column)
	}
	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteTableNames(t, store), "notification_delivery")
}

func TestNotificationMigrationKeepsLOBAndRollbackPortable(t *testing.T) {
	migration := strings.Join(getVersionSQL("mysql", "v2.20.51.sql", false), ";")
	rollback := strings.Join(getVersionSQL("mysql", "v2.20.51.err.sql", true), ";")
	eventDefinition := strings.Split(strings.Split(migration, "create table `notification_event`")[1], "create index `notification_event__scope_history`")[0]
	deliveryDefinition := strings.Split(strings.Split(migration, "create table `notification_delivery`")[1], "create index `notification_delivery__due`")[0]
	assert.Contains(t, migration, "`encrypted_credential` longtext not null")
	assert.NotContains(t, migration, "`encrypted_credential` longtext not null default")
	assert.NotContains(t, eventDefinition, "foreign key")
	assert.NotContains(t, deliveryDefinition, "foreign key (`destination_id`)")
	assert.NotContains(t, rollback, "drop index")
}

func TestNotificationDestinationIsScopedRevisionedAndCredentialWriteOnly(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "notification scope"})
	require.NoError(t, err)

	destination, err := store.CreateNotificationDestination(db.NotificationDestination{
		ProjectID: &project.ID, Name: "primary", Provider: "pagerduty", Environment: "production",
		EncryptedCredential: "sealed:secret", CredentialConfigured: true, Enabled: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, destination.Revision)
	encoded, err := json.Marshal(destination)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "sealed:secret")

	_, err = store.GetNotificationDestination(nil, destination.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	destination.Name = "primary-updated"
	updated, err := store.UpdateNotificationDestination(destination, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, updated.Revision)
	_, err = store.UpdateNotificationDestination(updated, 1)
	assert.ErrorIs(t, err, db.ErrNotificationDestinationRevisionConflict)
}

func TestNotificationOutboxIsIdempotentAndLeaseFenced(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "notification outbox"})
	require.NoError(t, err)
	destination, err := store.CreateNotificationDestination(db.NotificationDestination{
		ProjectID: &project.ID, Name: "primary", Provider: "pagerduty", Enabled: true,
	})
	require.NoError(t, err)
	event := notificationTestEvent(&project.ID, strings.Repeat("a", 32), "task", "task:1", "error", "trigger", db.NotificationRoutingRouted)
	delivery := db.NotificationDelivery{DestinationID: destination.ID, DestinationRevision: destination.Revision, DestinationName: destination.Name, DestinationProvider: destination.Provider, DestinationEnvironment: destination.Environment}
	created, err := store.CreateNotificationEventWithRouting(event, []db.NotificationDelivery{delivery})
	require.NoError(t, err)
	replay := event
	replay.EventID = strings.Repeat("d", 32)
	replayed, err := store.CreateNotificationEventWithRouting(replay, []db.NotificationDelivery{delivery})
	require.NoError(t, err)
	assert.Equal(t, created.ID, replayed.ID)

	now := time.Now().UTC().Add(time.Minute)
	claimed, err := store.ClaimNotificationDeliveries(now, now.Add(time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, event.EventID, claimed[0].EventID)
	assert.Equal(t, destination.Provider, claimed[0].DestinationProvider)
	assert.NotEmpty(t, claimed[0].LeaseToken)
	assert.ErrorIs(t, store.MarkNotificationDeliverySucceeded(claimed[0].ID, "stale", now), db.ErrNotificationDeliveryNotClaimed)
	require.NoError(t, store.MarkNotificationDeliveryFailed(claimed[0].ID, claimed[0].LeaseToken, db.NotificationDeliveryReasonTransport, now))
	require.NoError(t, store.RetryNotificationDelivery(claimed[0].ID, now.Add(time.Second)))

	history, err := store.GetNotificationDeliveries(&project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, event.EventID, history[0].EventID)
	assert.Equal(t, event.IncidentKey, history[0].IncidentKey)
	assert.Equal(t, "task", history[0].SourceKind)
	assert.Equal(t, "task:1", history[0].SourceID)
	assert.Equal(t, "trigger", history[0].LifecycleAction)
	assert.Equal(t, "error", history[0].Severity)
	assert.Equal(t, event.OccurredAt, history[0].OccurredAt)
	encoded, encodeErr := json.Marshal(history[0])
	require.NoError(t, encodeErr)
	assert.NotContains(t, string(encoded), "details")
	assert.NotContains(t, string(encoded), "sealed:")
	assert.Equal(t, db.NotificationDeliveryRetrying, history[0].Status)
	assert.Equal(t, db.NotificationDeliveryReasonManualRetry, history[0].LastReason)
}

func TestNotificationFilteredEventPersistsWithoutDelivery(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	event := notificationTestEvent(nil, strings.Repeat("e", 32), "system", "system:node", "warning", "update", db.NotificationRoutingFiltered)
	_, err := store.CreateNotificationEventWithRouting(event, nil)
	require.NoError(t, err)
	history, err := store.GetNotificationDeliveries(nil, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, history)
}

func TestNotificationEventHistoryExposesFilteredAndRoutedEnvelopesByScope(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	first, err := store.CreateProject(db.Project{Name: "notification history first"})
	require.NoError(t, err)
	second, err := store.CreateProject(db.Project{Name: "notification history second"})
	require.NoError(t, err)
	filtered := notificationTestEvent(&first.ID, strings.Repeat("7", 32), "task", "task:71", "error", "trigger", db.NotificationRoutingFiltered)
	_, err = store.CreateNotificationEventWithRouting(filtered, nil)
	require.NoError(t, err)
	destination, err := store.CreateNotificationDestination(db.NotificationDestination{ProjectID: &first.ID, Name: "history", Provider: "pagerduty", Enabled: true})
	require.NoError(t, err)
	routed := notificationTestEvent(&first.ID, strings.Repeat("8", 32), "task", "task:72", "error", "trigger", db.NotificationRoutingRouted)
	_, err = store.CreateNotificationEventWithRouting(routed, []db.NotificationDelivery{{
		DestinationID: destination.ID, DestinationRevision: destination.Revision, DestinationName: destination.Name, DestinationProvider: destination.Provider,
	}})
	require.NoError(t, err)

	firstPage, err := store.GetNotificationEventHistory(&first.ID, db.RetrieveQueryParams{Count: 1})
	require.NoError(t, err)
	require.Len(t, firstPage, 1)
	assert.Equal(t, routed.EventID, firstPage[0].EventID)
	assert.Equal(t, db.NotificationRoutingRouted, firstPage[0].RoutingOutcome)
	assert.Equal(t, "task", firstPage[0].SourceKind)
	assert.Equal(t, "task:72", firstPage[0].SourceID)
	assert.Equal(t, "trigger", firstPage[0].LifecycleAction)
	assert.Equal(t, "error", firstPage[0].Severity)
	encoded, encodeErr := json.Marshal(firstPage[0])
	require.NoError(t, encodeErr)
	assert.NotContains(t, string(encoded), "details")
	assert.NotContains(t, string(encoded), "credential")

	secondPage, err := store.GetNotificationEventHistory(&first.ID, db.RetrieveQueryParams{Count: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, secondPage, 1)
	assert.Equal(t, filtered.EventID, secondPage[0].EventID)
	assert.Equal(t, db.NotificationRoutingFiltered, secondPage[0].RoutingOutcome)
	otherScope, err := store.GetNotificationEventHistory(&second.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, otherScope)
	globalScope, err := store.GetNotificationEventHistory(nil, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, globalScope)
}

func TestNotificationHistorySurvivesProjectAndDestinationDeletion(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "notification retention"})
	require.NoError(t, err)
	destination, err := store.CreateNotificationDestination(db.NotificationDestination{
		ProjectID: &project.ID, Name: "retained", Provider: "pagerduty", Environment: "production", Enabled: true,
	})
	require.NoError(t, err)
	event := notificationTestEvent(&project.ID, strings.Repeat("1", 32), "task", "task:9", "error", "trigger", db.NotificationRoutingRouted)
	_, err = store.CreateNotificationEventWithRouting(event, []db.NotificationDelivery{{
		DestinationID: destination.ID, DestinationRevision: destination.Revision, DestinationName: destination.Name,
		DestinationProvider: destination.Provider, DestinationEnvironment: destination.Environment,
	}})
	require.NoError(t, err)
	_, err = store.exec("delete from project where id=?", project.ID)
	require.NoError(t, err)

	var count int
	require.NoError(t, store.selectOne(&count, "select count(*) from notification_event where event_id=?", event.EventID))
	assert.Equal(t, 1, count)
	var delivery db.NotificationDelivery
	require.NoError(t, store.selectOne(&delivery, "select d.*, e.event_id from notification_delivery d join notification_event e on e.id=d.notification_event_id where e.event_id=?", event.EventID))
	assert.Equal(t, "retained", delivery.DestinationName)
	assert.Equal(t, "production", delivery.DestinationEnvironment)
}

func TestNotificationDistinctTaskSourcesShareLifecycleIncident(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "notification lifecycle"})
	require.NoError(t, err)
	destination, err := store.CreateNotificationDestination(db.NotificationDestination{
		ProjectID: &project.ID, Name: "primary", Provider: "pagerduty", Enabled: true,
	})
	require.NoError(t, err)
	delivery := db.NotificationDelivery{DestinationID: destination.ID, DestinationRevision: destination.Revision, DestinationName: destination.Name, DestinationProvider: destination.Provider}
	first := notificationTestEvent(&project.ID, strings.Repeat("4", 32), "task", "task:41", "error", "trigger", db.NotificationRoutingRouted)
	second := notificationTestEvent(&project.ID, strings.Repeat("5", 32), "task", "task:42", "error", "trigger", db.NotificationRoutingRouted)
	createdFirst, err := store.CreateNotificationEventWithRouting(first, []db.NotificationDelivery{delivery})
	require.NoError(t, err)
	createdSecond, err := store.CreateNotificationEventWithRouting(second, []db.NotificationDelivery{delivery})
	require.NoError(t, err)
	assert.NotEqual(t, createdFirst.SourceEventKey, createdSecond.SourceEventKey)
	assert.Equal(t, createdFirst.IncidentKey, createdSecond.IncidentKey)
}

func TestNotificationRouterRollbackAndScopeIsolation(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "isolated"})
	require.NoError(t, err)
	projectDestination, err := store.CreateNotificationDestination(db.NotificationDestination{ProjectID: &project.ID, Name: "project", Provider: "pagerduty", Enabled: true})
	require.NoError(t, err)
	_, err = store.CreateNotificationRule(db.NotificationRule{ProjectID: &project.ID, DestinationID: projectDestination.ID, SourceKinds: "task", LifecycleActions: "trigger", MinimumSeverity: "error", Enabled: true})
	require.NoError(t, err)
	projectID, taskID, templateID := project.ID, 42, 9
	event := pro_interfaces.NotificationEvent{Scope: pro_interfaces.NotificationScopeProject, ProjectID: &projectID, Source: pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceTask, ID: "task:42"}, LifecycleID: "template:9", SourceRevision: 1, Severity: pro_interfaces.NotificationSeverityError, LifecycleAction: pro_interfaces.NotificationLifecycleTrigger, Details: pro_interfaces.NotificationDetails{TaskID: &taskID, TemplateID: &templateID, Status: "failed"}}
	tx, err := store.Sql().Begin()
	require.NoError(t, err)
	require.NoError(t, store.routeNotificationTx(tx, event))
	require.NoError(t, tx.Rollback())
	history, err := store.GetNotificationDeliveries(&project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, history)
	require.NoError(t, store.RecordGlobalSystemNotification(pro_interfaces.NotificationEvent{Scope: pro_interfaces.NotificationScopeGlobal, Source: pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceSystem, ID: "system:node"}, LifecycleID: "system:node", SourceRevision: 1, Severity: pro_interfaces.NotificationSeverityWarning, LifecycleAction: pro_interfaces.NotificationLifecycleUpdate, Details: pro_interfaces.NotificationDetails{Status: "degraded"}}))
	history, err = store.GetNotificationDeliveries(&project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, history)
}

func notificationTestEvent(projectID *int, eventID, sourceKind, sourceID, severity, action string, outcome db.NotificationRoutingOutcome) db.NotificationEvent {
	lifecycleID := "system:node"
	details := `{"status":"failed"}`
	if sourceKind == "task" {
		lifecycleID = "template:9"
		taskID := strings.TrimPrefix(sourceID, "task:")
		details = `{"task_id":` + taskID + `,"template_id":9,"status":"failed"}`
	}
	incidentKey := notificationIncidentKey(projectID, lifecycleID)
	return db.NotificationEvent{
		SchemaVersion: "semaphore.notification.v1", EventID: eventID, SourceEventKey: notificationSourceEventKey(projectID, sourceKind, sourceID, 1), SourceRevision: 1,
		ProjectID: projectID, SourceKind: sourceKind, SourceID: sourceID, LifecycleID: lifecycleID, Severity: severity, LifecycleAction: action,
		IncidentKey: incidentKey, Details: details, RoutingOutcome: outcome, OccurredAt: time.Now().UTC(),
	}
}
