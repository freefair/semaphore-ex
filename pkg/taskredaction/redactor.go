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
	if taskSecret == "" || len(targets) == 0 {
		return Redactor{}
	}
	values := make(map[string]struct{}, len(targets))
	var decoded map[string]any
	if json.Unmarshal([]byte(taskSecret), &decoded) != nil {
		return Redactor{}
	}
	for _, target := range targets {
		value, ok := decoded[target].(string)
		if ok && value != "" {
			values[value] = struct{}{}
			// Task summaries, structured logs and artifacts pass through Go JSON
			// encoding. Protect its exact escaped representation as well as the
			// raw value; arbitrary task-side transformations remain out of scope.
			if encoded, err := json.Marshal(value); err == nil && len(encoded) >= 2 {
				escaped := string(encoded[1 : len(encoded)-1])
				if escaped != "" {
					values[escaped] = struct{}{}
				}
			}
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
