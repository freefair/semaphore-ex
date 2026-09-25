# Explicit API response booleans

## Contract

Concrete API response booleans retain both values in JSON. A missing property
is not a replacement for `false`. Explicit optional booleans retain omission
only when absence has a separate contract meaning.

## Implementation sequence

1. Reproduce the missing `empty: false` field and inspect actual key read DTOs.
2. Change `Empty` and `Synchronized` tags in `db.AccessKey` and the shared safe
   key response DTO; leave request-only key controls and private material intact.
   Compute `Empty` when reading stored keys and before generation results lose
   private material; the safe response DTO preserves that derived value.
3. Cover false and true, list/detail/generated responses, and secret redaction.
4. Document required response fields in both OpenAPI entry documents; record
   the rule and its rationale in the Wiki. No database schema changes are needed.
5. Run relevant model/API tests, specification bundling and maintenance checks;
   obtain a Claude review and commit verified changes.

## Broader audit

Other public responses also omit concrete false values, including template,
workflow, project, view, LDAP/OIDC and execution-review fields. This correction
establishes the access-key contract; the wider change is a separate follow-up.
Template/workflow structures are also used in immutable stored snapshots and
fingerprints; changing their serialization requires a separate compatibility
decision or response DTO boundary. Request/config-only and optional pointer
fields are not part of a public response fix.

## Alternatives

A caller fallback fixes one checker but leaves ambiguous server responses.
Changing a global JSON encoder would affect unrelated payloads and custom
serialization. Correct the actual response contract instead; preserve stored
schema and secrets.
