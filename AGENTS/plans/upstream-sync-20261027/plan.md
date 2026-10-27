# Upstream sync: numeric HTTP listen ports

## Scope and evidence

Merge assessed upstream d1044fb3b194f359e34f6d28a102aa13734d0b9e into product develop at 9a2ee87ba9daca0596f7a50619a589f68b0fa326. Two upstream commits add integer HTTP ports. Preserve the 119 unrelated fork-history-rebuild files, independent migration namespaces, Enhanced selection, and the Wiki-only documentation contract. Evidence: /tmp/semaphore-sync-20261027.

## Approach

1. Retain recovery ref, then merge the recorded SHA without committing. Preserve the removed docs gitlink; inspect incoming documentation separately.
2. Review util/config.go, util/config_test.go, cli/cmd/root.go, and config.schema.yaml. Verify EX consumers of Config.Port and add only compatibility regressions justified by findings.
3. Select Go 1.26.9 and Node 24.19.0 explicitly. Run focused port decoding/default/environment/listener tests and all retained verification gates. Check the real product listener on task-owned loopback fixtures; no UI files change.
4. Obtain independent Claude review and dedicated Terra security review of the immutable final patch. Update Configuration and maintenance/status Wiki pages with an ADR for the compatibility decision.
5. Recheck origin tips, sign and commit verified changes, push develop and Wiki master normally, verify remote equality and required exact-commit CI. Clean task-owned fixtures and archive the journal.

## Alternatives

Adopt upstream ListenPort with focused EX compatibility fixes: preserves upstream shape and minimizes future merge cost; requires all callers to use its explicit string/address conversion. Reimplement integer parsing downstream: avoids a public type change but duplicates upstream behavior and increases future conflicts. Choose upstream shape after consumer and runtime verification. Preserve docs removal under existing Wiki policy rather than recreating a submodule.
