package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type policyGuardrailGovernanceStub struct {
	scope     pro_interfaces.PolicyGuardrailScope
	projectID *int
	actorID   int
	save      struct {
		source   string
		expected int
	}
	publish           pro_interfaces.PolicyGuardrailPublishRequest
	rollback          pro_interfaces.PolicyGuardrailRollbackRequest
	draftRevision     int
	publishedRevision int
	rollbackRevision  int
	err               error
}

type policyGuardrailAuditRecorder struct {
	events []pro_interfaces.AuditEvent
	err    error
}

func (r *policyGuardrailAuditRecorder) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	r.events = append(r.events, event)
	return r.err
}

func (s *policyGuardrailGovernanceStub) remember(scope pro_interfaces.PolicyGuardrailScope, projectID *int) {
	s.scope, s.projectID = scope, projectID
}
func (s *policyGuardrailGovernanceStub) Get(_ context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int) (pro_interfaces.PolicyGuardrailDraftState, error) {
	s.remember(scope, projectID)
	return pro_interfaces.PolicyGuardrailDraftState{Draft: db.PolicyGuardrailDraft{Revision: 3}}, s.err
}
func (s *policyGuardrailGovernanceStub) SaveDraft(_ context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, source string, expected, actor int) (db.PolicyGuardrailDraft, error) {
	s.remember(scope, projectID)
	s.save.source, s.save.expected, s.actorID = source, expected, actor
	revision := expected + 1
	if s.draftRevision != 0 {
		revision = s.draftRevision
	}
	return db.PolicyGuardrailDraft{Revision: revision}, s.err
}
func (s *policyGuardrailGovernanceStub) Validate(_ context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, _ string) pro_interfaces.PolicyGuardrailValidationResult {
	s.remember(scope, projectID)
	return pro_interfaces.PolicyGuardrailValidationResult{Valid: true}
}
func (s *policyGuardrailGovernanceStub) TestFixture(_ context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, _ pro_interfaces.PolicyGuardrailFixtureRequest) (pro_interfaces.PolicyGuardrailEvaluation, error) {
	s.remember(scope, projectID)
	return pro_interfaces.PolicyGuardrailEvaluation{Allowed: true}, s.err
}
func (s *policyGuardrailGovernanceStub) Diff(_ context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, _, _ int) (pro_interfaces.PolicyGuardrailDiff, error) {
	s.remember(scope, projectID)
	return pro_interfaces.PolicyGuardrailDiff{FromRevision: 1, ToRevision: 2}, s.err
}
func (s *policyGuardrailGovernanceStub) Publish(_ context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, request pro_interfaces.PolicyGuardrailPublishRequest, actor int) (db.PolicyGuardrailRevision, error) {
	s.remember(scope, projectID)
	s.publish, s.actorID = request, actor
	revision := 4
	if s.publishedRevision != 0 {
		revision = s.publishedRevision
	}
	return db.PolicyGuardrailRevision{Revision: revision}, s.err
}
func (s *policyGuardrailGovernanceStub) Rollback(_ context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, request pro_interfaces.PolicyGuardrailRollbackRequest, actor int) (db.PolicyGuardrailRevision, error) {
	s.remember(scope, projectID)
	s.rollback, s.actorID = request, actor
	revision := 5
	if s.rollbackRevision != 0 {
		revision = s.rollbackRevision
	}
	return db.PolicyGuardrailRevision{Revision: revision}, s.err
}
func (s *policyGuardrailGovernanceStub) Impact(_ context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, _ pro_interfaces.PolicyGuardrailImpactRequest) (pro_interfaces.PolicyGuardrailImpactResult, error) {
	s.remember(scope, projectID)
	return pro_interfaces.PolicyGuardrailImpactResult{}, s.err
}
func (s *policyGuardrailGovernanceStub) Revisions(_ context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, _ db.RetrieveQueryParams) ([]db.PolicyGuardrailRevision, error) {
	s.remember(scope, projectID)
	return []db.PolicyGuardrailRevision{}, s.err
}
func (s *policyGuardrailGovernanceStub) Evaluations(_ context.Context, projectID *int, _ db.RetrieveQueryParams) ([]db.PolicyGuardrailEvaluationRecord, error) {
	s.projectID = projectID
	return []db.PolicyGuardrailEvaluationRecord{}, s.err
}

func policyGuardrailRequest(method, path, body string, projectID *int) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r = helpers.SetContextValue(r, "user", &db.User{ID: 11})
	if projectID != nil {
		r = helpers.SetContextValue(r, "project", db.Project{ID: *projectID})
	}
	return r
}

func TestPolicyGuardrailControllerUsesOnlyContextScopeForEveryOperation(t *testing.T) {
	stub := &policyGuardrailGovernanceStub{}
	controller := NewPolicyGuardrailController(stub)
	projectID := 7
	cases := []struct {
		method string
		path   string
		body   string
		h      func(http.ResponseWriter, *http.Request)
	}{
		{http.MethodGet, "/api/project/7/policy-guardrails", "", controller.Get},
		{http.MethodPut, "/api/project/7/policy-guardrails/draft", `{"source_yaml":"version: 1\nrules: []","expected_revision":3}`, controller.SaveDraft},
		{http.MethodPost, "/api/project/7/policy-guardrails/validate", `{"source_yaml":"version: 1\nrules: []"}`, controller.Validate},
		{http.MethodPost, "/api/project/7/policy-guardrails/test", policyFixtureJSON(projectID), controller.TestFixture},
		{http.MethodGet, "/api/project/7/policy-guardrails/diff?from_revision=1&to_revision=2", "", controller.Diff},
		{http.MethodPost, "/api/project/7/policy-guardrails/publish", `{"expected_draft_revision":3}`, controller.Publish},
		{http.MethodPost, "/api/project/7/policy-guardrails/impact", policyImpactJSON(projectID), controller.Impact},
		{http.MethodGet, "/api/project/7/policy-guardrails/revisions?count=10", "", controller.Revisions},
		{http.MethodGet, "/api/project/7/policy-guardrails/evaluations?count=10", "", controller.Evaluations},
		{http.MethodPost, "/api/project/7/policy-guardrails/rollback", `{"revision":2,"expected_draft_revision":3,"reason":"restore known-safe revision"}`, controller.Rollback},
	}
	for _, test := range cases {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.h(response, policyGuardrailRequest(test.method, test.path, test.body, &projectID))
			assert.Less(t, response.Code, http.StatusBadRequest, response.Body.String())
			assert.Equal(t, pro_interfaces.PolicyGuardrailScopeProject, stub.scope)
			require.NotNil(t, stub.projectID)
			assert.Equal(t, projectID, *stub.projectID)
		})
	}
	assert.Equal(t, "version: 1\nrules: []", stub.save.source)
	assert.Equal(t, 3, stub.save.expected)
	assert.Equal(t, 11, stub.actorID)
	assert.Equal(t, 2, stub.rollback.Revision)
}

func TestPolicyGuardrailControllerGlobalScopeAndTransportValidation(t *testing.T) {
	stub := &policyGuardrailGovernanceStub{}
	controller := NewPolicyGuardrailController(stub)

	response := httptest.NewRecorder()
	controller.Get(response, policyGuardrailRequest(http.MethodGet, "/api/policy-guardrails", "", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, pro_interfaces.PolicyGuardrailScopeGlobal, stub.scope)
	assert.Nil(t, stub.projectID)
	response = httptest.NewRecorder()
	controller.Evaluations(response, policyGuardrailRequest(http.MethodGet, "/api/policy-guardrails/evaluations?count=10", "", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Nil(t, stub.projectID, "global evaluation history must not synthesize a tenant filter")

	for _, body := range []string{
		`{"source_yaml":"version: 1\nrules: []","expected_revision":3,"project_id":8}`,
		`{"source_yaml":"version: 1\nrules: []","expected_revision":3,"unexpected":true}`,
		`{"source_yaml":"version: 1\nrules: []","expected_revision":0}`,
		`{"source_yaml":"version: 1\nrules: []"} {}`,
	} {
		response = httptest.NewRecorder()
		controller.SaveDraft(response, policyGuardrailRequest(http.MethodPut, "/api/policy-guardrails/draft", body, nil))
		assert.Equal(t, http.StatusBadRequest, response.Code, body)
	}

	response = httptest.NewRecorder()
	controller.Diff(response, policyGuardrailRequest(http.MethodGet, "/api/policy-guardrails/diff?from_revision=0&to_revision=2", "", nil))
	assert.Equal(t, http.StatusBadRequest, response.Code)

	stub.err = db.ErrPolicyGuardrailDraftRevisionConflict
	response = httptest.NewRecorder()
	controller.Publish(response, policyGuardrailRequest(http.MethodPost, "/api/policy-guardrails/publish", `{"expected_draft_revision":3}`, nil))
	assert.Equal(t, http.StatusConflict, response.Code)

	stub.err = errors.New("internal source must not be leaked")
	response = httptest.NewRecorder()
	controller.Rollback(response, policyGuardrailRequest(http.MethodPost, "/api/policy-guardrails/rollback", `{"revision":2,"expected_draft_revision":3,"reason":"restore known-safe revision"}`, nil))
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.NotContains(t, response.Body.String(), "internal source")
}

func TestPolicyGuardrailControllerAuditsOnlyBoundedGovernanceProvenance(t *testing.T) {
	stub := &policyGuardrailGovernanceStub{}
	controller := NewPolicyGuardrailController(stub)
	audit := &policyGuardrailAuditRecorder{}
	controller.(pro_interfaces.PolicyGuardrailAuditConfigurer).ConfigurePolicyGuardrailAudit(audit)
	projectID := 7
	policySource := "version: 1\nrules: []\n# must-never-reach-audit"
	response := httptest.NewRecorder()
	controller.SaveDraft(response, policyGuardrailRequest(http.MethodPut, "/api/project/7/policy-guardrails/draft", `{"source_yaml":`+strconv.Quote(policySource)+`,"expected_revision":3}`, &projectID))
	require.Equal(t, http.StatusOK, response.Code)
	require.Len(t, audit.events, 1)
	event := audit.events[0]
	require.NoError(t, event.Validate())
	assert.Equal(t, pro_interfaces.AuditActionPolicyGuardrailDraftSave, event.Action)
	assert.Equal(t, pro_interfaces.AuditTargetPolicyGuardrail, event.TargetType)
	assert.Equal(t, "project:7", event.TargetID)
	require.NotNil(t, event.PolicyGuardrailProvenance)
	assert.Equal(t, pro_interfaces.PolicyGuardrailScopeProject, event.PolicyGuardrailProvenance.Scope)
	assert.NotContains(t, fmt.Sprint(event), policySource)
	assert.NotContains(t, fmt.Sprint(event.SafeFields()), policySource)

	response = httptest.NewRecorder()
	controller.Rollback(response, policyGuardrailRequest(http.MethodPost, "/api/project/7/policy-guardrails/rollback", `{"revision":2,"expected_draft_revision":3,"reason":"restore known-safe revision"}`, &projectID))
	require.Equal(t, http.StatusCreated, response.Code)
	require.Len(t, audit.events, 2)
	assert.Equal(t, pro_interfaces.AuditActionPolicyGuardrailRollback, audit.events[1].Action)
	require.NoError(t, audit.events[1].Validate())

	response = httptest.NewRecorder()
	controller.Publish(response, policyGuardrailRequest(http.MethodPost, "/api/project/7/policy-guardrails/publish", `{"expected_draft_revision":0}`, &projectID))
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Len(t, audit.events, 3)
	assert.Equal(t, pro_interfaces.AuditOutcomeDenied, audit.events[2].Outcome)
	assert.Equal(t, pro_interfaces.AuditReasonInvalidInput, audit.events[2].Reason)
	require.NoError(t, audit.events[2].Validate())
}

func TestPolicyGuardrailControllerRejectsOversizedAndCrossTenantFixtures(t *testing.T) {
	controller := NewPolicyGuardrailController(&policyGuardrailGovernanceStub{})
	projectID := 7
	oversized := `{"source_yaml":"` + strings.Repeat("x", pro_interfaces.MaxPolicyGuardrailYAMLBytes+16*1024+1) + `","expected_revision":1}`
	response := httptest.NewRecorder()
	controller.SaveDraft(response, policyGuardrailRequest(http.MethodPut, "/api/project/7/policy-guardrails/draft", oversized, &projectID))
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)

	response = httptest.NewRecorder()
	controller.TestFixture(response, policyGuardrailRequest(http.MethodPost, "/api/project/7/policy-guardrails/test", policyFixtureJSON(8), &projectID))
	assert.Equal(t, http.StatusBadRequest, response.Code)
	response = httptest.NewRecorder()
	controller.Impact(response, policyGuardrailRequest(http.MethodPost, "/api/project/7/policy-guardrails/impact", policyImpactJSON(8), &projectID))
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestPolicyGuardrailControllerRejectsUnboundedRevisionProtocolFields(t *testing.T) {
	stub := &policyGuardrailGovernanceStub{}
	controller := NewPolicyGuardrailController(stub)
	audit := &policyGuardrailAuditRecorder{}
	controller.(pro_interfaces.PolicyGuardrailAuditConfigurer).ConfigurePolicyGuardrailAudit(audit)
	projectID := 7
	overflow := pro_interfaces.AuditPolicyGuardrailRevisionMax + 1
	mutations := []struct {
		name string
		path string
		body string
		h    func(http.ResponseWriter, *http.Request)
	}{
		{"draft expected", "/api/project/7/policy-guardrails/draft", fmt.Sprintf(`{"source_yaml":"version: 1\nrules: []","expected_revision":%d}`, overflow), controller.SaveDraft},
		{"publish expected", "/api/project/7/policy-guardrails/publish", fmt.Sprintf(`{"expected_draft_revision":%d}`, overflow), controller.Publish},
		{"rollback revision", "/api/project/7/policy-guardrails/rollback", fmt.Sprintf(`{"revision":%d,"expected_draft_revision":3,"reason":"restore known-safe revision"}`, overflow), controller.Rollback},
		{"rollback expected", "/api/project/7/policy-guardrails/rollback", fmt.Sprintf(`{"revision":3,"expected_draft_revision":%d,"reason":"restore known-safe revision"}`, overflow), controller.Rollback},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.h(response, policyGuardrailRequest(http.MethodPost, test.path, test.body, &projectID))
			assert.Equal(t, http.StatusBadRequest, response.Code)
		})
	}
	require.Len(t, audit.events, len(mutations))
	for _, event := range audit.events {
		assert.Equal(t, pro_interfaces.AuditOutcomeDenied, event.Outcome)
		assert.Equal(t, pro_interfaces.AuditReasonInvalidInput, event.Reason)
		require.NoError(t, event.Validate())
	}

	response := httptest.NewRecorder()
	controller.Diff(response, policyGuardrailRequest(http.MethodGet, fmt.Sprintf("/api/project/7/policy-guardrails/diff?from_revision=%d&to_revision=1", overflow), "", &projectID))
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestPolicyGuardrailControllerFailsClosedOnUnboundedServiceRevision(t *testing.T) {
	projectID := 7
	overflow := pro_interfaces.AuditPolicyGuardrailRevisionMax + 1
	cases := []struct {
		name  string
		path  string
		body  string
		setup func(*policyGuardrailGovernanceStub)
		h     func(pro_interfaces.PolicyGuardrailController, http.ResponseWriter, *http.Request)
	}{
		{
			name: "draft", path: "/api/project/7/policy-guardrails/draft", body: `{"source_yaml":"version: 1\nrules: []","expected_revision":3}`,
			setup: func(stub *policyGuardrailGovernanceStub) { stub.draftRevision = overflow },
			h: func(controller pro_interfaces.PolicyGuardrailController, w http.ResponseWriter, r *http.Request) {
				controller.SaveDraft(w, r)
			},
		},
		{
			name: "publish", path: "/api/project/7/policy-guardrails/publish", body: `{"expected_draft_revision":3}`,
			setup: func(stub *policyGuardrailGovernanceStub) { stub.publishedRevision = overflow },
			h: func(controller pro_interfaces.PolicyGuardrailController, w http.ResponseWriter, r *http.Request) {
				controller.Publish(w, r)
			},
		},
		{
			name: "rollback", path: "/api/project/7/policy-guardrails/rollback", body: `{"revision":2,"expected_draft_revision":3,"reason":"restore known-safe revision"}`,
			setup: func(stub *policyGuardrailGovernanceStub) { stub.rollbackRevision = overflow },
			h: func(controller pro_interfaces.PolicyGuardrailController, w http.ResponseWriter, r *http.Request) {
				controller.Rollback(w, r)
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			stub := &policyGuardrailGovernanceStub{}
			test.setup(stub)
			controller := NewPolicyGuardrailController(stub)
			audit := &policyGuardrailAuditRecorder{}
			controller.(pro_interfaces.PolicyGuardrailAuditConfigurer).ConfigurePolicyGuardrailAudit(audit)
			response := httptest.NewRecorder()
			test.h(controller, response, policyGuardrailRequest(http.MethodPost, test.path, test.body, &projectID))
			assert.Equal(t, http.StatusInternalServerError, response.Code)
			require.Len(t, audit.events, 1)
			event := audit.events[0]
			assert.Equal(t, pro_interfaces.AuditOutcomeFailure, event.Outcome)
			assert.Equal(t, pro_interfaces.AuditReasonOperationError, event.Reason)
			require.NoError(t, event.Validate())
		})
	}
}

func TestPolicyGuardrailControllerAcceptsBoundedFixtureBeyondDraftEnvelope(t *testing.T) {
	controller := NewPolicyGuardrailController(&policyGuardrailGovernanceStub{})
	projectID := 7
	sourcePrefix := "version: 1\nrules: []\n#"
	source := sourcePrefix + strings.Repeat("x", pro_interfaces.MaxPolicyGuardrailYAMLBytes-len(sourcePrefix))
	fixture := pro_interfaces.PolicyGuardrailFixtureRequest{
		SourceYAML: source,
		Input: pro_interfaces.PolicyGuardrailEvaluationInput{
			ProjectID: projectID, Intent: pro_interfaces.ExecutionPreflightTask,
			EvaluatedAt: time.Date(2026, time.September, 3, 0, 0, 0, 0, time.UTC),
			Template:    &pro_interfaces.PolicyGuardrailTemplateMetadata{ID: 1, Application: "ansible", Source: "manual"},
			Runner:      pro_interfaces.PolicyGuardrailRunnerMetadata{RequestedTags: repeatedPolicyFixtureValues(256, 256)},
			Executor:    pro_interfaces.PolicyGuardrailExecutorMetadata{Type: "local", ImageReferenceKind: "none"},
		},
	}
	body, err := json.Marshal(fixture)
	require.NoError(t, err)
	require.Greater(t, len(body), pro_interfaces.MaxPolicyGuardrailYAMLBytes+16*1024)
	response := httptest.NewRecorder()
	controller.TestFixture(response, policyGuardrailRequest(http.MethodPost, "/api/project/7/policy-guardrails/test", string(body), &projectID))
	assert.Equal(t, http.StatusOK, response.Code)
}

func repeatedPolicyFixtureValues(count, length int) []string {
	values := make([]string, count)
	for index := range values {
		values[index] = strings.Repeat("x", length)
	}
	return values
}

func policyFixtureJSON(projectID int) string {
	return `{"source_yaml":"version: 1\nrules: []","input":{"project_id":` + string(rune('0'+projectID)) + `,"intent":"task","evaluated_at":"2026-01-01T00:00:00Z","template":{"id":1,"application":"ansible","source":"manual"},"executor":{"type":"local","image_reference_kind":"none"}}}`
}

func policyImpactJSON(projectID int) string {
	return `{"inputs":[{"project_id":` + string(rune('0'+projectID)) + `,"intent":"task","evaluated_at":"2026-01-01T00:00:00Z","template":{"id":1,"application":"ansible","source":"manual"},"executor":{"type":"local","image_reference_kind":"none"}}]}`
}
