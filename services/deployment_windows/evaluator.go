// Package deployment_windows contains the pure, backend-authoritative policy
// evaluator. Durable admission, authorization, and audit persistence are
// intentionally outside this package and must invoke it transactionally.
package deployment_windows

import (
	"errors"
	"sort"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const defaultMaxEvaluationOperations = 10_000

type Evaluator struct {
	validateTimezone        db.ValidateTimezoneFunc
	maxEvaluationOperations int
}

type Option func(*Evaluator)

func WithTimezoneValidator(validate db.ValidateTimezoneFunc) Option {
	return func(evaluator *Evaluator) { evaluator.validateTimezone = validate }
}

// WithMaxNextEligibleMinutes remains a compatibility no-op: the evaluator
// deliberately jumps between policy transitions instead of scanning minutes.
func WithMaxNextEligibleMinutes(_ int) Option { return func(*Evaluator) {} }

func WithMaxEvaluationOperations(limit int) Option {
	return func(evaluator *Evaluator) { evaluator.maxEvaluationOperations = limit }
}

func NewEvaluator(options ...Option) *Evaluator {
	evaluator := &Evaluator{maxEvaluationOperations: defaultMaxEvaluationOperations}
	for _, option := range options {
		if option != nil {
			option(evaluator)
		}
	}
	return evaluator
}

func (evaluator *Evaluator) Evaluate(policy db.DeploymentWindowPolicy, request pro_interfaces.DeploymentWindowEvaluationRequest) (pro_interfaces.DeploymentWindowDecision, error) {
	if evaluator == nil || evaluator.validateTimezone == nil || evaluator.maxEvaluationOperations < 1 ||
		policy.Validate(evaluator.validateTimezone) != nil || request.Validate() != nil || policy.ProjectID != request.ProjectID {
		return pro_interfaces.DeploymentWindowDecision{}, errors.New("deployment window evaluation is invalid")
	}
	location, err := time.LoadLocation(policy.Timezone)
	if err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, errors.New("deployment window evaluation is invalid")
	}
	rules, err := compileApplicableRules(policy.Rules, request)
	if err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, errors.New("deployment window evaluation is invalid")
	}
	at := request.At.UTC()
	budget := &evaluationBudget{remaining: evaluator.maxEvaluationOperations}
	state, reason, active, err := evaluateAt(policy, rules, location, at, budget)
	if err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	provenance := decisionProvenance(policy, at, active)
	if request.Override != nil && state == pro_interfaces.DeploymentWindowDecisionBlocked {
		provenance.OverrideActorID = request.Override.ActorID
		return pro_interfaces.DeploymentWindowDecision{State: pro_interfaces.DeploymentWindowDecisionOverridden, Reason: pro_interfaces.DeploymentWindowReasonOverride, NextEligibleKnown: true, Provenance: provenance}, nil
	}
	decision := pro_interfaces.DeploymentWindowDecision{State: state, Reason: reason, NextEligibleKnown: state != pro_interfaces.DeploymentWindowDecisionBlocked, Provenance: provenance}
	if state == pro_interfaces.DeploymentWindowDecisionBlocked {
		next, known, nextErr := nextEligible(policy, rules, location, at, budget)
		if nextErr != nil {
			return pro_interfaces.DeploymentWindowDecision{}, nextErr
		}
		decision.NextEligibleAt, decision.NextEligibleKnown = next, known
	}
	return decision, nil
}

type compiledRule struct {
	rule     db.DeploymentWindowRule
	schedule cron.Schedule
}

type activeRule struct {
	compiled compiledRule
	endsAt   time.Time
}

func compileApplicableRules(rules []db.DeploymentWindowRule, request pro_interfaces.DeploymentWindowEvaluationRequest) ([]compiledRule, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for _, rule := range rules {
		if !rule.Active || !ruleApplies(rule, request) {
			continue
		}
		schedule, err := cron.ParseStandard(rule.Recurrence)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, compiledRule{rule: rule, schedule: schedule})
	}
	return compiled, nil
}

func evaluateAt(policy db.DeploymentWindowPolicy, rules []compiledRule, location *time.Location, at time.Time, budget *evaluationBudget) (pro_interfaces.DeploymentWindowDecisionState, pro_interfaces.DeploymentWindowReason, []activeRule, error) {
	active := make([]activeRule, 0, len(rules))
	hasFreeze, hasAllow := false, false
	for _, rule := range rules {
		current, err := activeAt(rule, location, at, budget)
		if err != nil {
			return "", "", nil, err
		}
		if current == nil {
			continue
		}
		active = append(active, *current)
		if rule.rule.Kind == db.DeploymentWindowFreeze {
			hasFreeze = true
		} else {
			hasAllow = true
		}
	}
	if hasFreeze {
		return pro_interfaces.DeploymentWindowDecisionBlocked, pro_interfaces.DeploymentWindowReasonFreezeActive, active, nil
	}
	if hasAllow {
		return pro_interfaces.DeploymentWindowDecisionAllowed, pro_interfaces.DeploymentWindowReasonAllowWindow, active, nil
	}
	if policy.Default == db.DeploymentWindowDefaultAllow {
		return pro_interfaces.DeploymentWindowDecisionAllowed, pro_interfaces.DeploymentWindowReasonDefaultAllow, active, nil
	}
	return pro_interfaces.DeploymentWindowDecisionBlocked, pro_interfaces.DeploymentWindowReasonDefaultDeny, active, nil
}

// nextEligible advances only to a rule start or active-rule end. It reaches
// sparse monthly/quarterly windows without a fixed time horizon and never does
// a minute×rule scan. Exhausting the explicit operation budget is reported as
// unknown, never as a false assertion that no future window exists.
func nextEligible(policy db.DeploymentWindowPolicy, rules []compiledRule, location *time.Location, at time.Time, budget *evaluationBudget) (*time.Time, bool, error) {
	candidate := at
	for {
		state, _, active, err := evaluateAt(policy, rules, location, candidate, budget)
		if err != nil {
			if errors.Is(err, errEvaluationBudgetExceeded) {
				return nil, false, nil
			}
			return nil, false, err
		}
		if candidate.After(at) && state == pro_interfaces.DeploymentWindowDecisionAllowed {
			value := candidate.UTC()
			return &value, true, nil
		}
		next, found, err := nextTransition(policy, rules, active, location, candidate, budget)
		if err != nil {
			if errors.Is(err, errEvaluationBudgetExceeded) {
				return nil, false, nil
			}
			return nil, false, err
		}
		if !found {
			return nil, true, nil
		}
		candidate = next
	}
}

func nextTransition(policy db.DeploymentWindowPolicy, rules []compiledRule, active []activeRule, location *time.Location, at time.Time, budget *evaluationBudget) (time.Time, bool, error) {
	var next time.Time
	consider := func(candidate time.Time) {
		if candidate.After(at) && (next.IsZero() || candidate.Before(next)) {
			next = candidate
		}
	}
	for _, rule := range rules {
		// A freeze cannot make a deny-default policy eligible, and a policy
		// with an allow default reaches this function only while a freeze is
		// active. In both cases its future starts cannot be the next allow.
		if policy.Default != db.DeploymentWindowDefaultDeny || rule.rule.Kind != db.DeploymentWindowAllow {
			continue
		}
		if err := budget.consume(); err != nil {
			return time.Time{}, false, err
		}
		consider(nextRuleStart(rule, location, at))
	}
	for _, rule := range active {
		consider(rule.endsAt.UTC())
	}
	return next, !next.IsZero(), nil
}

// nextRuleStart skips recurrence instances outside a rule's local effective
// range. Without this bound an expired allow rule can make a deny-default
// policy walk every future recurrence until its operation budget is exhausted.
func nextRuleStart(rule compiledRule, location *time.Location, at time.Time) time.Time {
	probe := at.In(location)
	if rule.rule.EffectiveFrom != nil {
		from, err := time.ParseInLocation("2006-01-02", *rule.rule.EffectiveFrom, location)
		if err == nil && probe.Before(from) {
			probe = from.Add(-time.Nanosecond)
		}
	}
	next := rule.schedule.Next(probe)
	if rule.rule.EffectiveUntil != nil && next.In(location).Format("2006-01-02") > *rule.rule.EffectiveUntil {
		return time.Time{}
	}
	return next.UTC()
}

func activeAt(rule compiledRule, location *time.Location, at time.Time, budget *evaluationBudget) (*activeRule, error) {
	localAt := at.In(location)
	lower, upper := effectiveSearchRange(rule.rule, location, localAt)
	occurrence, found, err := lastOccurrence(rule.schedule, lower, upper, budget)
	if err != nil || !found {
		return nil, err
	}
	endsAt := occurrence.Add(time.Duration(rule.rule.DurationMinutes) * time.Minute)
	if effectiveDate(rule.rule, occurrence.In(location)) && !localAt.Before(occurrence) && localAt.Before(endsAt) {
		return &activeRule{compiled: rule, endsAt: endsAt}, nil
	}
	return nil, nil
}

func effectiveSearchRange(rule db.DeploymentWindowRule, location *time.Location, at time.Time) (time.Time, time.Time) {
	lower, upper := at.Add(-time.Duration(rule.DurationMinutes)*time.Minute), at
	if rule.EffectiveFrom != nil {
		if start, err := time.ParseInLocation("2006-01-02", *rule.EffectiveFrom, location); err == nil && start.After(lower) {
			lower = start
		}
	}
	if rule.EffectiveUntil != nil {
		if endDate, err := time.ParseInLocation("2006-01-02", *rule.EffectiveUntil, location); err == nil {
			end := endDate.AddDate(0, 0, 1).Add(-time.Nanosecond)
			if end.Before(upper) {
				upper = end
			}
		}
	}
	return lower, upper
}

// lastOccurrence locates the last cron occurrence in [lower, upper] through
// monotonic schedule probes. Five-field schedules have minute precision, so a
// logarithmic search plus a tiny final adjustment replaces the former linear
// walk over every dense recurrence in a window duration.
func lastOccurrence(schedule cron.Schedule, lower, upper time.Time, budget *evaluationBudget) (time.Time, bool, error) {
	if upper.Before(lower) {
		return time.Time{}, false, nil
	}
	if err := budget.consume(); err != nil {
		return time.Time{}, false, err
	}
	first := schedule.Next(lower.Add(-time.Nanosecond))
	if first.After(upper) {
		return time.Time{}, false, nil
	}
	lo, hi := lower, upper
	for hi.Sub(lo) > time.Minute {
		mid := lo.Add(hi.Sub(lo) / 2)
		if err := budget.consume(); err != nil {
			return time.Time{}, false, err
		}
		next := schedule.Next(mid)
		if !next.After(upper) {
			lo = next
		} else {
			hi = mid
		}
	}
	last := schedule.Next(lo.Add(-time.Nanosecond))
	if last.After(upper) {
		return time.Time{}, false, nil
	}
	for range 3 {
		if err := budget.consume(); err != nil {
			return time.Time{}, false, err
		}
		next := schedule.Next(last)
		if next.After(upper) {
			break
		}
		last = next
	}
	return last, true, nil
}

func decisionProvenance(policy db.DeploymentWindowPolicy, at time.Time, active []activeRule) pro_interfaces.DeploymentWindowDecisionProvenance {
	ids := make([]int, 0, len(active))
	rules := make([]pro_interfaces.DeploymentWindowMatchedRule, 0, len(active))
	for _, rule := range active {
		ids = append(ids, rule.compiled.rule.ID)
		rules = append(rules, pro_interfaces.DeploymentWindowMatchedRule{ID: rule.compiled.rule.ID, Revision: rule.compiled.rule.Revision, Kind: rule.compiled.rule.Kind})
	}
	sort.Ints(ids)
	sort.Slice(rules, func(left, right int) bool { return rules[left].ID < rules[right].ID })
	return pro_interfaces.DeploymentWindowDecisionProvenance{PolicyRevision: policy.Revision, EffectiveTimezone: policy.Timezone, EvaluatedAt: at, MatchedRuleIDs: ids, MatchedRules: rules}
}

func ruleApplies(rule db.DeploymentWindowRule, request pro_interfaces.DeploymentWindowEvaluationRequest) bool {
	switch rule.Scope {
	case db.DeploymentWindowProjectScope:
		return true
	case db.DeploymentWindowTemplateScope:
		return rule.TemplateID != nil && *rule.TemplateID == request.TemplateID
	case db.DeploymentWindowWorkflowScope:
		return rule.WorkflowID != nil && *rule.WorkflowID == request.WorkflowID
	default:
		return false
	}
}

var errEvaluationBudgetExceeded = errors.New("deployment window evaluation budget exceeded")

type evaluationBudget struct{ remaining int }

func (budget *evaluationBudget) consume() error {
	if budget == nil || budget.remaining <= 0 {
		return errEvaluationBudgetExceeded
	}
	budget.remaining--
	return nil
}

func effectiveDate(rule db.DeploymentWindowRule, at time.Time) bool {
	date := at.Format("2006-01-02")
	return (rule.EffectiveFrom == nil || date >= *rule.EffectiveFrom) && (rule.EffectiveUntil == nil || date <= *rule.EffectiveUntil)
}
