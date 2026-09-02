package taskredaction

import (
	"encoding/json"
	"strings"
	"testing"
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
