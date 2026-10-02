# LDAP setup test diagnostics

Status: Preliminary implementation proposal, deferred while the requested backend-wide assessment is completed. No changes described here have been implemented. See `plan.md`, `report.md` and `decision.md` for the expanded scope and findings.

## Goal
Expose the failed setup operation and actionable technical cause to authenticated administrators without changing public login errors or including credentials and directory-controlled diagnostic strings.

## Approach
1. Preserve typed protocol/transport errors in the identity adapter and derive credential-free diagnostics from their types and LDAP result codes.
2. Return the diagnostic and existing readiness state from the administrative test endpoint; log that safe diagnostic.
3. Display the diagnostic and failed readiness state in the existing panel.
4. Verify identity, API and UI regressions, build the frontend, inspect the rendered failure, and check maintenance contracts.

## Decision
Use typed, safe diagnostics rather than raw error interpolation (which can disclose directory data) or generic readiness codes alone (which do not identify certificate, DNS or bind failures). Keep error/status compatibility and the existing LDAPReadiness DTO. Add no database schema or authentication policy changes. Unknown errors retain a safe operation-specific fallback; original causes remain available through Go error unwrapping.

## Scope
Current local develop checkout. No deployment, remote configuration changes, push, or modification of unrelated fork-history plans. Live LDAP root cause cannot be established without the affected instance.
