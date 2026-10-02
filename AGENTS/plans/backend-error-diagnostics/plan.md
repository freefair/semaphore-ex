# Backend error diagnostics assessment

## Request and scope
Investigate the LDAP setup failure `LDAP_PROVIDER_UNAVAILABLE`, then inspect the entire backend for the same loss of error causes, ignored failures and misleading aggregate responses.

The assessment covers the root backend and the selected Enhanced implementation in `test/edition-contract/enhanced`, including HTTP APIs, identity adapters, secret providers, database access, workers, task execution, runners, HA, imports and CLI. The local branch is `develop`; the reviewed source commit is recorded in the report.

## Sequence
1. Trace the LDAP setup operation from protocol adapter through service, API and UI.
2. Inventory all non-test Go source and build-selected packages. Scan error branches, ignored results, generic error writers and compound guards; follow candidate call chains.
3. Distinguish cause destruction, missing operator diagnostics, incorrect classification and false success. Exclude safe cleanup, infallible operations, named error returns and cases already logged by a lower layer.
4. Reproduce representative defects using synthetic dependencies, an in-memory database and a disposable local TLS endpoint.
5. Produce a prioritized report and a proposed error-handling contract. Keep implementation work explicit and separate from the requested assessment.

## Decisions
- Use a source-wide inventory plus targeted failure injection. A grep count alone is not a defect count, and normal success-path tests do not verify diagnosability.
- Keep the existing product checkout and branch. Do not alter deployments, real LDAP configuration, credentials, authentication policy or published history.
- Review both modules: the root `pro` tree contains compatibility implementations and is not the selected full-product backend.
- Keep audit artifacts under `AGENTS`; do not publish unfinished implementation guidance to the product Wiki.
- Request the prescribed Claude second opinion. Its CLI returned `Not logged in`; record that review as unavailable and continue the independent local investigation.

## Implementation status
No executable product source has been changed. The LDAP repair and backend remediation remain open. `report.md` describes confirmed evidence and `decision.md` proposes the shared contract needed for coherent fixes.
