package pro_interfaces

import "net/http"

// PolicyGuardrailController is the replaceable HTTP boundary for global and
// project policy governance. Community retains the complete route shape but
// keeps it unavailable; Enhanced supplies the transport-only implementation.
type PolicyGuardrailController interface {
	Get(http.ResponseWriter, *http.Request)
	SaveDraft(http.ResponseWriter, *http.Request)
	Validate(http.ResponseWriter, *http.Request)
	TestFixture(http.ResponseWriter, *http.Request)
	Diff(http.ResponseWriter, *http.Request)
	Publish(http.ResponseWriter, *http.Request)
	Impact(http.ResponseWriter, *http.Request)
	Revisions(http.ResponseWriter, *http.Request)
	Evaluations(http.ResponseWriter, *http.Request)
	Rollback(http.ResponseWriter, *http.Request)
}

// PolicyGuardrailAuditConfigurer is an optional process-wiring seam. The
// controller records only bounded scope and revision provenance and never
// copies policy source or fixture data into audit records.
type PolicyGuardrailAuditConfigurer interface {
	ConfigurePolicyGuardrailAudit(AuditServiceFacade)
}
