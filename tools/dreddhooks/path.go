package dreddhooks

import (
	"regexp"
	"strconv"
	"strings"
)

var dedicatedStateFixturePaths = []*regexp.Regexp{
	regexp.MustCompile(`^/api/audit-webhook(?:/|$)`),
	regexp.MustCompile(`^/api/workflow-triggers(?:/|$)`),
	regexp.MustCompile(`^/api/workflow-artifact-retention(?:/|$)`),
	regexp.MustCompile(`^/api/global-credentials(?:/|$)`),
	regexp.MustCompile(`^/api/policy-guardrails(?:/|$)`),
	regexp.MustCompile(`^/api/project/\d+/(?:granted-credentials|policy-guardrails|workflow-artifact-retention)(?:/|$)`),
	regexp.MustCompile(`^/api/project/\d+/keys/(?:generate|\d+/rotate)(?:/|$)`),
	regexp.MustCompile(`^/api/project/\d+/workflows/\d+/(?:triggers|preflight)(?:/|$)`),
	regexp.MustCompile(`^/api/project/\d+/workflows/\d+/runs/\d+(?:/|$)`),
	regexp.MustCompile(`^/api/project/\d+/tasks/(?:preflight|\d+/(?:credential-usage|runner-attempts|ansible/summary))(?:/|$)`),
}

// PutRequestObjectID returns the trailing numeric resource identifier used by
// item PUT endpoints. Collection and singleton PUT endpoints have no such ID.
func PutRequestObjectID(path string) (int, bool) {
	segment := path
	if query := strings.IndexByte(segment, '?'); query >= 0 {
		segment = segment[:query]
	}
	segment = strings.TrimSuffix(segment, "/")
	if separator := strings.LastIndexByte(segment, '/'); separator >= 0 {
		segment = segment[separator+1:]
	}
	identifier, err := strconv.Atoi(segment)
	return identifier, err == nil
}

// RequiresDedicatedStateFixture identifies full-product endpoints whose
// multi-step state machines are covered by their focused Go integration tests.
// Dredd still parses their OpenAPI contracts, but its generic one-request
// fixture cannot execute them without manufacturing invalid state.
func RequiresDedicatedStateFixture(path string) bool {
	if query := strings.IndexByte(path, '?'); query >= 0 {
		path = path[:query]
	}
	for _, pattern := range dedicatedStateFixturePaths {
		if pattern.MatchString(path) {
			return true
		}
	}
	return false
}
