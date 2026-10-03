package taskredaction

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactorRemovesExactCredentialCorpusWithoutTouchingUnrelatedText(t *testing.T) {
	redactor := NewFromTaskSecret(`{"deploy_token":"token-value-123","other":"survey-value"}`, []string{"deploy_token"})
	for _, input := range []string{
		"token-value-123",
		"prefix token-value-123 suffix",
		`{"value":"token-value-123"}`,
		"token-value-123\nagain token-value-123",
	} {
		if output := redactor.Redact(input); output == input || strings.Contains(output, "token-value-123") {
			t.Fatalf("credential leaked from %q: %q", input, output)
		}
	}
	if output := redactor.Redact("survey-value remains"); output != "survey-value remains" {
		t.Fatalf("unbound task secret must not be redacted: %q", output)
	}
}

func TestRedactorRemovesGoJSONEscapedCredentialRepresentation(t *testing.T) {
	credential := "quote\" slash\\ angle<>&"
	payload, err := json.Marshal(map[string]string{"deploy_token": credential})
	if err != nil {
		t.Fatal(err)
	}
	redactor := NewFromTaskSecret(string(payload), []string{"deploy_token"})
	structured, err := json.Marshal(map[string]string{"value": credential})
	if err != nil {
		t.Fatal(err)
	}
	redacted := redactor.Redact(string(structured))
	if strings.Contains(redacted, "quote\\\"") || strings.Contains(redacted, "\\u003c") || strings.Contains(redacted, "slash\\\\") {
		t.Fatalf("JSON-escaped credential leaked: %s", redacted)
	}
	if !strings.Contains(redacted, replacement) {
		t.Fatalf("escaped credential was not redacted: %s", redacted)
	}
}

func TestRedactorExtraValuesProduceOneEscapedVariant(t *testing.T) {
	credential := `quote" slash\\`
	redactor := NewFromTaskSecretAndValues("", nil, []string{credential})
	escaped, err := json.Marshal(credential)
	require.NoError(t, err)
	expectedCorpus := []string{
		credential,
		string(escaped[1 : len(escaped)-1]),
		url.User(credential).String(),
	}
	assert.ElementsMatch(t, expectedCorpus, redactor.values)
	for _, value := range []string{credential, string(escaped[1 : len(escaped)-1])} {
		redacted := redactor.Redact(value)
		assert.NotContains(t, redacted, credential)
		assert.Contains(t, redacted, replacement)
	}
}

func TestRedactorRemovesURLUserinfoCredentialRepresentation(t *testing.T) {
	credential := "token=with@reserved/value%'ü"
	redactor := NewFromTaskSecretAndValues("", nil, []string{credential})
	encoded := strings.ReplaceAll(strings.TrimPrefix(url.UserPassword("user", credential).String(), "user:"), "=", "%3D")

	output := "fatal: could not authenticate to https://user:" + encoded + "@git.example/role.git"
	redacted := redactor.Redact(output)

	assert.NotContains(t, redacted, encoded)
	assert.NotContains(t, redacted, credential)
	assert.Contains(t, redacted, replacement)
}
