package apiclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// readFile is a seam for tests; it is os.ReadFile in production.
var readFile = os.ReadFile

const masked = "***"

// maskedHeaders never reach the debug trace in clear text.
var maskedHeaders = map[string]bool{
	"Authorization": true,
	"Cookie":        true,
	"Set-Cookie":    true,
}

// secretKeyParts mark a JSON key whose value is masked in the debug trace:
// a key containing one of them, compared after JSON unescaping, in lower case
// and without "-" and "_" (password, current_password, api-token, …).
var secretKeyParts = []string{"password", "passcode", "token", "secret", "passphrase", "privatekey"}

// secretKeys are masked as a whole: "string" holds the value of a String
// access key, "ssh" and "plain" hold key material.
var secretKeys = map[string]bool{"string": true, "ssh": true, "plain": true}

func isSecretKey(key string) bool {
	k := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
	if secretKeys[k] {
		return true
	}
	for _, part := range secretKeyParts {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

func (c *Client) trace(format string, args ...any) {
	if c.debug == nil {
		return
	}
	_, _ = fmt.Fprintf(c.debug, format+"\n", args...)
}

func (c *Client) traceHeaders(prefix string, h http.Header) {
	if c.debug == nil {
		return
	}
	for name, values := range h {
		for _, v := range values {
			if maskedHeaders[http.CanonicalHeaderKey(name)] {
				v = masked
			}
			_, _ = fmt.Fprintf(c.debug, "%s%s: %s\n", prefix, name, v)
		}
	}
}

// redactResponse masks a response body for the debug trace. API token
// responses carry the token itself in their "id" field, so they are never
// traced; other bodies are redacted by key name.
func redactResponse(apiPath string, body []byte) string {
	if apiPath == "/user/tokens" || strings.HasPrefix(apiPath, "/user/tokens/") {
		return masked
	}
	return redactJSON(body)
}

// redactJSON masks secret fields of a JSON document for the debug trace. It
// decodes the document, so escaped key names and nested values are found, and
// masks a secret key's whole value. Text that is not JSON passes through.
func redactJSON(body []byte) string {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return strings.TrimSpace(string(body))
	}
	out, err := json.Marshal(redactValue(doc))
	if err != nil {
		return masked
	}
	return string(out)
}

func redactValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if isSecretKey(k) {
				x[k] = masked
			} else {
				x[k] = redactValue(child)
			}
		}
	case []any:
		for i, child := range x {
			x[i] = redactValue(child)
		}
	}
	return v
}
