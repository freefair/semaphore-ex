package dreddhooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPutRequestObjectID(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		id   int
		ok   bool
	}{
		{name: "item", path: "/api/project/1/users/42", id: 42, ok: true},
		{name: "trailing slash", path: "/api/project/1/users/42/", id: 42, ok: true},
		{name: "query", path: "/api/project/1/users/42?revision=1", id: 42, ok: true},
		{name: "singleton", path: "/api/audit-webhook"},
		{name: "collection", path: "/api/runners"},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, ok := PutRequestObjectID(test.path)
			assert.Equal(t, test.ok, ok)
			assert.Equal(t, test.id, id)
		})
	}
}

func TestRequiresDedicatedStateFixture(t *testing.T) {
	for _, path := range []string{
		"/api/audit-webhook",
		"/api/audit-webhook/signing-secret?revision=0",
		"/api/global-credentials/21/grants",
		"/api/project/1/policy-guardrails/draft",
		"/api/project/1/keys/generate",
		"/api/project/1/keys/3/rotate",
		"/api/project/1/workflows/18/triggers",
		"/api/project/1/workflows/18/runs/19",
		"/api/project/1/workflows/18/runs/19/approvals/20",
		"/api/project/1/workflows/18/runs/19/file-artifacts/21/content",
		"/api/project/1/tasks/8/ansible/summary/hosts?count=50",
	} {
		assert.True(t, RequiresDedicatedStateFixture(path), path)
	}
	for _, path := range []string{
		"/api/events",
		"/api/project/1/workflows",
		"/api/project/1/workflows/18/run",
		"/api/project/1/tasks",
		"/api/project/1/runners/16",
	} {
		assert.False(t, RequiresDedicatedStateFixture(path), path)
	}
}
