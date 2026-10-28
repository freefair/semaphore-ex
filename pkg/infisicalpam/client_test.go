package infisicalpam_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam/pamtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func newTestClient(t *testing.T, env *pamtest.Environment, secret string) *infisicalpam.Client {
	t.Helper()
	client, err := infisicalpam.NewClient(infisicalpam.Config{
		SiteURL:       env.URL(),
		CACertificate: env.APICACert,
		ClientID:      pamtest.ClientID,
		ClientSecret:  []byte(secret),
	})
	require.NoError(t, err)
	return client
}

func openSession(t *testing.T, env *pamtest.Environment) (*infisicalpam.Client, infisicalpam.AccessToken, *infisicalpam.Session) {
	t.Helper()
	client := newTestClient(t, env, pamtest.ClientSecret)
	token, err := client.Login(context.Background())
	require.NoError(t, err)
	session, err := client.Access(context.Background(), token, infisicalpam.AccessRequest{
		Path: "servers/web-1", Duration: time.Hour, Reason: "Semaphore task 7",
	})
	require.NoError(t, err)
	return client, token, session
}

func runCommand(t *testing.T, session *infisicalpam.Session, command string) string {
	t.Helper()
	conn, err := session.Dial(context.Background())
	require.NoError(t, err)
	sshConn, channels, requests, err := ssh.NewClientConn(conn, "gateway", &ssh.ClientConfig{
		User:            "ignored",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	require.NoError(t, err)
	client := ssh.NewClient(sshConn, channels, requests)
	defer client.Close()
	sshSession, err := client.NewSession()
	require.NoError(t, err)
	defer sshSession.Close()
	output, err := sshSession.Output(command)
	require.NoError(t, err)
	return string(output)
}

func TestSessionRoutesSSHThroughRelay(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	_, _, session := openSession(t, env)

	output := runCommand(t, session, "uptime")

	assert.Equal(t, "ok uptime\n", output)
	assert.Equal(t, []pamtest.AccessRecord{{
		Path: "servers/web-1", Duration: "1h0m0s", Reason: "Semaphore task 7", Token: pamtest.AccessToken,
	}}, env.Accesses())
}

func TestSessionPrefersDirectAddress(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{Direct: true, NoRelay: true})
	_, _, session := openSession(t, env)

	assert.Equal(t, "ok id\n", runCommand(t, session, "id"))
	assert.Empty(t, session.RelayHost)
}

func TestSessionRejectsForeignGatewayIdentity(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{GatewayIdentity: "urn:infisical:gateway:someone-else"})
	_, _, session := openSession(t, env)

	_, err := session.Dial(context.Background())

	require.Error(t, err)
	assert.Equal(t, infisicalpam.ErrorTransport, infisicalpam.CategoryOf(err))
}

func TestSessionRejectsGatewayWithoutIdentity(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{OmitGatewayIdentity: true})
	_, _, session := openSession(t, env)

	_, err := session.Dial(context.Background())

	require.Error(t, err)
	assert.Equal(t, infisicalpam.ErrorTransport, infisicalpam.CategoryOf(err))
}

func TestAccessRejectsSessionWithoutGatewayID(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{OmitGatewayID: true})
	client := newTestClient(t, env, pamtest.ClientSecret)
	token, err := client.Login(context.Background())
	require.NoError(t, err)

	_, err = client.Access(context.Background(), token, infisicalpam.AccessRequest{Path: "servers/web-1", Duration: time.Hour})

	assert.Equal(t, infisicalpam.ErrorResponseInvalid, infisicalpam.CategoryOf(err))
}

func TestSessionCancelAndEnd(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	client, token, session := openSession(t, env)

	require.NoError(t, session.Cancel(context.Background()))
	require.NoError(t, client.EndSession(context.Background(), token, session.ID))

	assert.Eventually(t, func() bool { return len(env.Cancelled()) == 1 }, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{session.ID}, env.Cancelled())
	assert.Equal(t, []string{session.ID}, env.Ended())
}

func TestLoginRejectsWrongSecret(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	client := newTestClient(t, env, "wrong")

	_, err := client.Login(context.Background())

	require.Error(t, err)
	assert.Equal(t, infisicalpam.ErrorAuthentication, infisicalpam.CategoryOf(err))
	assert.NotContains(t, err.Error(), "wrong")
}

func TestAccessErrorCategories(t *testing.T) {
	tests := []struct {
		name     string
		apiError string
		expected infisicalpam.ErrorCategory
	}{
		{"approval", "PAM_APPROVAL_REQUIRED", infisicalpam.ErrorApprovalRequired},
		{"expired grant", "PAM_GRANT_EXPIRED", infisicalpam.ErrorApprovalRequired},
		{"reason", "PAM_REASON_REQUIRED", infisicalpam.ErrorReasonRequired},
		{"mfa", "SESSION_MFA_REQUIRED", infisicalpam.ErrorMFARequired},
		{"forbidden", "PermissionDenied", infisicalpam.ErrorPermission},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := pamtest.Start(t, pamtest.Options{AccessError: tt.apiError})
			client := newTestClient(t, env, pamtest.ClientSecret)
			token, err := client.Login(context.Background())
			require.NoError(t, err)

			_, err = client.Access(context.Background(), token, infisicalpam.AccessRequest{Path: "servers/web-1", Duration: time.Hour})

			require.Error(t, err)
			assert.Equal(t, tt.expected, infisicalpam.CategoryOf(err))
		})
	}
}

func TestAccessNotFound(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	client := newTestClient(t, env, pamtest.ClientSecret)
	token, err := client.Login(context.Background())
	require.NoError(t, err)

	_, err = client.Access(context.Background(), token, infisicalpam.AccessRequest{Path: "missing/web", Duration: time.Hour})

	assert.Equal(t, infisicalpam.ErrorNotFound, infisicalpam.CategoryOf(err))
}

func TestAccessRequestValidation(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	client := newTestClient(t, env, pamtest.ClientSecret)
	tests := []struct {
		name    string
		request infisicalpam.AccessRequest
	}{
		{"no folder", infisicalpam.AccessRequest{Path: "web", Duration: time.Hour}},
		{"traversal", infisicalpam.AccessRequest{Path: "servers/../web", Duration: time.Hour}},
		{"short duration", infisicalpam.AccessRequest{Path: "servers/web", Duration: time.Second}},
		{"long duration", infisicalpam.AccessRequest{Path: "servers/web", Duration: 48 * time.Hour}},
		{"newline reason", infisicalpam.AccessRequest{Path: "servers/web", Duration: time.Hour, Reason: "a\nb"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Access(context.Background(), infisicalpam.AccessToken{Value: "x"}, tt.request)
			assert.Equal(t, infisicalpam.ErrorValidation, infisicalpam.CategoryOf(err))
		})
	}
	assert.Empty(t, env.Accesses())
}

func TestNewClientValidation(t *testing.T) {
	valid := infisicalpam.Config{SiteURL: "https://app.infisical.com", ClientID: "abc-123", ClientSecret: []byte("s")}
	tests := []struct {
		name   string
		mutate func(*infisicalpam.Config)
	}{
		{"http url", func(c *infisicalpam.Config) { c.SiteURL = "http://app.infisical.com" }},
		{"url with credentials", func(c *infisicalpam.Config) { c.SiteURL = "https://user:pw@app.infisical.com" }},
		{"url with query", func(c *infisicalpam.Config) { c.SiteURL = "https://app.infisical.com/?x=1" }},
		{"bad client id", func(c *infisicalpam.Config) { c.ClientID = "a b" }},
		{"empty secret", func(c *infisicalpam.Config) { c.ClientSecret = nil }},
		{"bad organization", func(c *infisicalpam.Config) { c.OrganizationSlug = "Org!" }},
		{"bad CA", func(c *infisicalpam.Config) { c.CACertificate = []byte("nope") }},
		{"timeout", func(c *infisicalpam.Config) { c.Timeout = time.Hour }},
	}
	_, err := infisicalpam.NewClient(valid)
	require.NoError(t, err)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := valid
			config.ClientSecret = bytes.Clone(valid.ClientSecret)
			tt.mutate(&config)
			_, err := infisicalpam.NewClient(config)
			assert.Equal(t, infisicalpam.ErrorValidation, infisicalpam.CategoryOf(err))
		})
	}
}

func TestValidateAccountPath(t *testing.T) {
	tests := []struct {
		path  string
		valid bool
	}{
		{"servers/web-1", true},
		{"prod/linux/web_1.example", true},
		{"servers", false},
		{"/servers/web", false},
		{"servers/web/", false},
		{"servers/ web", false},
		{"servers/web;rm", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.valid, infisicalpam.ValidateAccountPath(tt.path) == nil)
		})
	}
}
