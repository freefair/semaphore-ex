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
