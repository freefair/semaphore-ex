# Upstream role-set compatibility sync

## Objective

Merge recorded upstream `5f51971e28dfd931c9b15ce4c5cba321b9267b5e` into product develop while preserving EX authorization, immutable role identities, assignment ownership and published migration history. Publish ordinary product/Wiki branch updates after verification.

## Approach

1. Preserve recovery branch at `8fbde8ff4fa3fc8ff6d2100233b456da04cbc5c7`; retain assessment and unrelated-file fingerprints.
2. Resolve backend role-model, migration, membership, template-permission and export conflicts together. Adapt canonical incoming migrations to the existing EX identity/schema contract; preserve all shipped migrations. Dedicated Terra handles security-sensitive integration.
3. Resolve frontend role/team/template controls against the resulting API, preserving EX permission catalog, revisions, effective permission and global-assignment controls. Update API documentation and Wiki compatibility ADR.
4. Review integrated changes with Claude and Terra. Test migration forward/rollback on SQLite, PostgreSQL, MySQL and MariaDB, run complete retained gates with Node 24.19.0 and Go 1.26.9, and exercise desktop/mobile synthetic role workflows.
5. Commit verified changes, re-fetch origin, push normally, read back SHAs and wait for exact-commit Dev and Full Product Build. Clean up task-owned fixtures and retain evidence/recovery refs.

## Alternatives

- Replace EX role schema with upstream numeric identities: large downstream identity/provenance migration and risk to existing contracts; rejected.
- Retain one-role-only EX behavior: loses incoming upstream functionality; rejected.
- Add multiple membership roles around EX stable identities, retaining CAS, explicit deny and ownership semantics: selected, pending integrated evidence and review.

No release tags, deployments, shared runtime changes, provider changes, or unrelated plan changes are included.
