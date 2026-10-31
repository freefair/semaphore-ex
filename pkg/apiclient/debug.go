package apiclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
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

// secretJSONKeys are masked when a request or response body is traced.
// "string" is the value of a String access key.
var secretJSONKeys = regexp.MustCompile(`(?i)"(password|passcode|token|secret|registration_token|login_password|ssh|plain|string|private_key|passphrase)"\s*:\s*("(?:[^"\\]|\\.)*"|\{[^{}]*\})`)

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

// redactJSON masks well-known secret fields of a JSON document for the debug
// trace. It works on the text so that a body that is not an object (an array,
// plain text) passes through unchanged.
func redactJSON(body []byte) string {
	if !json.Valid(body) {
		return strings.TrimSpace(string(body))
	}
	return secretJSONKeys.ReplaceAllString(string(body), `"$1":"`+masked+`"`)
}
