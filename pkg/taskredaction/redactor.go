// Package taskredaction removes exact dispatch-time credential values from
// task output. It intentionally does not claim to prevent transformations of
// a value by untrusted task code; it protects the direct-value channels the
// runtime controls.
package taskredaction

import (
	"encoding/json"
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
