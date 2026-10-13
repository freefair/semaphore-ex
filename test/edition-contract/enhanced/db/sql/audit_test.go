package sql

import (
	"context"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditEventRepository_FiltersUsersWithoutMatchingNonUsers(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID := 3
	for _, event := range []db.AuditEvent{
		{EventID: "user", SchemaVersion: "1", Category: "auth", EventCode: "auth.login", Type: "start", Action: "authenticate", Outcome: "success", ActorType: "user", ActorID: "7", ProjectID: &projectID, InstanceID: "test", Metadata: "{}"},
		{EventID: "runner", SchemaVersion: "1", Category: "runner", EventCode: "runner.lifecycle", Type: "start", Action: "create", Outcome: "success", ActorType: "runner", ActorID: "7", ProjectID: &projectID, InstanceID: "test", Metadata: "{}"},
	} {
		_, err := store.CreateAuditEvent(context.Background(), event)
		require.NoError(t, err)
	}

	userID := 7
	page, err := NewAuditEventRepository(store).List(context.Background(), AuditFilter{User: &userID}, 50)

	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	assert.Equal(t, "user", page.Events[0].EventID)
}

func TestAuditEventRepository_RejectsCanceledQueries(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewAuditEventRepository(store).List(ctx, AuditFilter{}, 50)

	assert.Error(t, err)
}

func TestAuditEventRepository_UsesCursorAndProjectFilter(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectOne, projectTwo := 1, 2
	for _, event := range []db.AuditEvent{
		{EventID: "one", SchemaVersion: "1", Category: "auth", EventCode: "auth.login", Type: "start", Action: "authenticate", Outcome: "success", ActorType: "user", ActorID: "1", ProjectID: &projectOne, InstanceID: "test", Metadata: "{}"},
		{EventID: "two", SchemaVersion: "1", Category: "auth", EventCode: "auth.login", Type: "start", Action: "authenticate", Outcome: "success", ActorType: "user", ActorID: "1", ProjectID: &projectTwo, InstanceID: "test", Metadata: "{}"},
	} {
		_, err := store.CreateAuditEvent(context.Background(), event)
		require.NoError(t, err)
	}
	page, err := NewAuditEventRepository(store).List(context.Background(), AuditFilter{Project: &projectOne}, 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	assert.Equal(t, "one", page.Events[0].EventID)
}

func TestAuditEventRepository_EmptyAfterPagePreservesNextOlderCursor(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	for _, id := range []string{"one", "two", "three"} {
		_, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: id, SchemaVersion: "1", Category: "auth", EventCode: "auth.login", Type: "start", Action: "authenticate", Outcome: "success", ActorType: "user", ActorID: "1", InstanceID: "test", Metadata: "{}"})
		require.NoError(t, err)
	}
	page, err := NewAuditEventRepository(store).List(context.Background(), AuditFilter{After: 3}, 50)
	require.NoError(t, err)
	assert.Empty(t, page.Events)
	assert.Equal(t, int64(4), page.Older)
	back, err := NewAuditEventRepository(store).List(context.Background(), AuditFilter{Before: page.Older}, 50)
	require.NoError(t, err)
	require.Len(t, back.Events, 3)
	assert.Equal(t, int64(3), back.Events[0].Seq)
}

func TestAuditEventRepository_ExportsAfterCursorNewestFirst(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	for _, id := range []string{"one", "two", "three"} {
		_, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: id, SchemaVersion: "1", Category: "auth", EventCode: "auth.login", Type: "start", Action: "authenticate", Outcome: "success", ActorType: "user", ActorID: "1", InstanceID: "test", Metadata: "{}"})
		require.NoError(t, err)
	}
	rows, complete, err := NewAuditEventRepository(store).Export(context.Background(), AuditFilter{After: 1}, 50)
	require.NoError(t, err)
	assert.True(t, complete)
	assert.Equal(t, []int64{3, 2}, []int64{rows[0].Seq, rows[1].Seq})
}

func TestAuditEventRepository_FiltersMultipleKinds(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	for _, event := range []db.AuditEvent{
		{EventID: "auth", SchemaVersion: "1", Category: "auth", EventCode: "auth.login", Type: "start", Action: "authenticate", Outcome: "success", ActorType: "user", ActorID: "1", InstanceID: "test", Metadata: "{}"},
		{EventID: "iam", SchemaVersion: "1", Category: "iam", EventCode: "iam.user", Type: "creation", Action: "create", Outcome: "success", ActorType: "user", ActorID: "1", InstanceID: "test", Metadata: "{}"},
		{EventID: "task", SchemaVersion: "1", Category: "task", EventCode: "task.execution", Type: "creation", Action: "create", Outcome: "success", ActorType: "user", ActorID: "1", InstanceID: "test", Metadata: "{}"},
	} {
		_, err := store.CreateAuditEvent(context.Background(), event)
		require.NoError(t, err)
	}
	page, err := NewAuditEventRepository(store).List(context.Background(), AuditFilter{Kinds: []string{"auth.login/authenticate", "iam.user/create"}}, 50)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	assert.Equal(t, []string{"iam", "auth"}, []string{page.Events[0].EventID, page.Events[1].EventID})
}
