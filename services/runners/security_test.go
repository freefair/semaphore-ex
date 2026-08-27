package runners

import (
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerTransportTrustMatrix(t *testing.T) {
	assert.Equal(t, db.RunnerTransportPlaintext, runnerTransportTrust("http://runner.test", nil))
	assert.Equal(t, db.RunnerTransportSystemCA, runnerTransportTrust("https://runner.test", nil))
	assert.Equal(t, db.RunnerTransportInsecure, runnerTransportTrust("https://runner.test", &util.RunnerConnectionConfig{SkipTLSVerify: true}))
	assert.Equal(t, db.RunnerTransportCustomCA, runnerTransportTrust("https://runner.test", &util.RunnerConnectionConfig{ServerCACertFile: "ca.pem"}))
}

func TestOnlySecureRegistrationTokensRequireRunnerIdentity(t *testing.T) {
	assert.False(t, runnerRegistrationRequiresIdentity(db.RunnerRegistrationTokenPrefix+"standard"))
	assert.True(t, runnerRegistrationRequiresIdentity(db.RunnerSecureRegistrationTokenPrefix+"secure"))
}

func TestRunnerHTTPClientValidAndInvalidTLSFixtures(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	validCA := filepath.Join(dir, "valid-ca.pem")
	certificate := server.Certificate()
	require.NotNil(t, certificate)
	require.NoError(t, os.WriteFile(validCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0600))
	invalidCA := filepath.Join(dir, "invalid-ca.pem")
	require.NoError(t, os.WriteFile(invalidCA, []byte("not a certificate"), 0600))

	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{Runner: &util.RunnerConfig{Connection: &util.RunnerConnectionConfig{ServerCACertFile: validCA}}}
	response, err := newHTTPClient().Get(server.URL)
	require.NoError(t, err)
	response.Body.Close()
	assert.Equal(t, http.StatusNoContent, response.StatusCode)

	util.Config.Runner.Connection.ServerCACertFile = invalidCA
	_, err = newHTTPClient().Get(server.URL)
	var unknownAuthority x509.UnknownAuthorityError
	assert.Error(t, err)
	assert.ErrorAs(t, err, &unknownAuthority)
}

func TestEnsureRunnerIdentityCreatesAndReusesPrivateKey(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{Runner: &util.RunnerConfig{}}
	configPath := filepath.Join(t.TempDir(), "config.yaml")

	require.NoError(t, EnsureRunnerIdentity(&configPath))
	firstPublicKey := util.Config.Runner.IdentityPublicKey
	require.NotEmpty(t, firstPublicKey)
	info, err := os.Stat(configPath + ".runner-identity")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	util.Config.Runner.IdentityPublicKey = ""
	util.Config.Runner.IdentityPrivateKeyFile = ""
	require.NoError(t, EnsureRunnerIdentity(&configPath))
	assert.Equal(t, firstPublicKey, util.Config.Runner.IdentityPublicKey)
}

func TestRunnerRegistrationErrorIncludesPolicyRemediation(t *testing.T) {
	policyResponse := &http.Response{
		StatusCode: http.StatusConflict,
		Body: io.NopCloser(strings.NewReader(
			`{"reason":"secure mode requires verified TLS server identity","remediation":"Use HTTPS with system trust."}`,
		)),
	}

	message := (&JobPool{}).getResponseErrorMessage(policyResponse)
	assert.Contains(t, message, "409")
	assert.Contains(t, message, "verified TLS server identity")
	assert.Contains(t, message, "remediation: Use HTTPS with system trust.")

	legacyResponse := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"error":"Invalid registration token"}`)),
	}
	assert.Contains(t, (&JobPool{}).getResponseErrorMessage(legacyResponse), "Invalid registration token")
}
