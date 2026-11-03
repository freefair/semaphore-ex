package sql

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The activity of a project reaches a member only with read_project; events without a project reach everyone.
func TestGetUserEvents_NeedsReadOnTheProject(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "p"})
	require.NoError(t, err)
	member := func(name string, mask db.ProjectUserPermission) db.User {
		role, err := store.CreateRole(db.Role{Slug: name, Name: name, Permissions: mask, ProjectID: &project.ID})
		require.NoError(t, err)
		user, err := store.CreateUserWithoutPassword(db.User{Username: name, Name: name, Email: name + "@example.com"})
		require.NoError(t, err)
		_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
		require.NoError(t, err)
		return user
	}
	reader, nobody := member("reader", db.CanReadProject), member("nobody", 0)
	inProject, global := "in project", "global"
	_, err = store.CreateEvent(db.Event{ProjectID: &project.ID, Description: &inProject})
	require.NoError(t, err)
	_, err = store.CreateEvent(db.Event{Description: &global})
	require.NoError(t, err)

	descriptions := func(user db.User) []string {
		events, err := store.GetUserEvents(user.ID, db.RetrieveQueryParams{})
		require.NoError(t, err)
		got := []string{}
		for _, event := range events {
			got = append(got, *event.Description)
		}
		return got
	}
	assert.Equal(t, []string{"global", "in project"}, descriptions(reader))
	assert.Equal(t, []string{"global"}, descriptions(nobody))
}

func TestTaskEventsFilterBeforeLimitForBothFeeds(t *testing.T) {
	f := newVisibilityFixture(t)
	role, err := f.store.GetProjectRole(f.projectID, "grantee")
	require.NoError(t, err)
	role.Permissions |= db.CanReadProject
	_, err = f.store.UpdateProjectRole(f.projectID, role, role.Revision)
	require.NoError(t, err)
	kind := db.EventTask
	visible, hidden := "visible", "hidden"
	_, err = f.store.CreateEvent(db.Event{ProjectID: &f.projectID, ObjectType: &kind, ObjectID: &f.taskA.ID, Description: &visible})
	require.NoError(t, err)
	_, err = f.store.CreateEvent(db.Event{ProjectID: &f.projectID, ObjectType: &kind, ObjectID: &f.taskB.ID, Description: &hidden})
	require.NoError(t, err)
	params := db.RetrieveQueryParams{Count: 1, Viewer: &db.Viewer{UserID: f.grantee.ID}}
	projectEvents, err := f.store.GetEvents(f.projectID, params)
	require.NoError(t, err)
	require.Len(t, projectEvents, 1)
	assert.Equal(t, visible, *projectEvents[0].Description)
	userEvents, err := f.store.GetUserEvents(f.grantee.ID, params)
	require.NoError(t, err)
	require.Len(t, userEvents, 1)
	assert.Equal(t, visible, *userEvents[0].Description)
}

func TestEventVisibilityFindsVisibleEventPastHiddenPage(t *testing.T) {
	f := newVisibilityFixture(t)
	role, err := f.store.GetProjectRole(f.projectID, "grantee")
	require.NoError(t, err)
	role.Permissions |= db.CanReadProject
	_, err = f.store.UpdateProjectRole(f.projectID, role, role.Revision)
	require.NoError(t, err)
	kind := db.EventTask
	visible, hidden := "visible older", "hidden newer"
	_, err = f.store.CreateEvent(db.Event{ProjectID: &f.projectID, ObjectType: &kind, ObjectID: &f.taskA.ID, Description: &visible})
	require.NoError(t, err)
	for i := 0; i < 205; i++ {
		_, err = f.store.CreateEvent(db.Event{ProjectID: &f.projectID, ObjectType: &kind, ObjectID: &f.taskB.ID, Description: &hidden})
		require.NoError(t, err)
	}
	var sqlTrace bytes.Buffer
	f.store.Sql().TraceOn("", log.New(&sqlTrace, "", 0))
	defer f.store.Sql().TraceOff()
	params := db.RetrieveQueryParams{Count: 1, Viewer: &db.Viewer{UserID: f.grantee.ID}}
	projectEvents, err := f.store.GetEvents(f.projectID, params)
	require.NoError(t, err)
	require.Len(t, projectEvents, 1)
	assert.Equal(t, visible, *projectEvents[0].Description)
	userEvents, err := f.store.GetUserEvents(f.grantee.ID, params)
	require.NoError(t, err)
	require.Len(t, userEvents, 1)
	assert.Equal(t, visible, *userEvents[0].Description)
	trace := strings.ToLower(sqlTrace.String())
	assert.True(t, strings.Contains(trace, "limit 200"), "event query must fetch bounded pages")
	assert.True(t, strings.Contains(trace, "event.id <"), "the next page must use an event-ID cursor")
}

// Bound the failing query so a cursor regression cannot hang the test process.
type eventQueryGuard struct{ queries int }

func (g *eventQueryGuard) Printf(format string, args ...interface{}) {
	if strings.Contains(strings.ToLower(fmt.Sprintf(format, args...)), "from event") {
		g.queries++
		if g.queries > 5 {
			panic("event pagination did not terminate")
		}
	}
}

func TestUserEventCursorHandlesGlobalAndHiddenEvents(t *testing.T) {
	f := newVisibilityFixture(t)
	role, err := f.store.GetProjectRole(f.projectID, "grantee")
	require.NoError(t, err)
	role.Permissions |= db.CanReadProject
	_, err = f.store.UpdateProjectRole(f.projectID, role, role.Revision)
	require.NoError(t, err)
	kind := db.EventTask
	visible, hidden, global := "visible", "hidden", "global"
	_, err = f.store.CreateEvent(db.Event{ProjectID: &f.projectID, ObjectType: &kind, ObjectID: &f.taskA.ID, Description: &visible})
	require.NoError(t, err)
	for i := 0; i < 205; i++ {
		_, err = f.store.CreateEvent(db.Event{Description: &global})
		require.NoError(t, err)
		if i%70 == 0 {
			_, err = f.store.CreateEvent(db.Event{ProjectID: &f.projectID, ObjectType: &kind, ObjectID: &f.taskB.ID, Description: &hidden})
			require.NoError(t, err)
		}
	}
	for _, tc := range []struct{ count, expected int }{{0, 206}, {202, 202}} {
		if !t.Run(fmt.Sprint(tc.count), func(t *testing.T) {
			guard := &eventQueryGuard{}
			f.store.Sql().TraceOn("", guard)
			defer f.store.Sql().TraceOff()
			var events []db.Event
			var err error
			require.NotPanics(t, func() { events, err = f.store.GetUserEvents(f.grantee.ID, db.RetrieveQueryParams{Count: tc.count}) })
			require.NoError(t, err)
			require.Len(t, events, tc.expected)
			seen := map[int]bool{}
			for _, event := range events {
				require.False(t, seen[event.ID], "duplicate event %d", event.ID)
				seen[event.ID] = true
				require.NotEqual(t, hidden, *event.Description)
			}
			require.LessOrEqual(t, guard.queries, 3)
		}) {
			return
		}
	}
}
