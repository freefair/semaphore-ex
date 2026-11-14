package tasks

import (
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
)

// Test alert channel outcomes reported to the project settings view.
const (
	TestAlertSent          = "sent"
	TestAlertFailed        = "failed"
	TestAlertNotConfigured = "not_configured"
	TestAlertNoRecipients  = "no_recipients"
)

// TestAlertChannelReport states what a test alert did on one channel. Channels
// are configured in the server configuration; ConfigKeys names the keys an
// operator sets to enable a channel that is not configured.
type TestAlertChannelReport struct {
	Channel    string   `json:"channel"`
	Configured bool     `json:"configured"`
	Status     string   `json:"status"`
	Recipients int      `json:"recipients"`
	ConfigKeys []string `json:"config_keys"`
	Detail     string   `json:"detail"`
}

// TestAlertReport is the truthful result of a project test alert: Sent is true
// only when at least one channel confirmed delivery.
type TestAlertReport struct {
	Sent     bool                     `json:"sent"`
	Channels []TestAlertChannelReport `json:"channels"`
}

type testAlertChannel struct {
	name       string
	logName    string
	enabled    func() bool
	configKeys []string
	send       func(*TaskRunner)
}

func testAlertChannels() []testAlertChannel {
	return []testAlertChannel{
		{"email", "email", func() bool { return util.Config.EmailAlert },
			[]string{"email_alert", "email_sender", "email_host", "email_port"}, (*TaskRunner).sendMailAlert},
		{"telegram", "telegram", func() bool { return util.Config.TelegramAlert },
			[]string{"telegram_alert", "telegram_chat", "telegram_token"}, (*TaskRunner).sendTelegramAlert},
		{"slack", "slack", func() bool { return util.Config.SlackAlert },
			[]string{"slack_alert", "slack_url"}, (*TaskRunner).sendSlackAlert},
		{"rocketchat", "rocketchat", func() bool { return util.Config.RocketChatAlert },
			[]string{"rocketchat_alert", "rocketchat_url"}, (*TaskRunner).sendRocketChatAlert},
		{"microsoft_teams", "microsoft teams", func() bool { return util.Config.MicrosoftTeamsAlert },
			[]string{"microsoft_teams_alert", "microsoft_teams_url"}, (*TaskRunner).sendMicrosoftTeamsAlert},
		{"dingtalk", "dingtalk", func() bool { return util.Config.DingTalkAlert },
			[]string{"dingtalk_alert", "dingtalk_url"}, (*TaskRunner).sendDingTalkAlert},
		{"gotify", "gotify", func() bool { return util.Config.GotifyAlert },
			[]string{"gotify_alert", "gotify_url", "gotify_token"}, (*TaskRunner).sendGotifyAlert},
	}
}

// SendProjectTestAlertsReport sends a test alert on every configured channel
// and reports per channel whether it was delivered, failed, or could not be
// attempted. It never reports success for a channel that did not confirm it.
func SendProjectTestAlertsReport(project db.Project, store db.Store) (TestAlertReport, error) {
	projectUsers, err := store.GetProjectUsers(project.ID, db.RetrieveQueryParams{})
	if err != nil {
		return TestAlertReport{}, err
	}
	userIDs := make([]int, 0, len(projectUsers))
	mailRecipients := 0
	for _, u := range projectUsers {
		userIDs = append(userIDs, u.ID)
		if u.Alert && strings.TrimSpace(u.Email) != "" {
			mailRecipients++
		}
	}

	report := TestAlertReport{Channels: []TestAlertChannelReport{}}
	for _, channel := range testAlertChannels() {
		entry := TestAlertChannelReport{
			Channel: channel.name, Configured: channel.enabled(), ConfigKeys: channel.configKeys,
		}
		if channel.name == "email" {
			entry.Recipients = mailRecipients
		}
		switch {
		case !entry.Configured:
			entry.Status = TestAlertNotConfigured
		case channel.name == "email" && mailRecipients == 0:
			entry.Status = TestAlertNoRecipients
		default:
			entry.Status, entry.Detail = runTestAlertChannel(project, store, userIDs, channel)
		}
		if entry.Status == TestAlertSent {
			report.Sent = true
		}
		report.Channels = append(report.Channels, entry)
	}
	return report, nil
}

func runTestAlertChannel(
	project db.Project,
	store db.Store,
	userIDs []int,
	channel testAlertChannel,
) (string, string) {
	runner := newTestAlertRunner(project, store, userIDs)
	// Drain while sending: a channel with many recipients logs more lines than
	// the buffer holds, and a full logger would block the request.
	lines := make(chan []string, 1)
	go func() {
		var collected []string
		for record := range runner.pool.logger {
			collected = append(collected, record.output)
		}
		lines <- collected
	}()
	channel.send(runner)
	close(runner.pool.logger)

	var attempted, sent bool
	var failure string
	for _, output := range <-lines {
		line := strings.ToLower(output)
		if !strings.Contains(line, channel.logName) {
			continue
		}
		switch {
		case strings.HasPrefix(line, "sent successfully"):
			sent = true
		case strings.HasPrefix(line, "attempting to send"):
			attempted = true
		case strings.HasPrefix(line, "can't") || strings.Contains(line, "is empty"):
			failure = sanitizedTestAlertFailure(line)
		}
	}
	switch {
	case sent && failure == "":
		return TestAlertSent, ""
	case failure != "":
		return TestAlertFailed, failure
	case attempted:
		return TestAlertFailed, "The channel did not confirm delivery; see the server log."
	default:
		return TestAlertFailed, "The channel was not attempted; check its server configuration."
	}
}

// sanitizedTestAlertFailure turns a sender log line into a fixed message. Raw
// sender errors can embed webhook URLs and bot tokens from the server
// configuration, and project managers may run test alerts, so only the
// receiver's HTTP status code is passed through.
func sanitizedTestAlertFailure(line string) string {
	if _, after, found := strings.Cut(line, "response code: "); found {
		code := strings.TrimSpace(after)
		if len(code) == 3 && strings.Trim(code, "0123456789") == "" {
			return "The receiver answered with HTTP " + code + "."
		}
	}
	switch {
	case strings.Contains(line, "template") || strings.Contains(line, "is empty"):
		return "The notification text could not be generated; see the server log."
	case strings.Contains(line, "error"):
		return "The receiver could not be reached; see the server log."
	default:
		return "Sending failed; see the server log."
	}
}

func newTestAlertRunner(project db.Project, store db.Store, userIDs []int) *TaskRunner {
	return &TaskRunner{
		Task: db.Task{
			ProjectID:  project.ID,
			TemplateID: 0,
			Status:     task_logger.TaskSuccessStatus,
			Message:    "This is a test notification",
		},
		Template: db.Template{
			ID:        0,
			ProjectID: project.ID,
			Name:      "Test Notification",
			Type:      db.TemplateTask,
		},
		users:     userIDs,
		alert:     project.Alert,
		alertChat: project.AlertChat,
		pool: &TaskPool{
			logger: make(chan logRecord, 100),
			store:  store,
		},
	}
}
