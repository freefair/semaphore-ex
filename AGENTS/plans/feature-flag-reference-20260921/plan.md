# Feature-Flag Reference Corrections

## Objective

Resolve findings F1–F12 from the feature-flag audit through accurate documentation.
Keep application behavior, configuration tags, defaults, permissions, and lifecycle enforcement unchanged.
The application baseline is `afdcfc67`; the documentation baseline is `b9aeae3`.

## Approach

1. Correct field comments in `util/config.go`, `config_auth.go`, `config_ex.go`, `App.go`, `LdapProvider.go`, and `OdbcProvider.go` against selected runtime behavior.
2. Extend `tools/docsref` to display boolean zero defaults and boolean members of named configuration maps.
   Keep map objects as the settable environment entries and explicitly avoid inventing per-entry environment variables.
   Preserve pointer-bool unset semantics and explicit tagged defaults.
3. Correct authentication, logging, configuration, and lifecycle guides, including a reference-linked map of process settings to persisted feature controls.
   Remove unsupported IdP-initiated recipes from Azure, Keycloak, and Okta guides and their ten translations while preserving valid provider configuration and stable section anchors.
4. Regenerate the configuration reference through the project task, remove superseded description fallbacks, and check all twelve findings against the generated output.
5. Run focused generator tests, reference reproducibility, structural checks, the serial eleven-locale documentation build and canonical fallback checks.
   Verify rendered reference tables and links and obtain a Terra review of security-related guidance.
6. Commit the verified docs and application changes locally and present the concrete result for publication approval.

## Alternatives and scope

Hand-editing the generated table would lose the fixes on regeneration.
Adding runtime implementations for compatibility-only flags would change product behavior and is outside this documentation request.
Expanding every credential and provider object member would broaden this feature-flag correction; the generator exposes their boolean members while the parent object and provider guides retain the rest of the schema.
Existing canonical-English fallbacks keep reviewed security guidance consistent across locales.
TLS hosting and dependency alerts are separate follow-ups.

## Evidence

Retain checks and finding-by-finding closure under `/tmp/semaphore-feature-flag-doc-fix-20260921/`.
Preserve the original audit and append its resolution status after verification.
