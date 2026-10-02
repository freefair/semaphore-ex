# Proposed ADR: Preserve operational causes across error boundaries

Status: Proposed; assessment only, no runtime behavior changed.

## Context
Independent backend boundaries erase causes, misclassify infrastructure failures as client errors or policy decisions, and sometimes report success after persistence fails. The existing request-correlation middleware is not consistently connected to error logs or response bodies. Authentication and secret-provider adapters also need to keep secrets out of responses and logs.

## Options
1. Return or log every raw error. Small change, but directory responses, URLs, SQL parameters, configuration and provider payloads can contain confidential values. Not suitable as a general policy.
2. Add an individual string or log at every current failure. Quick for LDAP, but inconsistent status codes, missing correlation and repeated cause destruction remain.
3. Preserve causal chains internally and translate them at explicit boundaries into safe, structured diagnostic records. More implementation work, but maintains error identity, useful input feedback, dependency diagnostics and public-response policies.

## Recommendation
Adopt option 3 in focused, independently verified changes:

- Wrap the original cause at each meaningful operation boundary; preserve `errors.Is`/`errors.As`. A stable category or sentinel must not replace the original cause. Keep safe presentation separate from raw `Error()` strings.
- Give API failures stable codes, safe actionable messages, request IDs, and field paths for validation failures. Classify invalid input, not found, conflicts, dependency failures and internal failures independently.
- Use the existing `X-Request-ID`/correlation context. Record the operation, safe technical reason, relevant resource IDs, dependency status/result code and request ID at the handling boundary. Avoid duplicate logs at every layer.
- Provide administrators useful setup diagnostics: failed stage and precise safe protocol/transport reason, including certificate validation, DNS, service-bind rejection, search base/filter and LDAP result codes.
- Preserve intentionally non-enumerating public authentication/authorization responses while retaining a correlated operator diagnostic. Do not weaken authorization merely to improve diagnostics.
- Background loops must handle returned failures explicitly and expose operator-visible failure state; counters alone do not explain a failed operation.
- Do not return success after a required write fails. Explicitly report partial completion where an operation is non-transactional. Keep a primary failure and a failed status/audit write distinguishable.
- Treat cleanup and best-effort operations deliberately: document the fallback, retain a diagnostic when operationally useful, and avoid converting cleanup failures into false success of a required operation.

## Verification contract
For each migrated area inject an input error, a missing resource, a conflict and an infrastructure failure. Assert the HTTP classification or worker outcome, preserved cause, operator-visible diagnostic and correlation. For confidential inputs assert that credentials and arbitrary provider payloads are absent. Validate changed UI in the browser. Existing tests that only assert a generic error code are insufficient.

## Compatibility
Changing generic HTTP 400 responses to 500/503 is observable API behavior. Update API specifications, callers and contract tests with each change. Keep existing stable codes where possible and add diagnostic fields. No database migration is assumed necessary; determine persistence requirements from each worker's existing state model.
