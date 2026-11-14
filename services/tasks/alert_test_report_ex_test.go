package tasks

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testAlertProject(t *testing.T, alertUser bool) (db.Project, db.Store) {
	t.Helper()
	store := sql.InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "alerts", Alert: true})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "ops", Name: "Ops", Email: "ops@example.invalid", Alert: alertUser,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, Role: db.ProjectOwner})
	require.NoError(t, err)
	return project, store
}

func withAlertTransport(t *testing.T, status int) {
	t.Helper()
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
}

func channelReport(t *testing.T, report TestAlertReport, name string) TestAlertChannelReport {
	t.Helper()
	for _, channel := range report.Channels {
		if channel.Channel == name {
			return channel
		}
	}
	require.Failf(t, "channel missing", "no report for %s", name)
	return TestAlertChannelReport{}
}

func TestSendProjectTestAlertsReport_NothingConfiguredIsNotSuccess(t *testing.T) {
	project, store := testAlertProject(t, true)

	report, err := SendProjectTestAlertsReport(project, store)

	require.NoError(t, err)
	assert.False(t, report.Sent)
	require.Len(t, report.Channels, 7)
	for _, channel := range report.Channels {
		assert.False(t, channel.Configured, channel.Channel)
		assert.Equal(t, TestAlertNotConfigured, channel.Status, channel.Channel)
		assert.NotEmpty(t, channel.ConfigKeys, channel.Channel)
	}
	assert.Equal(t, []string{"email_alert", "email_sender", "email_host", "email_port"},
		channelReport(t, report, "email").ConfigKeys)
}

func TestSendProjectTestAlertsReport_EmailWithoutRecipients(t *testing.T) {
	project, store := testAlertProject(t, false)
	util.Config.EmailAlert = true

	report, err := SendProjectTestAlertsReport(project, store)

	require.NoError(t, err)
	email := channelReport(t, report, "email")
	assert.True(t, email.Configured)
	assert.Equal(t, TestAlertNoRecipients, email.Status)
	assert.Equal(t, 0, email.Recipients)
	assert.False(t, report.Sent)
}

func TestSendProjectTestAlertsReport_WebhookOutcome(t *testing.T) {
	tests := []struct {
		name       string
		httpStatus int
		status     string
		sent       bool
	}{
		{"delivered", http.StatusOK, TestAlertSent, true},
		{"rejected by the receiver", http.StatusInternalServerError, TestAlertFailed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, store := testAlertProject(t, true)
			util.Config.SlackAlert = true
			util.Config.SlackUrl = "https://hooks.example.invalid/slack"
			withAlertTransport(t, tt.httpStatus)

			report, err := SendProjectTestAlertsReport(project, store)

			require.NoError(t, err)
			slack := channelReport(t, report, "slack")
			assert.True(t, slack.Configured)
			assert.Equal(t, tt.status, slack.Status)
			assert.Equal(t, tt.sent, report.Sent)
			if !tt.sent {
				assert.Contains(t, slack.Detail, "slack")
			}
		})
	}
}
