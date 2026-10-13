# Workflow output compatibility

## Scope

Merge upstream `55e065f856f15310cc9c67d36b3cbfb16665ff43` into the verified audit merge `d536dd6638c83ec3c165405de346cd8e92aed96c`, then publish both changes with ordinary pushes. Preserve unrelated plans, the separate LDAP journal and existing recovery points.

## Approach

1. Retain the post-audit assessment and a recovery branch; merge the exact upstream SHA without committing.
2. Preserve existing EX artifact declarations, immutable snapshots, encryption, permissions and HA lifecycle fencing while integrating upstream output capture and edge input mapping. Implement every selected factory explicitly in the Enhanced module. Retain existing APIs and map incoming fields through durable definition/run boundaries.
3. Preserve shipped migration 2.20.10 byte-for-byte and introduce the required additional columns through an anchored EX compatibility migration. Append ownership entries, review changed seam inventories and record behavior tests. An in-place rewrite would break the published migration contract; dropping upstream behavior would leave visible controls ineffective.
4. Retain incoming secret-safe Ansible execution and runner validation with dedicated Terra review and regression coverage. Keep helper tooling changes restricted to incoming upstream files; no unrelated refactoring.
5. Run focused integration and migration checks, independent Claude review, the complete retained gate runner and desktop/mobile browser verification. Adapt documentation in the existing independent Wiki checkout and document the compatibility decision.
6. Commit verified changes using configured signing; fetch origins before ordinary authorized pushes, confirm remote heads and required exact-commit CI. Preserve evidence and restore task-owned temporary state.

## HA schema identity compatibility

Exact-head CI and a local clean-room image reproduce a PostgreSQL startup failure:
the combined schema identity is 33 characters, while `cluster__node.schema_version`
still has the shipped 32-character limit. Fresh migrations themselves succeed.

1. Add EX migration `2.20.11-ex1.2` to widen storage without changing published
   migration files or shortening either namespace identity. Retain the wider
   storage on rollback if narrowing would lose registration data.
2. Cover the actual current identity in registry persistence and the supported
   database migration matrix, including round-trip preservation.
3. Record the storage and rollback decision in the independent Wiki ADR; review
   the narrow delta with Terra and Claude.
4. Verify disposable multi-node HA startup and fault scenarios, run the retained
   gates, then publish normally and require both exact-head CI workflows.

Shortening or truncating the identity would weaken exact schema comparison.
Editing the historical table-creation migration would violate migration
immutability. An additive widening preserves both contracts.
