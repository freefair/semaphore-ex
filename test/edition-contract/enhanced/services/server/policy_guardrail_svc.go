package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type policyGuardrailAdmissionService struct {
	repository pro_interfaces.PolicyGuardrailAdmissionRepository
}

var _ pro_interfaces.PolicyGuardrailAdmissionService = (*policyGuardrailAdmissionService)(nil)

func NewPolicyGuardrailAdmissionService(repository pro_interfaces.PolicyGuardrailAdmissionRepository) pro_interfaces.PolicyGuardrailAdmissionService {
	return &policyGuardrailAdmissionService{repository: repository}
}

func (s *policyGuardrailAdmissionService) EvaluatePolicyGuardrails(input pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error) {
	if s == nil || s.repository == nil || input.Validate() != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
	}
	return s.repository.PreviewPolicyGuardrails(input, evaluatePolicyGuardrailRevisions)
}
func (s *policyGuardrailAdmissionService) ClaimPolicyGuardrailEvaluation(request pro_interfaces.PolicyGuardrailAdmissionRequest) (pro_interfaces.PolicyGuardrailEvaluationClaim, error) {
	if s == nil || s.repository == nil || request.Input.Validate() != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.ErrInvalidOperation
	}
	return s.repository.ClaimPolicyGuardrailEvaluation(request, evaluatePolicyGuardrailRevisions)
}

func evaluatePolicyGuardrailRevisions(revisions []db.PolicyGuardrailRevision, input pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error) {
	global, project, err := compiledPoliciesFromRevisions(revisions, input.ProjectID)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	evaluator, err := NewPolicyGuardrailEvaluator(global, project)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	return evaluator.EvaluatePolicyGuardrails(input)
}

func compiledPoliciesFromRevisions(revisions []db.PolicyGuardrailRevision, projectID int) (*PolicyGuardrailCompiledPolicy, *PolicyGuardrailCompiledPolicy, error) {
	var global, project *PolicyGuardrailCompiledPolicy
	for index, revision := range revisions {
		if index > 1 || revision.CompilerVersion != pro_interfaces.PolicyGuardrailCompilerVersion || revision.Validate() != nil {
			return nil, nil, db.ErrInvalidOperation
		}
		scope := pro_interfaces.PolicyGuardrailScope(revision.Scope)
		if (scope == pro_interfaces.PolicyGuardrailScopeGlobal && (global != nil || project != nil)) || (scope == pro_interfaces.PolicyGuardrailScopeProject && (project != nil || revision.ProjectID == nil || *revision.ProjectID != projectID)) || (scope != pro_interfaces.PolicyGuardrailScopeGlobal && scope != pro_interfaces.PolicyGuardrailScopeProject) {
			return nil, nil, db.ErrInvalidOperation
		}
		document, err := decodePolicyGuardrailDocument(revision.CompiledJSON)
		if err != nil {
			return nil, nil, db.ErrInvalidOperation
		}
		policy, err := NewCompiledPolicyGuardrailPolicy(scope, revision.ProjectID, revision.Revision, document)
		if err != nil || policy.RevisionRef().Fingerprint != revision.Fingerprint {
			return nil, nil, db.ErrInvalidOperation
		}
		if scope == pro_interfaces.PolicyGuardrailScopeGlobal {
			global = policy
		} else {
			project = policy
		}
	}
	return global, project, nil
}

func decodePolicyGuardrailDocument(value string) (pro_interfaces.PolicyGuardrailDocument, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	var document pro_interfaces.PolicyGuardrailDocument
	if err := decoder.Decode(&document); err != nil {
		return pro_interfaces.PolicyGuardrailDocument{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return pro_interfaces.PolicyGuardrailDocument{}, errors.New("multiple compiled documents")
	}
	if err := document.Validate(); err != nil {
		return pro_interfaces.PolicyGuardrailDocument{}, err
	}
	return canonicalPolicyGuardrailDocument(document), nil
}

type policyGuardrailGovernanceService struct {
	repository pro_interfaces.PolicyGuardrailGovernanceRepository
}

var _ pro_interfaces.PolicyGuardrailGovernanceServiceFacade = (*policyGuardrailGovernanceService)(nil)

func NewPolicyGuardrailGovernanceService(repository pro_interfaces.PolicyGuardrailGovernanceRepository) pro_interfaces.PolicyGuardrailGovernanceServiceFacade {
	return &policyGuardrailGovernanceService{repository: repository}
}

func (s *policyGuardrailGovernanceService) Get(ctx context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int) (pro_interfaces.PolicyGuardrailDraftState, error) {
	if s == nil || s.repository == nil || ctx == nil || ctx.Err() != nil {
		return pro_interfaces.PolicyGuardrailDraftState{}, db.ErrInvalidOperation
	}
	draft, err := s.repository.GetPolicyGuardrailDraft(scope, projectID)
	if err != nil {
		return pro_interfaces.PolicyGuardrailDraftState{}, err
	}
	state := pro_interfaces.PolicyGuardrailDraftState{Draft: draft}
	if draft.ActiveRevision != nil {
		revision, getErr := s.repository.GetPolicyGuardrailRevision(scope, projectID, *draft.ActiveRevision)
		if getErr != nil {
			return state, getErr
		}
		state.Active = &revision
	}
	return state, nil
}
func (s *policyGuardrailGovernanceService) SaveDraft(ctx context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, source string, expected, actor int) (db.PolicyGuardrailDraft, error) {
	if s == nil || s.repository == nil || ctx == nil || ctx.Err() != nil {
		return db.PolicyGuardrailDraft{}, db.ErrInvalidOperation
	}
	return s.repository.SavePolicyGuardrailDraft(scope, projectID, source, expected, actor)
}
func (s *policyGuardrailGovernanceService) Validate(ctx context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, source string) pro_interfaces.PolicyGuardrailValidationResult {
	if ctx == nil || ctx.Err() != nil {
		return policyGuardrailInvalidResult()
	}
	document, fingerprint, err := CompilePolicyGuardrailYAML([]byte(source))
	if err != nil {
		return policyGuardrailInvalidResult()
	}
	return pro_interfaces.PolicyGuardrailValidationResult{Valid: true, Fingerprint: fingerprint, RuleCount: len(document.Rules)}
}
func policyGuardrailInvalidResult() pro_interfaces.PolicyGuardrailValidationResult {
	return pro_interfaces.PolicyGuardrailValidationResult{Valid: false, Issues: []pro_interfaces.PolicyGuardrailValidationIssue{{Code: "invalid_policy", Message: "Policy YAML is invalid."}}}
}
func (s *policyGuardrailGovernanceService) TestFixture(ctx context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, request pro_interfaces.PolicyGuardrailFixtureRequest) (pro_interfaces.PolicyGuardrailEvaluation, error) {
	if ctx == nil || ctx.Err() != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
	}
	doc, _, err := CompilePolicyGuardrailYAML([]byte(request.SourceYAML))
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
	}
	policy, err := NewCompiledPolicyGuardrailPolicy(scope, projectID, 1, doc)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
	}
	e, err := NewPolicyGuardrailEvaluator(mapPolicyScope(scope, policy), mapProjectScope(scope, policy))
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	return e.EvaluatePolicyGuardrails(request.Input)
}
func mapPolicyScope(scope pro_interfaces.PolicyGuardrailScope, p *PolicyGuardrailCompiledPolicy) *PolicyGuardrailCompiledPolicy {
	if scope == pro_interfaces.PolicyGuardrailScopeGlobal {
		return p
	}
	return nil
}
func mapProjectScope(scope pro_interfaces.PolicyGuardrailScope, p *PolicyGuardrailCompiledPolicy) *PolicyGuardrailCompiledPolicy {
	if scope == pro_interfaces.PolicyGuardrailScopeProject {
		return p
	}
	return nil
}
func (s *policyGuardrailGovernanceService) Diff(ctx context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, from, to int) (pro_interfaces.PolicyGuardrailDiff, error) {
	if s == nil || s.repository == nil || ctx == nil || ctx.Err() != nil || from <= 0 || to <= 0 {
		return pro_interfaces.PolicyGuardrailDiff{}, db.ErrInvalidOperation
	}
	a, e := s.repository.GetPolicyGuardrailRevision(scope, projectID, from)
	if e != nil {
		return pro_interfaces.PolicyGuardrailDiff{}, e
	}
	b, e := s.repository.GetPolicyGuardrailRevision(scope, projectID, to)
	if e != nil {
		return pro_interfaces.PolicyGuardrailDiff{}, e
	}
	ad, e := decodePolicyGuardrailDocument(a.CompiledJSON)
	if e != nil {
		return pro_interfaces.PolicyGuardrailDiff{}, db.ErrInvalidOperation
	}
	bd, e := decodePolicyGuardrailDocument(b.CompiledJSON)
	if e != nil {
		return pro_interfaces.PolicyGuardrailDiff{}, db.ErrInvalidOperation
	}
	return policyGuardrailDiff(from, to, ad, bd), nil
}
func policyGuardrailDiff(from, to int, a, b pro_interfaces.PolicyGuardrailDocument) pro_interfaces.PolicyGuardrailDiff {
	am, bm := map[string]pro_interfaces.PolicyGuardrailRule{}, map[string]pro_interfaces.PolicyGuardrailRule{}
	for _, r := range a.Rules {
		am[r.ID] = r
	}
	for _, r := range b.Rules {
		bm[r.ID] = r
	}
	d := pro_interfaces.PolicyGuardrailDiff{FromRevision: from, ToRevision: to}
	for id, r := range bm {
		if old, ok := am[id]; !ok {
			d.Added = append(d.Added, id)
		} else if !policyRulesEqual(old, r) {
			d.Changed = append(d.Changed, id)
		}
	}
	for id := range am {
		if _, ok := bm[id]; !ok {
			d.Removed = append(d.Removed, id)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Changed)
	return d
}
func policyRulesEqual(a, b pro_interfaces.PolicyGuardrailRule) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func (s *policyGuardrailGovernanceService) Publish(ctx context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, request pro_interfaces.PolicyGuardrailPublishRequest, actor int) (db.PolicyGuardrailRevision, error) {
	if s == nil || s.repository == nil || ctx == nil || ctx.Err() != nil {
		return db.PolicyGuardrailRevision{}, db.ErrInvalidOperation
	}
	draft, e := s.repository.GetPolicyGuardrailDraft(scope, projectID)
	if e != nil {
		return db.PolicyGuardrailRevision{}, e
	}
	doc, fp, e := CompilePolicyGuardrailYAML([]byte(draft.SourceYAML))
	if e != nil {
		return db.PolicyGuardrailRevision{}, db.ErrInvalidOperation
	}
	compiled, e := json.Marshal(doc)
	if e != nil {
		return db.PolicyGuardrailRevision{}, e
	}
	return s.repository.PublishPolicyGuardrailRevision(scope, projectID, request.ExpectedDraftRevision, actor, draft.SourceYAML, string(compiled), fp, pro_interfaces.PolicyGuardrailCompilerVersion)
}
func (s *policyGuardrailGovernanceService) Rollback(ctx context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, request pro_interfaces.PolicyGuardrailRollbackRequest, actor int) (db.PolicyGuardrailRevision, error) {
	if s == nil || s.repository == nil || ctx == nil || ctx.Err() != nil {
		return db.PolicyGuardrailRevision{}, db.ErrInvalidOperation
	}
	return s.repository.RollbackPolicyGuardrailRevision(scope, projectID, request.Revision, request.ExpectedDraftRevision, actor, request.Reason)
}
func (s *policyGuardrailGovernanceService) Impact(ctx context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, request pro_interfaces.PolicyGuardrailImpactRequest) (pro_interfaces.PolicyGuardrailImpactResult, error) {
	if s == nil || s.repository == nil || ctx == nil || ctx.Err() != nil || len(request.Inputs) > 100 {
		return pro_interfaces.PolicyGuardrailImpactResult{}, db.ErrInvalidOperation
	}
	draft, e := s.repository.GetPolicyGuardrailDraft(scope, projectID)
	if e != nil {
		return pro_interfaces.PolicyGuardrailImpactResult{}, e
	}
	doc, _, e := CompilePolicyGuardrailYAML([]byte(draft.SourceYAML))
	if e != nil {
		return pro_interfaces.PolicyGuardrailImpactResult{}, db.ErrInvalidOperation
	}
	p, e := NewCompiledPolicyGuardrailPolicy(scope, projectID, 1, doc)
	if e != nil {
		return pro_interfaces.PolicyGuardrailImpactResult{}, e
	}
	ev, e := NewPolicyGuardrailEvaluator(mapPolicyScope(scope, p), mapProjectScope(scope, p))
	if e != nil {
		return pro_interfaces.PolicyGuardrailImpactResult{}, e
	}
	out := pro_interfaces.PolicyGuardrailImpactResult{}
	for _, in := range request.Inputs {
		x, err := ev.EvaluatePolicyGuardrails(in)
		if err != nil {
			return out, err
		}
		out.Evaluations = append(out.Evaluations, x)
		if x.Allowed {
			out.Allowed++
		} else {
			out.Denied++
		}
	}
	return out, nil
}
func (s *policyGuardrailGovernanceService) Revisions(ctx context.Context, scope pro_interfaces.PolicyGuardrailScope, projectID *int, params db.RetrieveQueryParams) ([]db.PolicyGuardrailRevision, error) {
	if s == nil || s.repository == nil || ctx == nil || ctx.Err() != nil {
		return nil, db.ErrInvalidOperation
	}
	return s.repository.GetPolicyGuardrailRevisions(scope, projectID, params)
}
func (s *policyGuardrailGovernanceService) Evaluations(ctx context.Context, projectID *int, params db.RetrieveQueryParams) ([]db.PolicyGuardrailEvaluationRecord, error) {
	if s == nil || s.repository == nil || ctx == nil || ctx.Err() != nil {
		return nil, db.ErrInvalidOperation
	}
	return s.repository.GetPolicyGuardrailEvaluationHistory(projectID, params)
}
