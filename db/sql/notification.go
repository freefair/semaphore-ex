package sql

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// NotificationTransactionRouter exposes the core-owned notification routing
// implementation to Enhanced SQL repositories that already hold a gorp
// transaction. It deliberately stays in db/sql: pro_interfaces remains free
// of persistence framework types.
type NotificationTransactionRouter struct {
	connection *SqlDbConnection
}

func NewNotificationTransactionRouter(connection *SqlDbConnection) *NotificationTransactionRouter {
	return &NotificationTransactionRouter{connection: connection}
}

func (r *NotificationTransactionRouter) RouteTx(tx *gorp.Transaction, event pro_interfaces.NotificationEvent) error {
	if r == nil || r.connection == nil {
		return errors.New("notification transaction router is unavailable")
	}
	return (&SqlDb{connection: *r.connection}).routeNotificationTx(tx, event)
}

// RecordGlobalSystemNotification persists a global system lifecycle event and
// its filtered/routed outcome in one explicit transaction. Later system
// services and test-send flows use this instead of post-mutation audit calls.
func (d *SqlDb) RecordGlobalSystemNotification(event pro_interfaces.NotificationEvent) error {
	if event.Scope != pro_interfaces.NotificationScopeGlobal || event.ProjectID != nil || event.Source.Kind != pro_interfaces.NotificationSourceSystem {
		return db.ErrInvalidOperation
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = d.routeNotificationTx(tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *SqlDb) CreateNotificationDestination(destination db.NotificationDestination) (db.NotificationDestination, error) {
	if err := validateNotificationDestination(destination); err != nil {
		return db.NotificationDestination{}, err
	}
	now := tz.Now()
	destination.Revision = 1
	destination.ConfigurationRevision = 1
	destination.Created = now
	destination.Updated = now
	if err := d.Sql().Insert(&destination); err != nil {
		return db.NotificationDestination{}, err
	}
	return destination, nil
}

func (d *SqlDb) GetNotificationDestination(projectID *int, id int) (db.NotificationDestination, error) {
	var destination db.NotificationDestination
	err := d.selectOne(&destination, notificationDestinationScopeQuery("id=?", projectID), append([]any{id}, notificationScopeArgs(projectID)...)...)
	return destination, err
}

func (d *SqlDb) GetNotificationDestinations(projectID *int, params db.RetrieveQueryParams) ([]db.NotificationDestination, error) {
	query := squirrel.Select("*").From("notification_destination").Where(notificationScopePredicate(projectID)).OrderBy("id desc")
	if params.Count > 0 {
		query = query.Limit(uint64(params.Count))
	}
	if params.Offset > 0 {
		query = query.Offset(uint64(params.Offset))
	}
	statement, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}
	var destinations []db.NotificationDestination
	_, err = d.selectAll(&destinations, statement, append(args, notificationScopeArgs(projectID)...)...)
	return destinations, err
}

func (d *SqlDb) UpdateNotificationDestination(destination db.NotificationDestination, expectedRevision int) (db.NotificationDestination, error) {
	if expectedRevision <= 0 || destination.Revision != expectedRevision {
		return db.NotificationDestination{}, db.ErrNotificationDestinationRevisionConflict
	}
	if err := validateNotificationDestination(destination); err != nil {
		return db.NotificationDestination{}, err
	}
	now := tz.Now()
	query := "update notification_destination set name=?, provider=?, environment=?, region=?, provider_config=?, encrypted_credential=?, credential_configured=?, enabled=?, paused=?, revision=revision+1, configuration_revision=configuration_revision+1, updated=? where id=? and revision=? and " + notificationScopePredicate(destination.ProjectID)
	args := []any{destination.Name, destination.Provider, destination.Environment, destination.Region, destination.ProviderConfig, destination.EncryptedCredential, destination.CredentialConfigured, destination.Enabled, destination.Paused, now, destination.ID, expectedRevision}
	args = append(args, notificationScopeArgs(destination.ProjectID)...)
	result, err := d.exec(query, args...)
	if err != nil {
		return db.NotificationDestination{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return db.NotificationDestination{}, err
	}
	if rows != 1 {
		return db.NotificationDestination{}, db.ErrNotificationDestinationRevisionConflict
	}
	return d.GetNotificationDestination(destination.ProjectID, destination.ID)
}

// SetNotificationDestinationPaused changes only the administrative pause state
// and its optimistic-lock revision. It deliberately preserves the immutable
// configuration generation used to bind queued deliveries before decryption.
func (d *SqlDb) SetNotificationDestinationPaused(destination db.NotificationDestination, expectedRevision int) (db.NotificationDestination, error) {
	if destination.ID <= 0 || expectedRevision <= 0 || destination.Revision != expectedRevision {
		return db.NotificationDestination{}, db.ErrNotificationDestinationRevisionConflict
	}
	now := tz.Now()
	query := "update notification_destination set paused=?, revision=revision+1, updated=? where id=? and revision=? and " + notificationScopePredicate(destination.ProjectID)
	args := append([]any{destination.Paused, now, destination.ID, expectedRevision}, notificationScopeArgs(destination.ProjectID)...)
	result, err := d.exec(query, args...)
	if err != nil {
		return db.NotificationDestination{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return db.NotificationDestination{}, err
	}
	if rows != 1 {
		return db.NotificationDestination{}, db.ErrNotificationDestinationRevisionConflict
	}
	return d.GetNotificationDestination(destination.ProjectID, destination.ID)
}

// DeleteNotificationDestination deletes only a matching scoped revision. Its
// rules are removed in the same transaction before the restricted foreign key
// is reached; notification events and deliveries intentionally have no
// destination foreign key, preserving their immutable history snapshots.
func (d *SqlDb) DeleteNotificationDestination(projectID *int, id, expectedRevision int) error {
	if id <= 0 || expectedRevision <= 0 {
		return db.ErrNotificationDestinationRevisionConflict
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ruleQuery := "delete from notification_rule where destination_id=? and " + notificationScopePredicate(projectID)
	if _, err = d.execTx(tx, ruleQuery, append([]any{id}, notificationScopeArgs(projectID)...)...); err != nil {
		return err
	}
	destinationQuery := "delete from notification_destination where id=? and revision=? and " + notificationScopePredicate(projectID)
	result, err := d.execTx(tx, destinationQuery, append([]any{id, expectedRevision}, notificationScopeArgs(projectID)...)...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrNotificationDestinationRevisionConflict
	}
	return tx.Commit()
}

func (d *SqlDb) CreateNotificationRule(rule db.NotificationRule) (db.NotificationRule, error) {
	if err := d.validateNotificationRule(rule); err != nil {
		return db.NotificationRule{}, err
	}
	now := tz.Now()
	rule.Revision = 1
	rule.Created = now
	rule.Updated = now
	if err := d.Sql().Insert(&rule); err != nil {
		return db.NotificationRule{}, err
	}
	return rule, nil
}

func (d *SqlDb) GetNotificationRule(projectID *int, id int) (db.NotificationRule, error) {
	var rule db.NotificationRule
	err := d.selectOne(&rule, notificationRuleScopeQuery("id=?", projectID), append([]any{id}, notificationScopeArgs(projectID)...)...)
	return rule, err
}

func (d *SqlDb) GetNotificationRules(projectID *int, params db.RetrieveQueryParams) ([]db.NotificationRule, error) {
	query := squirrel.Select("*").From("notification_rule").Where(notificationScopePredicate(projectID)).OrderBy("id desc")
	if params.Count > 0 {
		query = query.Limit(uint64(params.Count))
	}
	if params.Offset > 0 {
		query = query.Offset(uint64(params.Offset))
	}
	statement, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}
	var rules []db.NotificationRule
	_, err = d.selectAll(&rules, statement, append(args, notificationScopeArgs(projectID)...)...)
	return rules, err
}

func (d *SqlDb) UpdateNotificationRule(rule db.NotificationRule, expectedRevision int) (db.NotificationRule, error) {
	if expectedRevision <= 0 || rule.Revision != expectedRevision {
		return db.NotificationRule{}, db.ErrNotificationRuleRevisionConflict
	}
	if err := d.validateNotificationRule(rule); err != nil {
		return db.NotificationRule{}, err
	}
	now := tz.Now()
	query := "update notification_rule set destination_id=?, source_kinds=?, lifecycle_actions=?, minimum_severity=?, enabled=?, revision=revision+1, updated=? where id=? and revision=? and " + notificationScopePredicate(rule.ProjectID)
	args := []any{rule.DestinationID, rule.SourceKinds, rule.LifecycleActions, rule.MinimumSeverity, rule.Enabled, now, rule.ID, expectedRevision}
	args = append(args, notificationScopeArgs(rule.ProjectID)...)
	result, err := d.exec(query, args...)
	if err != nil {
		return db.NotificationRule{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return db.NotificationRule{}, err
	}
	if rows != 1 {
		return db.NotificationRule{}, db.ErrNotificationRuleRevisionConflict
	}
	var updated db.NotificationRule
	err = d.selectOne(&updated, notificationRuleScopeQuery("id=?", rule.ProjectID), append([]any{rule.ID}, notificationScopeArgs(rule.ProjectID)...)...)
	return updated, err
}

func (d *SqlDb) DeleteNotificationRule(projectID *int, id, expectedRevision int) error {
	if id <= 0 || expectedRevision <= 0 {
		return db.ErrNotificationRuleRevisionConflict
	}
	query := "delete from notification_rule where id=? and revision=? and " + notificationScopePredicate(projectID)
	result, err := d.exec(query, append([]any{id, expectedRevision}, notificationScopeArgs(projectID)...)...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrNotificationRuleRevisionConflict
	}
	return nil
}

func (d *SqlDb) CreateNotificationEventWithRouting(event db.NotificationEvent, deliveries []db.NotificationDelivery) (db.NotificationEvent, error) {
	if err := validateNotificationRouting(event, deliveries); err != nil {
		return db.NotificationEvent{}, err
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.NotificationEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	created, err := d.createNotificationEventWithRoutingTx(tx, event, deliveries)
	if err != nil {
		_ = tx.Rollback()
		if isUniqueConstraintError(err) {
			var existing db.NotificationEvent
			if getErr := d.selectOne(&existing, "select * from notification_event where event_id=? or source_event_key=?", event.EventID, event.SourceEventKey); getErr == nil && sameNotificationTransition(existing, event) {
				return existing, nil
			}
		}
		return db.NotificationEvent{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.NotificationEvent{}, err
	}
	return created, nil
}

// createNotificationEventWithRoutingTx is intentionally transaction-scoped so
// later task/workflow/approval source repositories can invoke it from their
// own mutation transaction rather than emitting a post-commit notification.
func (d *SqlDb) createNotificationEventWithRoutingTx(tx *gorp.Transaction, event db.NotificationEvent, deliveries []db.NotificationDelivery) (db.NotificationEvent, error) {
	event.Created = tz.Now()
	if err := tx.Insert(&event); err != nil {
		return db.NotificationEvent{}, err
	}
	for _, delivery := range deliveries {
		delivery.NotificationEventID = event.ID
		delivery.IncidentKey = event.IncidentKey
		delivery.Status = db.NotificationDeliveryPending
		delivery.Attempts = 0
		delivery.NextAttempt = event.Created
		delivery.LeaseToken = ""
		delivery.LeaseUntil = nil
		delivery.LastReason = db.NotificationDeliveryReasonNone
		delivery.Created = event.Created
		delivery.Updated = event.Created
		delivery.DeliveredAt = nil
		key, err := notificationDeliveryKey(event.EventID, delivery.DestinationID)
		if err != nil {
			return db.NotificationEvent{}, err
		}
		delivery.IdempotencyKey = key
		if _, err = d.execTx(tx,
			"insert into notification_delivery(notification_event_id, destination_id, destination_revision, destination_configuration_revision, destination_name, destination_provider, destination_environment, destination_region, incident_key, idempotency_key, provider_request_id, status, attempts, next_attempt, lease_token, lease_until, last_reason, created, updated, delivered_at) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			delivery.NotificationEventID, delivery.DestinationID, delivery.DestinationRevision, delivery.DestinationConfigurationRevision, delivery.DestinationName, delivery.DestinationProvider, delivery.DestinationEnvironment, delivery.DestinationRegion, delivery.IncidentKey, delivery.IdempotencyKey,
			delivery.ProviderRequestID, delivery.Status, delivery.Attempts, delivery.NextAttempt, delivery.LeaseToken, delivery.LeaseUntil, delivery.LastReason,
			delivery.Created, delivery.Updated, delivery.DeliveredAt,
		); err != nil {
			return db.NotificationEvent{}, err
		}
	}
	return event, nil
}

// routeNotificationTx resolves enabled same-scope rules inside the caller's
// transaction. Source mutations invoke it only after their conditional update
// succeeds, so stale/replayed transitions cannot leave orphaned outbox rows.
func (d *SqlDb) routeNotificationTx(tx *gorp.Transaction, event pro_interfaces.NotificationEvent) error {
	event, err := event.EnsureIdentity(tz.Now())
	if err != nil {
		return err
	}
	var rules []db.NotificationRule
	scopeQuery, args := notificationRoutingRuleLookup(event.ProjectID)
	if _, err = tx.Select(&rules, d.PrepareQuery(scopeQuery), args...); err != nil {
		return err
	}
	deliveries := make([]db.NotificationDelivery, 0, len(rules))
	seenDestinations := make(map[int]struct{})
	for _, rule := range rules {
		filter := pro_interfaces.NotificationRoutingRule{
			SourceKinds:     notificationSourceKinds(rule.SourceKinds),
			Actions:         notificationLifecycleActions(rule.LifecycleActions),
			MinimumSeverity: pro_interfaces.NotificationSeverity(rule.MinimumSeverity),
			Enabled:         rule.Enabled,
		}
		if !filter.Matches(event) {
			continue
		}
		var destination db.NotificationDestination
		destinationQuery := "select * from notification_destination where id=? and project_id is null"
		destinationArgs := []any{rule.DestinationID}
		if event.ProjectID != nil {
			destinationQuery = "select * from notification_destination where id=? and project_id=?"
			destinationArgs = append(destinationArgs, *event.ProjectID)
		}
		if err = tx.SelectOne(&destination, d.PrepareQuery(destinationQuery), destinationArgs...); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if !destination.Enabled {
			continue
		}
		if _, duplicate := seenDestinations[destination.ID]; duplicate {
			continue
		}
		seenDestinations[destination.ID] = struct{}{}
		deliveries = append(deliveries, db.NotificationDelivery{
			DestinationID: destination.ID, DestinationRevision: destination.Revision, DestinationConfigurationRevision: destination.ConfigurationRevision,
			DestinationName: destination.Name, DestinationProvider: destination.Provider,
			DestinationEnvironment: destination.Environment, DestinationRegion: destination.Region,
		})
	}
	details, err := json.Marshal(event.Details)
	if err != nil {
		return err
	}
	routingOutcome := db.NotificationRoutingFiltered
	if len(deliveries) > 0 {
		routingOutcome = db.NotificationRoutingRouted
	}
	_, err = d.createNotificationEventWithRoutingTx(tx, db.NotificationEvent{
		SchemaVersion: event.SchemaVersion, EventID: event.EventID, SourceEventKey: event.SourceEventKey,
		SourceRevision: event.SourceRevision, ProjectID: event.ProjectID, SourceKind: string(event.Source.Kind),
		SourceID: event.Source.ID, LifecycleID: event.LifecycleID, Severity: string(event.Severity),
		LifecycleAction: string(event.LifecycleAction), IncidentKey: event.IncidentKey, Details: string(details),
		RoutingOutcome: routingOutcome, OccurredAt: event.OccurredAt,
	}, deliveries)
	return err
}

func notificationRoutingRuleLookup(projectID *int) (string, []any) {
	query := "select * from notification_rule where enabled=? and project_id is null"
	args := []any{1}
	if projectID != nil {
		query = "select * from notification_rule where enabled=? and project_id=?"
		args = append(args, *projectID)
	}
	return query, args
}

func notificationSourceKinds(value string) []pro_interfaces.NotificationSourceKind {
	entries := strings.Split(value, ",")
	result := make([]pro_interfaces.NotificationSourceKind, len(entries))
	for index, entry := range entries {
		result[index] = pro_interfaces.NotificationSourceKind(entry)
	}
	return result
}

func notificationLifecycleActions(value string) []pro_interfaces.NotificationLifecycleAction {
	entries := strings.Split(value, ",")
	result := make([]pro_interfaces.NotificationLifecycleAction, len(entries))
	for index, entry := range entries {
		result[index] = pro_interfaces.NotificationLifecycleAction(entry)
	}
	return result
}

func (d *SqlDb) ClaimNotificationDeliveries(now, leaseUntil time.Time, limit int) ([]db.NotificationDelivery, error) {
	if limit <= 0 || !leaseUntil.After(now) {
		return []db.NotificationDelivery{}, nil
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	query, args, err := squirrel.Select("d.*", "e.event_id").From("notification_delivery d").Join("notification_event e on e.id=d.notification_event_id").
		Where("((d.status in (?, ?) and d.next_attempt<=?) or (d.status=? and d.lease_until<=?))", db.NotificationDeliveryPending, db.NotificationDeliveryRetrying, now, db.NotificationDeliveryRunning, now).
		OrderBy("d.next_attempt asc", "d.id asc").Limit(uint64(limit)).ToSql()
	if err != nil {
		return nil, err
	}
	var candidates []db.NotificationDelivery
	if _, err = tx.Select(&candidates, d.PrepareQuery(query), formatArgs(args)...); err != nil {
		return nil, err
	}
	claimed := make([]db.NotificationDelivery, 0, len(candidates))
	for _, candidate := range candidates {
		token, tokenErr := notificationLeaseToken()
		if tokenErr != nil {
			return nil, tokenErr
		}
		result, updateErr := d.execTx(tx,
			"update notification_delivery set status=?, lease_token=?, lease_until=?, updated=? where id=? and ((status in (?, ?) and next_attempt<=?) or (status=? and lease_until<=?))",
			db.NotificationDeliveryRunning, token, leaseUntil, now, candidate.ID,
			db.NotificationDeliveryPending, db.NotificationDeliveryRetrying, now, db.NotificationDeliveryRunning, now,
		)
		if updateErr != nil {
			return nil, updateErr
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return nil, rowsErr
		}
		if rows == 1 {
			candidate.Status = db.NotificationDeliveryRunning
			candidate.LeaseToken = token
			candidate.LeaseUntil = &leaseUntil
			claimed = append(claimed, candidate)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (d *SqlDb) MarkNotificationDeliverySucceeded(id int, leaseToken string, now time.Time) error {
	return d.updateNotificationDelivery(id, leaseToken, db.NotificationDeliverySucceeded, db.NotificationDeliveryReasonNone, now, now, true)
}

// MarkNotificationDeliverySucceededWithRecord records a lifecycle update's
// exact provider identity with its terminal delivery state in one transition.
func (d *SqlDb) MarkNotificationDeliverySucceededWithRecord(id int, leaseToken, recordID, recordURL string, now time.Time) error {
	if id <= 0 || leaseToken == "" || !notificationRecordIDPattern.MatchString(recordID) || len(recordURL) == 0 || len(recordURL) > 1024 {
		return db.ErrNotificationDeliveryNotClaimed
	}
	var origin string
	if err := d.selectOne(&origin, "select b.provider_origin from notification_incident_binding b join notification_delivery d on d.destination_id=b.destination_id and d.incident_key=b.incident_key where d.id=?", id); err != nil || !validNotificationIncidentRecordURL(origin, recordID, recordURL) {
		return db.ErrNotificationDeliveryNotClaimed
	}
	result, err := d.exec("update notification_delivery set provider_record_id=?, provider_record_url=?, status=?, attempts=attempts+1, next_attempt=?, lease_token='', lease_until=null, last_reason=?, updated=?, delivered_at=? where id=? and status=? and lease_token=?",
		recordID, recordURL, db.NotificationDeliverySucceeded, now, db.NotificationDeliveryReasonNone, now, now, id, db.NotificationDeliveryRunning, leaseToken)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrNotificationDeliveryNotClaimed
	}
	return nil
}

func (d *SqlDb) MarkNotificationDeliveryRetrying(id int, leaseToken string, reason db.NotificationDeliveryReason, nextAttempt, now time.Time) error {
	return d.updateNotificationDelivery(id, leaseToken, db.NotificationDeliveryRetrying, reason, nextAttempt, now, false)
}

func (d *SqlDb) MarkNotificationDeliveryFailed(id int, leaseToken string, reason db.NotificationDeliveryReason, now time.Time) error {
	return d.updateNotificationDelivery(id, leaseToken, db.NotificationDeliveryFailed, reason, now, now, false)
}

// ReleaseNotificationDelivery returns a claimed item to the durable queue
// without incrementing attempts. It is used only for administratively paused
// destinations: a pause is neither a provider attempt nor a failure.
func (d *SqlDb) ReleaseNotificationDelivery(id int, leaseToken string, reason db.NotificationDeliveryReason, nextAttempt, now time.Time) error {
	if id <= 0 || leaseToken == "" || !validNotificationDeliveryReason(reason) {
		return db.ErrNotificationDeliveryNotClaimed
	}
	result, err := d.exec("update notification_delivery set status=?, next_attempt=?, lease_token='', lease_until=null, last_reason=?, updated=? where id=? and status=? and lease_token=?",
		db.NotificationDeliveryRetrying, nextAttempt, reason, now, id, db.NotificationDeliveryRunning, leaseToken)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrNotificationDeliveryNotClaimed
	}
	return nil
}

// ResumePausedNotificationDeliveries makes only items deferred by an explicit
// pause immediately eligible again. It preserves retry schedules created by
// providers and never changes attempts or delivery identity.
func (d *SqlDb) ResumePausedNotificationDeliveries(destinationID int, now time.Time) error {
	if destinationID <= 0 {
		return db.ErrInvalidOperation
	}
	_, err := d.exec("update notification_delivery set next_attempt=?, last_reason=?, updated=? where destination_id=? and status=? and last_reason=?",
		now, db.NotificationDeliveryReasonNone, now, destinationID, db.NotificationDeliveryRetrying, db.NotificationDeliveryReasonDestinationPaused)
	return err
}

func (d *SqlDb) RetryNotificationDelivery(id, destinationID, configurationRevision int, now time.Time) error {
	// A manual retry starts a new bounded delivery budget for the same durable
	// event and idempotency key. Unlike resume, it is an explicit administrator
	// action and may deliberately bind a failed delivery to a corrected current
	// configuration, fenced by that destination generation at update time.
	if id <= 0 || destinationID <= 0 || configurationRevision <= 0 {
		return db.ErrNotificationDeliveryNotClaimed
	}
	result, err := d.exec("update notification_delivery set destination_configuration_revision=?, provider_request_id='', status=?, attempts=0, next_attempt=?, lease_token='', lease_until=null, last_reason=?, updated=? where id=? and status=? and destination_id=? and exists (select 1 from notification_destination where id=? and configuration_revision=?)",
		configurationRevision, db.NotificationDeliveryRetrying, now, db.NotificationDeliveryReasonManualRetry, now, id, db.NotificationDeliveryFailed, destinationID, destinationID, configurationRevision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrNotificationDeliveryNotClaimed
	}
	return nil
}

// StoreNotificationDeliveryPending fences the provider's asynchronous request
// ID to the current lease and makes the same bounded attempt budget cover polls.
func (d *SqlDb) StoreNotificationDeliveryPending(id int, leaseToken, requestID string, nextAttempt, now time.Time) error {
	if id <= 0 || leaseToken == "" || !pro_interfaces.ValidNotificationProviderRequestID(requestID) {
		return db.ErrNotificationDeliveryNotClaimed
	}
	result, err := d.exec("update notification_delivery set provider_request_id=?, status=?, attempts=attempts+1, next_attempt=?, lease_token='', lease_until=null, last_reason=?, updated=? where id=? and status=? and lease_token=? and (provider_request_id='' or provider_request_id=?)", requestID, db.NotificationDeliveryRetrying, nextAttempt, db.NotificationDeliveryReasonProviderPending, now, id, db.NotificationDeliveryRunning, leaseToken, requestID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrNotificationDeliveryNotClaimed
	}
	return nil
}

func (d *SqlDb) GetNotificationDeliveries(projectID *int, params db.RetrieveQueryParams) ([]db.NotificationDelivery, error) {
	query := squirrel.Select(
		"d.*", "e.event_id", "e.source_kind", "e.source_id", "e.lifecycle_action", "e.severity", "e.occurred_at",
	).From("notification_delivery d").Join("notification_event e on e.id=d.notification_event_id").Where(notificationEventScopePredicate(projectID)).OrderBy("d.id desc")
	if params.Count > 0 {
		query = query.Limit(uint64(params.Count))
	}
	if params.Offset > 0 {
		query = query.Offset(uint64(params.Offset))
	}
	statement, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}
	var deliveries []db.NotificationDelivery
	_, err = d.selectAll(&deliveries, statement, append(args, notificationScopeArgs(projectID)...)...)
	return deliveries, err
}

// GetNotificationEventHistory reads only the allow-listed event envelope. In
// particular, notification_event.details stays out of this projection so an
// administrator cannot turn history into a generic metadata disclosure path.
func (d *SqlDb) GetNotificationEventHistory(projectID *int, params db.RetrieveQueryParams) ([]db.NotificationEventHistory, error) {
	query := squirrel.Select(
		"event_id", "source_kind", "source_id", "lifecycle_id", "severity", "lifecycle_action",
		"incident_key", "routing_outcome", "occurred_at", "created",
	).From("notification_event").Where(notificationHistoryEventScopePredicate(projectID)).OrderBy("id desc")
	if params.Count > 0 {
		query = query.Limit(uint64(params.Count))
	}
	if params.Offset > 0 {
		query = query.Offset(uint64(params.Offset))
	}
	statement, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}
	var events []db.NotificationEventHistory
	_, err = d.selectAll(&events, statement, append(args, notificationScopeArgs(projectID)...)...)
	return events, err
}

// GetNotificationDelivery resolves a delivery through its immutable event
// scope. Callers use this before manual retry so an identifier from another
// project cannot be retried by guessing its numeric primary key.
func (d *SqlDb) GetNotificationDelivery(projectID *int, id int) (db.NotificationDelivery, error) {
	var delivery db.NotificationDelivery
	statement := "select d.*, e.event_id, e.source_kind, e.source_id, e.lifecycle_action, e.severity, e.occurred_at from notification_delivery d join notification_event e on e.id=d.notification_event_id where d.id=? and " + notificationEventScopePredicate(projectID)
	err := d.selectOne(&delivery, statement, append([]any{id}, notificationScopeArgs(projectID)...)...)
	return delivery, err
}

// GetNotificationDispatchContext resolves a claimed delivery through the
// event's immutable scope and the destination's current record. A deleted
// destination is deliberately reported as missing: history remains readable
// from its snapshot while the worker records a bounded terminal configuration
// outcome instead of dispatching stale configuration.
func (d *SqlDb) GetNotificationDispatchContext(id int) (db.NotificationDelivery, db.NotificationEvent, db.NotificationDestination, error) {
	if id <= 0 {
		return db.NotificationDelivery{}, db.NotificationEvent{}, db.NotificationDestination{}, db.ErrNotFound
	}
	var delivery db.NotificationDelivery
	if err := d.selectOne(&delivery, "select d.*, e.event_id from notification_delivery d join notification_event e on e.id=d.notification_event_id where d.id=?", id); err != nil {
		return db.NotificationDelivery{}, db.NotificationEvent{}, db.NotificationDestination{}, err
	}
	var event db.NotificationEvent
	if err := d.selectOne(&event, "select * from notification_event where id=?", delivery.NotificationEventID); err != nil {
		return db.NotificationDelivery{}, db.NotificationEvent{}, db.NotificationDestination{}, err
	}
	destination, err := d.GetNotificationDestination(event.ProjectID, delivery.DestinationID)
	if err != nil {
		return db.NotificationDelivery{}, db.NotificationEvent{}, db.NotificationDestination{}, err
	}
	return delivery, event, destination, nil
}

// ReserveNotificationIncidentBinding creates the durable, lifecycle-wide
// create fence before a provider can issue a non-idempotent POST. A competing
// worker receives the existing row and therefore cannot create a second record.
func (d *SqlDb) ReserveNotificationIncidentBinding(binding db.NotificationIncidentBinding, leaseUntil time.Time) (db.NotificationIncidentBinding, error) {
	if binding.DestinationID <= 0 || len(binding.IncidentKey) != 64 || len(binding.CorrelationID) != 64 ||
		binding.Provider == "" || binding.ProviderOrigin == "" || binding.ConfigurationRevision <= 0 || !leaseUntil.After(tz.Now()) {
		return db.NotificationIncidentBinding{}, db.ErrInvalidOperation
	}
	if existing, err := d.GetNotificationIncidentBinding(binding.DestinationID, binding.IncidentKey); err == nil {
		if existing.State == db.NotificationIncidentBindingReservedNoPost && existing.LeaseUntil != nil && !existing.LeaseUntil.After(tz.Now()) {
			token, tokenErr := notificationLeaseToken()
			if tokenErr != nil {
				return db.NotificationIncidentBinding{}, tokenErr
			}
			result, updateErr := d.exec("update notification_incident_binding set owner_delivery_id=?, lease_token=?, lease_until=?, updated=? where id=? and state=? and lease_until<=?", binding.OwnerDeliveryID, token, leaseUntil, tz.Now(), existing.ID, db.NotificationIncidentBindingReservedNoPost, tz.Now())
			if updateErr != nil {
				return db.NotificationIncidentBinding{}, updateErr
			}
			if rows, rowsErr := result.RowsAffected(); rowsErr != nil {
				return db.NotificationIncidentBinding{}, rowsErr
			} else if rows == 1 {
				return d.GetNotificationIncidentBinding(binding.DestinationID, binding.IncidentKey)
			}
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, db.ErrNotFound) {
		return db.NotificationIncidentBinding{}, err
	}
	token, err := notificationLeaseToken()
	if err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	now := tz.Now()
	binding.State = db.NotificationIncidentBindingReservedNoPost
	binding.LeaseToken = token
	binding.LeaseUntil = &leaseUntil
	binding.LastReason = db.NotificationDeliveryReasonNone
	binding.Created = now
	binding.Updated = now
	if err = d.Sql().Insert(&binding); err != nil {
		// A unique-key collision is the expected concurrent path. Re-read the
		// authoritative binding rather than deciding from a driver-specific error.
		return d.GetNotificationIncidentBinding(binding.DestinationID, binding.IncidentKey)
	}
	return binding, nil
}

func (d *SqlDb) GetNotificationIncidentBinding(destinationID int, incidentKey string) (db.NotificationIncidentBinding, error) {
	if destinationID <= 0 || len(incidentKey) != 64 {
		return db.NotificationIncidentBinding{}, db.ErrNotFound
	}
	var binding db.NotificationIncidentBinding
	err := d.selectOne(&binding, "select * from notification_incident_binding where destination_id=? and incident_key=?", destinationID, incidentKey)
	return binding, err
}

// MarkNotificationIncidentPostStarted makes the ambiguity fence durable before
// the HTTP client is permitted to dispatch the one create request.
func (d *SqlDb) MarkNotificationIncidentPostStarted(id int, leaseToken string, now time.Time) (db.NotificationIncidentBinding, error) {
	if id <= 0 || leaseToken == "" {
		return db.NotificationIncidentBinding{}, db.ErrNotificationDeliveryNotClaimed
	}
	result, err := d.exec("update notification_incident_binding set state=?, updated=? where id=? and state=? and lease_token=?",
		db.NotificationIncidentBindingPostStarted, now, id, db.NotificationIncidentBindingReservedNoPost, leaseToken)
	if err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return db.NotificationIncidentBinding{}, db.ErrNotificationDeliveryNotClaimed
	}
	var binding db.NotificationIncidentBinding
	if err = d.selectOne(&binding, "select * from notification_incident_binding where id=?", id); err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	return binding, nil
}

func (d *SqlDb) BindNotificationIncident(id int, leaseToken, recordID, recordURL string, now time.Time) (db.NotificationIncidentBinding, error) {
	if id <= 0 || leaseToken == "" || !notificationRecordIDPattern.MatchString(recordID) || len(recordURL) == 0 || len(recordURL) > 1024 {
		return db.NotificationIncidentBinding{}, db.ErrInvalidOperation
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var before db.NotificationIncidentBinding
	if err = tx.SelectOne(&before, d.PrepareQuery("select * from notification_incident_binding where id=?"), id); err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	if !validNotificationIncidentRecordURL(before.ProviderOrigin, recordID, recordURL) {
		return db.NotificationIncidentBinding{}, db.ErrInvalidOperation
	}
	result, err := d.execTx(tx, "update notification_incident_binding set state=?, provider_record_id=?, provider_record_url=?, lease_token='', lease_until=null, last_reason='', updated=? where id=? and state in (?, ?, ?) and lease_token=?",
		db.NotificationIncidentBindingBound, recordID, recordURL, now, id, db.NotificationIncidentBindingReservedNoPost, db.NotificationIncidentBindingPostStarted, db.NotificationIncidentBindingAmbiguous, leaseToken)
	if err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return db.NotificationIncidentBinding{}, db.ErrNotificationDeliveryNotClaimed
	}
	if _, err = d.execTx(tx, "update notification_delivery set provider_record_id=?, provider_record_url=?, updated=? where destination_id=? and incident_key=?", recordID, recordURL, now, before.DestinationID, before.IncidentKey); err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	var binding db.NotificationIncidentBinding
	if err = d.selectOne(&binding, "select * from notification_incident_binding where id=?", id); err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	return binding, nil
}

// BindNotificationIncidentAndSucceed commits a successful create as one
// atomic transition: binding identity, all lifecycle history metadata, and the
// owner delivery's terminal success can never become externally inconsistent.
func (d *SqlDb) BindNotificationIncidentAndSucceed(id int, leaseToken, recordID, recordURL string, deliveryID int, deliveryLeaseToken string, now time.Time) (db.NotificationIncidentBinding, error) {
	if id <= 0 || leaseToken == "" || deliveryID <= 0 || deliveryLeaseToken == "" || !notificationRecordIDPattern.MatchString(recordID) || len(recordURL) == 0 || len(recordURL) > 1024 {
		return db.NotificationIncidentBinding{}, db.ErrInvalidOperation
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var before db.NotificationIncidentBinding
	if err = tx.SelectOne(&before, d.PrepareQuery("select * from notification_incident_binding where id=?"), id); err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	if !validNotificationIncidentRecordURL(before.ProviderOrigin, recordID, recordURL) {
		return db.NotificationIncidentBinding{}, db.ErrInvalidOperation
	}
	result, err := d.execTx(tx, "update notification_incident_binding set state=?, provider_record_id=?, provider_record_url=?, lease_token='', lease_until=null, last_reason='', updated=? where id=? and state in (?, ?, ?) and lease_token=?",
		db.NotificationIncidentBindingBound, recordID, recordURL, now, id, db.NotificationIncidentBindingReservedNoPost, db.NotificationIncidentBindingPostStarted, db.NotificationIncidentBindingAmbiguous, leaseToken)
	if err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return db.NotificationIncidentBinding{}, db.ErrNotificationDeliveryNotClaimed
	}
	if _, err = d.execTx(tx, "update notification_delivery set provider_record_id=?, provider_record_url=?, updated=? where destination_id=? and incident_key=?", recordID, recordURL, now, before.DestinationID, before.IncidentKey); err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	result, err = d.execTx(tx, "update notification_delivery set status=?, attempts=attempts+1, next_attempt=?, lease_token='', lease_until=null, last_reason=?, updated=?, delivered_at=? where id=? and status=? and lease_token=?",
		db.NotificationDeliverySucceeded, now, db.NotificationDeliveryReasonNone, now, now, deliveryID, db.NotificationDeliveryRunning, deliveryLeaseToken)
	if err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	rows, err = result.RowsAffected()
	if err != nil || rows != 1 {
		return db.NotificationIncidentBinding{}, db.ErrNotificationDeliveryNotClaimed
	}
	if err = tx.Commit(); err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	var binding db.NotificationIncidentBinding
	if err = d.selectOne(&binding, "select * from notification_incident_binding where id=?", id); err != nil {
		return db.NotificationIncidentBinding{}, err
	}
	return binding, nil
}

func (d *SqlDb) updateNotificationDelivery(id int, leaseToken string, status db.NotificationDeliveryStatus, reason db.NotificationDeliveryReason, nextAttempt, now time.Time, delivered bool) error {
	if id <= 0 || leaseToken == "" || !validNotificationDeliveryReason(reason) {
		return db.ErrNotificationDeliveryNotClaimed
	}
	var deliveredAt *time.Time
	if delivered {
		deliveredAt = &now
	}
	result, err := d.exec("update notification_delivery set status=?, attempts=attempts+1, next_attempt=?, lease_token='', lease_until=null, last_reason=?, updated=?, delivered_at=? where id=? and status=? and lease_token=?",
		status, nextAttempt, reason, now, deliveredAt, id, db.NotificationDeliveryRunning, leaseToken)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrNotificationDeliveryNotClaimed
	}
	return nil
}

func (d *SqlDb) validateNotificationRule(rule db.NotificationRule) error {
	if rule.DestinationID <= 0 || !validNotificationEnumList(rule.SourceKinds, "task", "workflow", "approval", "system") ||
		!validNotificationEnumList(rule.LifecycleActions, "trigger", "update", "resolve") ||
		!validNotificationSeverity(rule.MinimumSeverity) {
		return db.ErrInvalidOperation
	}
	destination, err := d.GetNotificationDestination(rule.ProjectID, rule.DestinationID)
	if err != nil || destination.ProjectID == nil != (rule.ProjectID == nil) {
		return db.ErrInvalidOperation
	}
	return nil
}

func validateNotificationDestination(destination db.NotificationDestination) error {
	if destination.Name == "" || len(destination.Name) > 128 || !notificationProviderPattern.MatchString(destination.Provider) ||
		len(destination.Environment) > 64 || len(destination.Region) > 8 || len(destination.ProviderConfig) > 16*1024 || len(destination.EncryptedCredential) > 16*1024 || destination.Revision < 0 {
		return db.ErrInvalidOperation
	}
	if destination.ProjectID != nil && *destination.ProjectID <= 0 {
		return db.ErrInvalidOperation
	}
	if destination.CredentialConfigured != (destination.EncryptedCredential != "") {
		return db.ErrInvalidOperation
	}
	return nil
}

func validateNotificationRouting(event db.NotificationEvent, deliveries []db.NotificationDelivery) error {
	if event.SchemaVersion != "semaphore.notification.v1" || !notificationEventIDPattern.MatchString(event.EventID) || !notificationIncidentKeyPattern.MatchString(event.SourceEventKey) || event.SourceRevision < 1 || !validNotificationSource(event.SourceKind, event.SourceID) || !validNotificationLifecycle(event.SourceKind, event.LifecycleID) ||
		!validNotificationSeverity(event.Severity) || !validNotificationLifecycleAction(event.LifecycleAction) ||
		!notificationIncidentKeyPattern.MatchString(event.IncidentKey) || event.OccurredAt.IsZero() || event.OccurredAt.Location() != time.UTC || !validNotificationDetails(event.SourceKind, event.SourceID, event.LifecycleID, event.Details) {
		return db.ErrInvalidOperation
	}
	if event.ProjectID != nil && *event.ProjectID <= 0 {
		return db.ErrInvalidOperation
	}
	incidentKey := notificationIncidentKey(event.ProjectID, event.LifecycleID)
	if event.IncidentKey != incidentKey || event.SourceEventKey != notificationSourceEventKey(event.ProjectID, event.SourceKind, event.SourceID, event.SourceRevision) {
		return db.ErrInvalidOperation
	}
	if event.RoutingOutcome == db.NotificationRoutingFiltered {
		if len(deliveries) != 0 {
			return db.ErrInvalidOperation
		}
		return nil
	}
	if event.RoutingOutcome != db.NotificationRoutingRouted || len(deliveries) == 0 {
		return db.ErrInvalidOperation
	}
	seen := make(map[int]struct{}, len(deliveries))
	for _, delivery := range deliveries {
		if delivery.DestinationID <= 0 || delivery.DestinationRevision <= 0 || delivery.DestinationConfigurationRevision <= 0 || delivery.DestinationName == "" || len(delivery.DestinationName) > 128 ||
			!notificationProviderPattern.MatchString(delivery.DestinationProvider) || len(delivery.DestinationEnvironment) > 64 || len(delivery.DestinationRegion) > 8 {
			return db.ErrInvalidOperation
		}
		if _, exists := seen[delivery.DestinationID]; exists {
			return db.ErrInvalidOperation
		}
		seen[delivery.DestinationID] = struct{}{}
	}
	return nil
}

func sameNotificationTransition(left, right db.NotificationEvent) bool {
	if left.SourceEventKey != right.SourceEventKey || left.SourceRevision != right.SourceRevision || left.SourceKind != right.SourceKind ||
		left.SourceID != right.SourceID || left.Severity != right.Severity || left.LifecycleAction != right.LifecycleAction ||
		left.IncidentKey != right.IncidentKey || left.Details != right.Details || left.RoutingOutcome != right.RoutingOutcome {
		return false
	}
	if left.ProjectID == nil || right.ProjectID == nil {
		return left.ProjectID == nil && right.ProjectID == nil
	}
	return *left.ProjectID == *right.ProjectID
}

func notificationDestinationScopeQuery(where string, projectID *int) string {
	return "select * from notification_destination where " + where + " and " + notificationScopePredicate(projectID)
}

func notificationRuleScopeQuery(where string, projectID *int) string {
	return "select * from notification_rule where " + where + " and " + notificationScopePredicate(projectID)
}

func notificationScopePredicate(projectID *int) string {
	if projectID == nil {
		return "project_id is null"
	}
	return "project_id=?"
}

func notificationEventScopePredicate(projectID *int) string {
	if projectID == nil {
		return "e.project_id is null"
	}
	return "e.project_id=?"
}

func notificationHistoryEventScopePredicate(projectID *int) string {
	if projectID == nil {
		return "project_id is null"
	}
	return "project_id=?"
}

func notificationScopeArgs(projectID *int) []any {
	if projectID == nil {
		return nil
	}
	return []any{*projectID}
}

func notificationDeliveryKey(eventID string, destinationID int) (string, error) {
	if !notificationEventIDPattern.MatchString(eventID) || destinationID <= 0 {
		return "", db.ErrInvalidOperation
	}
	return fmt.Sprintf("%x", sha256Sum(eventID, destinationID)), nil
}

func sha256Sum(eventID string, destinationID int) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%s|%d", eventID, destinationID)))
}

func notificationIncidentKey(projectID *int, lifecycleID string) string {
	scope, project := "global", ""
	if projectID != nil {
		scope = "project"
		project = fmt.Sprintf("%d", *projectID)
	}
	sum := sha256.Sum256([]byte(scope + "|" + project + "|" + lifecycleID))
	return hex.EncodeToString(sum[:])
}

func notificationSourceEventKey(projectID *int, sourceKind, sourceID string, sourceRevision int) string {
	scope, project := "global", ""
	if projectID != nil {
		scope = "project"
		project = fmt.Sprintf("%d", *projectID)
	}
	sum := sha256.Sum256([]byte(scope + "|" + project + "|" + sourceKind + "|" + sourceID + "|" + fmt.Sprintf("%d", sourceRevision)))
	return hex.EncodeToString(sum[:])
}

func notificationLeaseToken() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}

// validNotificationIncidentRecordURL accepts only the fixed record URL the
// dispatcher derives from the durable binding origin; callers cannot persist a
// provider-controlled redirect or arbitrary history link.
func validNotificationIncidentRecordURL(origin, recordID, recordURL string) bool {
	if !notificationRecordIDPattern.MatchString(recordID) || len(recordURL) == 0 || len(recordURL) > 1024 {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.Hostname() == "" || parsed.Port() != "" {
		return false
	}
	expected := "https://" + strings.ToLower(strings.TrimSuffix(parsed.Hostname(), ".")) + "/incident.do?sys_id=" + url.QueryEscape(recordID)
	return recordURL == expected
}

func validNotificationDeliveryReason(reason db.NotificationDeliveryReason) bool {
	switch reason {
	case db.NotificationDeliveryReasonNone, db.NotificationDeliveryReasonConfiguration, db.NotificationDeliveryReasonRateLimited,
		db.NotificationDeliveryReasonTransport, db.NotificationDeliveryReasonAttemptsExhausted, db.NotificationDeliveryReasonManualRetry,
		db.NotificationDeliveryReasonDestinationPaused, db.NotificationDeliveryReasonDestinationMissing,
		db.NotificationDeliveryReasonDestinationDisabled, db.NotificationDeliveryReasonDestinationChanged,
		db.NotificationDeliveryReasonCredentialUnavailable, db.NotificationDeliveryReasonProviderUnavailable,
		db.NotificationDeliveryReasonPermanent, db.NotificationDeliveryReasonProviderPending, db.NotificationDeliveryReasonProviderAmbiguous:
		return true
	default:
		return false
	}
}

func isUniqueConstraintError(err error) bool {
	return err != nil && (errors.Is(err, sql.ErrNoRows) || containsFold(err.Error(), "unique"))
}

var (
	notificationEventIDPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
	notificationIncidentKeyPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	notificationRecordIDPattern    = regexp.MustCompile(`^[a-f0-9]{32}$`)
	notificationSourceIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*:[a-z0-9][a-z0-9_.:-]{0,127}$`)
	notificationProviderPattern    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	notificationDetailPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,63}$`)
)

func validNotificationSource(kind, id string) bool {
	if !notificationSourceIDPattern.MatchString(id) {
		return false
	}
	return (kind == "task" && strings.HasPrefix(id, "task:")) ||
		(kind == "workflow" && strings.HasPrefix(id, "workflow_run:")) ||
		(kind == "approval" && strings.HasPrefix(id, "approval:")) ||
		(kind == "system" && strings.HasPrefix(id, "system:"))
}

func validNotificationLifecycle(kind, id string) bool {
	if !notificationSourceIDPattern.MatchString(id) {
		return false
	}
	return (kind == "task" && strings.HasPrefix(id, "template:")) ||
		(kind == "workflow" && strings.HasPrefix(id, "workflow:")) ||
		(kind == "approval" && strings.HasPrefix(id, "approval:")) ||
		(kind == "system" && strings.HasPrefix(id, "system:"))
}

func validNotificationSeverity(value string) bool {
	return value == "info" || value == "warning" || value == "error" || value == "critical"
}

func validNotificationLifecycleAction(value string) bool {
	return value == "trigger" || value == "update" || value == "resolve"
}

func validNotificationDetails(sourceKind, sourceID, lifecycleID, value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	var details struct {
		TaskID        *int   `json:"task_id,omitempty"`
		TemplateID    *int   `json:"template_id,omitempty"`
		WorkflowID    *int   `json:"workflow_id,omitempty"`
		WorkflowRunID *int   `json:"workflow_run_id,omitempty"`
		ApprovalID    *int   `json:"approval_id,omitempty"`
		Status        string `json:"status,omitempty"`
		Decision      string `json:"decision,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&details); err != nil {
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return false
	}
	if (details.Status != "" && !notificationDetailPattern.MatchString(details.Status)) ||
		(details.Decision != "" && !notificationDetailPattern.MatchString(details.Decision)) {
		return false
	}
	switch sourceKind {
	case "task":
		return details.TaskID != nil && details.TemplateID != nil && *details.TaskID > 0 && *details.TemplateID > 0 &&
			matchesNotificationNumericIdentifier(sourceID, "task:", *details.TaskID) && matchesNotificationNumericIdentifier(lifecycleID, "template:", *details.TemplateID) &&
			details.WorkflowID == nil && details.WorkflowRunID == nil && details.ApprovalID == nil
	case "workflow":
		return details.WorkflowID != nil && details.WorkflowRunID != nil && *details.WorkflowID > 0 && *details.WorkflowRunID > 0 &&
			matchesNotificationNumericIdentifier(sourceID, "workflow_run:", *details.WorkflowRunID) && matchesNotificationNumericIdentifier(lifecycleID, "workflow:", *details.WorkflowID) &&
			details.TaskID == nil && details.TemplateID == nil && details.ApprovalID == nil
	case "approval":
		return details.WorkflowID != nil && details.WorkflowRunID != nil && details.ApprovalID != nil && *details.WorkflowID > 0 && *details.WorkflowRunID > 0 && *details.ApprovalID > 0 &&
			matchesNotificationNumericIdentifier(sourceID, "approval:", *details.ApprovalID) && matchesNotificationNumericIdentifier(lifecycleID, "approval:", *details.ApprovalID) &&
			details.TaskID == nil && details.TemplateID == nil
	case "system":
		return details.TaskID == nil && details.TemplateID == nil && details.WorkflowID == nil && details.WorkflowRunID == nil && details.ApprovalID == nil
	default:
		return false
	}
}

func matchesNotificationNumericIdentifier(value, prefix string, expected int) bool {
	parsed, err := strconv.Atoi(strings.TrimPrefix(value, prefix))
	return err == nil && parsed == expected
}

func validNotificationEnumList(value string, allowed ...string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	seen := make(map[string]struct{})
	for _, entry := range strings.Split(value, ",") {
		if entry == "" || !contains(allowed, entry) {
			return false
		}
		if _, duplicate := seen[entry]; duplicate {
			return false
		}
		seen[entry] = struct{}{}
	}
	return true
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func containsFold(value, fragment string) bool {
	return len(value) >= len(fragment) && (value == fragment || strings.Contains(strings.ToLower(value), strings.ToLower(fragment)))
}

var _ db.NotificationRepository = (*SqlDb)(nil)
