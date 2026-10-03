// Package taskredaction removes exact dispatch-time credential values from
// task output. It intentionally does not claim to prevent transformations of
// a value by untrusted task code; it protects the direct-value channels the
// runtime controls.
package taskredaction

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

const replacement = "[REDACTED]"

type Redactor struct{ values []string }

func NewFromTaskSecret(taskSecret string, targets []string) Redactor {
	return NewFromTaskSecretAndValues(taskSecret, targets, nil)
}

// NewFromTaskSecretAndValues also covers dispatch-only credentials such as
// project host mappings. Those values never belong in the task payload.
func NewFromTaskSecretAndValues(taskSecret string, targets []string, extra []string) Redactor {
	values := make(map[string]struct{}, len(targets))
	var decoded map[string]any
	if taskSecret != "" && json.Unmarshal([]byte(taskSecret), &decoded) == nil {
		for _, target := range targets {
			value, ok := decoded[target].(string)
			if ok && value != "" {
				values[value] = struct{}{}
			}
		}
	}
	for _, value := range extra {
		if value != "" {
			values[value] = struct{}{}
		}
	}
	sources := make([]string, 0, len(values))
	for value := range values {
		sources = append(sources, value)
	}
	for _, value := range sources {
		if encoded, err := json.Marshal(value); err == nil && len(encoded) >= 2 {
			values[string(encoded[1:len(encoded)-1])] = struct{}{}
		}
		// Git URL rewrites encode credentials in userinfo before a child process
		// receives them. Keep that representation in the corpus too, so task
		// output cannot disclose an escaped password from a failed clone.
		userinfo := url.User(value).String()
		values[userinfo] = struct{}{}
		values[strings.ReplaceAll(userinfo, "=", "%3D")] = struct{}{}
		password := strings.TrimPrefix(url.UserPassword("redaction", value).String(), "redaction:")
		values[password] = struct{}{}
		// Git's GIT_CONFIG_PARAMETERS parser splits at the first equals sign, so
		// GalaxyGitEnv additionally escapes literal equals signs in userinfo.
		values[strings.ReplaceAll(password, "=", "%3D")] = struct{}{}
	}
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(left, right int) bool { return len(ordered[left]) > len(ordered[right]) })
	return Redactor{values: ordered}
}

func (r Redactor) Redact(value string) string {
	for _, sensitive := range r.values {
		value = strings.ReplaceAll(value, sensitive, replacement)
	}
	return value
}
