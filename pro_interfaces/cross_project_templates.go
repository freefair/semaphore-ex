package pro_interfaces

import "github.com/semaphoreui/semaphore/db"

type CrossProjectTemplateGrantDenial string

const (
	CrossProjectTemplateGrantDeniedNone                  CrossProjectTemplateGrantDenial = ""
	CrossProjectTemplateGrantDeniedInvalidGrant          CrossProjectTemplateGrantDenial = "invalid_grant"
	CrossProjectTemplateGrantDeniedPending               CrossProjectTemplateGrantDenial = "pending"
	CrossProjectTemplateGrantDeniedRevoked               CrossProjectTemplateGrantDenial = "revoked"
	CrossProjectTemplateGrantDeniedScope                 CrossProjectTemplateGrantDenial = "scope"
	CrossProjectTemplateGrantDeniedConsumerAuthorization CrossProjectTemplateGrantDenial = "consumer_authorization"
)

// CrossProjectTemplateGrantRequest contains the server-derived identity and
// required operation at save, start, or runtime resolution. ConsumerAuthorized
// must be calculated by the caller from the current project permission model.
type CrossProjectTemplateGrantRequest struct {
	ConsumerProjectID  int
	TemplateID         int
	TemplateVersion    int
	Operation          db.CrossProjectTemplateGrantOperation
	ConsumerAuthorized bool
}

type CrossProjectTemplateGrantDecision struct {
	Allowed bool
	Reason  CrossProjectTemplateGrantDenial
}

// EvaluateCrossProjectTemplateGrant is intentionally pure so all entry points
// apply the same pending, revocation, scope, operation, and consumer-permission
// decisions before resolving an owner-side immutable snapshot.
func EvaluateCrossProjectTemplateGrant(
	grant db.CrossProjectTemplateGrant,
	request CrossProjectTemplateGrantRequest,
) CrossProjectTemplateGrantDecision {
	if grant.Validate() != nil {
		return CrossProjectTemplateGrantDecision{Reason: CrossProjectTemplateGrantDeniedInvalidGrant}
	}
	switch grant.Status {
	case db.CrossProjectTemplateGrantPending:
		return CrossProjectTemplateGrantDecision{Reason: CrossProjectTemplateGrantDeniedPending}
	case db.CrossProjectTemplateGrantRevoked:
		return CrossProjectTemplateGrantDecision{Reason: CrossProjectTemplateGrantDeniedRevoked}
	}
	if request.ConsumerProjectID != grant.ConsumerProjectID || request.TemplateID != grant.TemplateID ||
		request.TemplateVersion < grant.MinTemplateVersion || request.TemplateVersion > grant.MaxTemplateVersion ||
		!grant.Operations.Allows(request.Operation) {
		return CrossProjectTemplateGrantDecision{Reason: CrossProjectTemplateGrantDeniedScope}
	}
	if !request.ConsumerAuthorized {
		return CrossProjectTemplateGrantDecision{Reason: CrossProjectTemplateGrantDeniedConsumerAuthorization}
	}
	return CrossProjectTemplateGrantDecision{Allowed: true}
}
