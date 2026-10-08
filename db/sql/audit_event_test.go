package sql

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func selectAuditEvents(t *testing.T, store *SqlDb) []db.AuditEvent {
	t.Helper()
	var rows []db.AuditEvent
	_, err := store.Sql().Select(&rows, "select * from audit_event order by seq")
	require.NoError(t, err)
	return rows
}

func fullAuditEvent() db.AuditEvent {
	projectID := 12
	return db.AuditEvent{
		EventID:               "7b0c0000-0000-4000-8000-000000000001",
		SchemaVersion:         "1",
		Category:              "iam",
		EventCode:             "iam.api_token",
		Type:                  "creation",
		Action:                "create",
		Outcome:               "success",
		Reason:                "",
		ActorType:             "user",
		ActorID:               "7",
		ActorName:             "alice",
		ActorAuth:             "api_token",
		ActorTokenFingerprint: "3f9a0c1d2e4b5a69",
		SourceIP:              "10.0.0.5",
		UserAgent:             "curl/8.5",
		TargetType:            "api_token",
		TargetID:              "3f9a0c1d2e4b5a69",
		TargetName:            "ci-token",
		ProjectID:             &projectID,
		RequestID:             "req-1",
		InstanceID:            "prod-eu",
		NodeID:                "node-a",
		Metadata:              `{"fields":["name"]}`,
	}
}

func TestAuditTablesAfterMigration(t *testing.T) {
	store := InitConfigCreateTestStore()

	lastSeq, err := store.Sql().SelectInt("select last_seq from audit_event_sequence where id = 1")
	require.NoError(t, err)
	assert.Zero(t, lastSeq)

	count, err := store.Sql().SelectInt("select count(*) from audit_export_state")
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestCreateAuditEvent_RoundTripsEveryColumn(t *testing.T) {
	store := InitConfigCreateTestStore()
	event := fullAuditEvent()

	stored, err := store.CreateAuditEvent(context.Background(), event)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stored.Seq)

	rows := selectAuditEvents(t, store)
	require.Len(t, rows, 1)
	assert.WithinDuration(t, time.Now(), stored.Created, time.Minute)
	assert.True(t, stored.Created.Equal(rows[0].Created))
	event.Seq = 1
	event.Created = stored.Created
	rows[0].Created = stored.Created
	assert.Equal(t, event, rows[0])
}

func TestCreateAuditEvent_NullProject(t *testing.T) {
	store := InitConfigCreateTestStore()
	event := fullAuditEvent()
	event.ProjectID = nil

	_, err := store.CreateAuditEvent(context.Background(), event)
	require.NoError(t, err)
	assert.Nil(t, selectAuditEvents(t, store)[0].ProjectID)
}

func TestCreateAuditEvent_RejectsInvalidMetadataWithoutAdvancingSequence(t *testing.T) {
	store := InitConfigCreateTestStore()
	event := fullAuditEvent()
	event.Metadata = `{"incomplete":`

	_, err := store.CreateAuditEvent(context.Background(), event)

	assert.ErrorContains(t, err, "metadata")
	assert.Empty(t, selectAuditEvents(t, store))
	lastSeq, err := store.Sql().SelectInt("select last_seq from audit_event_sequence where id = 1")
	require.NoError(t, err)
	assert.Zero(t, lastSeq)
}

func TestCreateAuditEvent_GapFreeUnderConcurrency(t *testing.T) {
	store := InitConfigCreateTestStore()
	const n = 20

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			event := fullAuditEvent()
			event.EventID = fmt.Sprintf("id-%d", i)
			_, err := store.CreateAuditEvent(context.Background(), event)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	rows := selectAuditEvents(t, store)
	require.Len(t, rows, n)
	for i, row := range rows {
		assert.Equal(t, int64(i+1), row.Seq)
	}
}

func TestCreateAuditEvent_CanceledContextStoresNothing(t *testing.T) {
	store := InitConfigCreateTestStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.CreateAuditEvent(ctx, fullAuditEvent())

	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, selectAuditEvents(t, store))
	lastSeq, err := store.Sql().SelectInt("select last_seq from audit_event_sequence where id = 1")
	require.NoError(t, err)
	assert.Zero(t, lastSeq)
}

func TestAuditExportStateStartsAtCurrentSequenceAndUsesCompareAndSwap(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()

	first, err := store.CreateAuditEvent(context.Background(), fullAuditEvent())
	require.NoError(t, err)
	cursor, err := store.InitializeAuditExportState(context.Background(), "siem-primary")
	require.NoError(t, err)
	assert.Equal(t, first.Seq, cursor)

	secondEvent := fullAuditEvent()
	secondEvent.EventID = "7b0c0000-0000-4000-8000-000000000002"
	second, err := store.CreateAuditEvent(context.Background(), secondEvent)
	require.NoError(t, err)
	pending, err := store.GetAuditEventsAfter(context.Background(), cursor, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, second.Seq, pending[0].Seq)

	advanced, err := store.AdvanceAuditExportState(context.Background(), "siem-primary", cursor, second.Seq)
	require.NoError(t, err)
	assert.True(t, advanced)
	advanced, err = store.AdvanceAuditExportState(context.Background(), "siem-primary", cursor, second.Seq)
	require.NoError(t, err)
	assert.False(t, advanced)

	resumedCursor, err := store.InitializeAuditExportState(context.Background(), "siem-primary")
	require.NoError(t, err)
	assert.Equal(t, second.Seq, resumedCursor)
}

func TestGetAuditExportBacklogCountsEveryPendingEvent(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()

	first, err := store.CreateAuditEvent(context.Background(), fullAuditEvent())
	require.NoError(t, err)
	_, err = store.InitializeAuditExportState(context.Background(), "siem-primary")
	require.NoError(t, err)

	const pendingEvents = 105
	var last db.AuditEvent
	for i := 0; i < pendingEvents; i++ {
		event := fullAuditEvent()
		event.EventID = fmt.Sprintf("7b0c0000-0000-4000-8000-%012d", i+2)
		last, err = store.CreateAuditEvent(context.Background(), event)
		require.NoError(t, err)
	}
	oldest := time.Now().UTC().Add(-2 * time.Minute)
	_, err = store.Sql().Exec(store.PrepareQuery("update audit_event set created = ? where seq = ?"), oldest, last.Seq)
	require.NoError(t, err)

	backlog, err := store.GetAuditExportBacklog(context.Background(), "siem-primary")

	require.NoError(t, err)
	assert.Equal(t, int64(pendingEvents), backlog.PendingEvents)
	assert.GreaterOrEqual(t, backlog.OldestPendingAge, 2*time.Minute-time.Second)

	advanced, err := store.AdvanceAuditExportState(context.Background(), "siem-primary", first.Seq, last.Seq)
	require.NoError(t, err)
	require.True(t, advanced)
	backlog, err = store.GetAuditExportBacklog(context.Background(), "siem-primary")
	require.NoError(t, err)
	assert.Zero(t, backlog.PendingEvents)
	assert.Zero(t, backlog.OldestPendingAge)
}

func TestParseAuditTimestampSupportsDatabaseAggregateRepresentations(t *testing.T) {
	for _, value := range []string{
		"2026-10-08 06:27:11.436733 +0000 UTC",
		"2026-10-08T06:27:11.436733Z",
		"2026-10-08 06:27:11.436733+00:00",
		"2026-10-08 06:27:11.436733",
	} {
		t.Run(value, func(t *testing.T) {
			parsed, err := parseAuditTimestamp(value)
			require.NoError(t, err)
			assert.Equal(t, time.UTC, parsed.Location())
		})
	}
}

func TestCreateAuditEvent_TimesOutWaitingForAConnection(t *testing.T) {
	store := InitConfigCreateTestStore()
	// SQLite has one connection, and this transaction holds it.
	busy, err := store.Sql().Begin()
	require.NoError(t, err)
	defer func() { _ = busy.Rollback() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := store.CreateAuditEvent(ctx, fullAuditEvent())
		done <- err
	}()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		_ = busy.Rollback()
		<-done
		t.Fatal("CreateAuditEvent ignored its context while waiting for a connection")
	}
}

func createAgedAuditEvents(t *testing.T, store *SqlDb, n int, created time.Time) []int64 {
	t.Helper()
	seqs := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		event := fullAuditEvent()
		event.EventID = fmt.Sprintf("aged-%d-%d", created.Unix(), i)
		row, err := store.CreateAuditEvent(context.Background(), event)
		require.NoError(t, err)
		_, err = store.Sql().Exec(store.PrepareQuery("update audit_event set created = ? where seq = ?"), created.UTC(), row.Seq)
		require.NoError(t, err)
		seqs = append(seqs, row.Seq)
	}
	return seqs
}

func TestDeleteAuditEventsOlderThan(t *testing.T) {
	store := InitConfigCreateTestStore()
	now := time.Now().UTC()
	old := createAgedAuditEvents(t, store, 5, now.AddDate(0, 0, -40))
	recent := createAgedAuditEvents(t, store, 2, now.AddDate(0, 0, -1))

	deleted, lastSeq, err := store.DeleteAuditEventsOlderThan(context.Background(), 30, 2)

	require.NoError(t, err)
	assert.Equal(t, int64(5), deleted)
	assert.Equal(t, old[len(old)-1], lastSeq)
	rows := selectAuditEvents(t, store)
	require.Len(t, rows, 2)
	assert.Equal(t, recent[0], rows[0].Seq)
}

func TestDeleteAuditEventsOlderThan_KeepsNewerEventBehindAClockStep(t *testing.T) {
	store := InitConfigCreateTestStore()
	now := time.Now().UTC()
	createAgedAuditEvents(t, store, 2, now.AddDate(0, 0, -40))
	recent := createAgedAuditEvents(t, store, 1, now.AddDate(0, 0, -1))
	createAgedAuditEvents(t, store, 1, now.AddDate(0, 0, -40))

	deleted, _, err := store.DeleteAuditEventsOlderThan(context.Background(), 30, 1000)

	require.NoError(t, err)
	assert.Equal(t, int64(3), deleted)
	rows := selectAuditEvents(t, store)
	require.Len(t, rows, 1)
	assert.Equal(t, recent[0], rows[0].Seq)
}

func TestDeleteAuditEventsOlderThan_NothingOld(t *testing.T) {
	store := InitConfigCreateTestStore()
	createAgedAuditEvents(t, store, 2, time.Now().UTC())

	deleted, lastSeq, err := store.DeleteAuditEventsOlderThan(context.Background(), 30, 1000)

	require.NoError(t, err)
	assert.Zero(t, deleted)
	assert.Zero(t, lastSeq)
	assert.Len(t, selectAuditEvents(t, store), 2)
}

func TestDeleteAuditEventsOlderThan_EmptyTable(t *testing.T) {
	store := InitConfigCreateTestStore()

	deleted, lastSeq, err := store.DeleteAuditEventsOlderThan(context.Background(), 0, 1000)

	require.NoError(t, err)
	assert.Zero(t, deleted)
	assert.Zero(t, lastSeq)
}

func TestDeleteAuditEventsOlderThan_KeepsCursors(t *testing.T) {
	store := InitConfigCreateTestStore()
	old := createAgedAuditEvents(t, store, 3, time.Now().UTC().AddDate(0, 0, -40))
	_, err := store.Sql().Exec(store.PrepareQuery("insert into audit_export_state (destination_id, cursor_seq) values (?, ?)"), "siem", old[0])
	require.NoError(t, err)

	deleted, _, err := store.DeleteAuditEventsOlderThan(context.Background(), 30, 1000)

	require.NoError(t, err)
	assert.Equal(t, int64(3), deleted)
	cursor, err := store.Sql().SelectInt(store.PrepareQuery("select cursor_seq from audit_export_state where destination_id = ?"), "siem")
	require.NoError(t, err)
	assert.Equal(t, old[0], cursor)
}

func TestDeleteAuditEventsOlderThan_BatchOfOne(t *testing.T) {
	store := InitConfigCreateTestStore()
	old := createAgedAuditEvents(t, store, 3, time.Now().UTC().AddDate(0, 0, -40))

	deleted, lastSeq, err := store.DeleteAuditEventsOlderThan(context.Background(), 30, 1)

	require.NoError(t, err)
	assert.Equal(t, int64(3), deleted)
	assert.Equal(t, old[2], lastSeq)
}

func TestDeleteAuditEventsOlderThan_NonPositiveBatch(t *testing.T) {
	store := InitConfigCreateTestStore()
	old := createAgedAuditEvents(t, store, 2, time.Now().UTC().AddDate(0, 0, -40))

	deleted, lastSeq, err := store.DeleteAuditEventsOlderThan(context.Background(), 30, 0)

	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)
	assert.Equal(t, old[1], lastSeq)
}

func TestDeleteAuditEventsOlderThan_CancelledContext(t *testing.T) {
	store := InitConfigCreateTestStore()
	createAgedAuditEvents(t, store, 2, time.Now().UTC().AddDate(0, 0, -40))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	deleted, lastSeq, err := store.DeleteAuditEventsOlderThan(ctx, 30, 1)

	require.Error(t, err)
	assert.Zero(t, deleted)
	assert.Zero(t, lastSeq)
	assert.Len(t, selectAuditEvents(t, store), 2)
}
