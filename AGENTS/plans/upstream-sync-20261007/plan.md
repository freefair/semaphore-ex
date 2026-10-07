# Upstream Audit and Repository Compatibility

## Scope

Merge upstream `48141755c6042a8d687234e7e746bffaac6f5f69` into the existing
`develop` lineage at `02ed1cd4b8ba8c819b2fe06e6b79534cb1085190` and publish
the verified product and relevant English Wiki documentation with ordinary pushes.
Preserve migration identities, permissions, HA ownership, credential redaction,
and the independent Wiki. The recovery branch is `codex/pre-sync-20261007`.

## Approach

1. Retain the incoming Git checkout validation, browse serialization, debug logger,
   and IPv4-mapped trusted-proxy fixes. Review the new debug output against the
   fork's credential-redaction contract; add focused compatibility regressions.
2. Implement the incoming Splunk HEC configuration in the selected Enhanced audit
   exporter. Reuse durable per-destination cursors and HA leasing, preserve TLS
   validation, bound requests, reject redirects, and acknowledge only confirmed
   delivery while ownership remains valid. Keep Syslog and HEC independent.
3. Keep the removed docs submodule and reference generator absent. Adapt relevant
   upstream documentation into the product Wiki, including an architecture
   decision. Remove the incoming commercial label from the HEC schema.
4. Update only applicable behavior-test references in `maintenance/contracts.yml`.
   Run focused regressions, dedicated Terra review and independent Claude review,
   then all retained verification gates on stable final source.
5. Fetch origin immediately before ordinary publication, compare with the assessed
   tip, verify the remote SHA after pushing, and inspect exact-commit CI.
   Restore unrelated files and original build inputs and retain evidence.

## Alternatives

- Accept the new HEC configuration without an exporter: minimal diff, but silently
  loses upstream behavior in the selected implementation. Rejected.
- Replace the existing exporter wholesale: one implementation, but unnecessarily
  risks its established Syslog and HA contracts. Prefer a focused HEC transport
  and narrow integration with existing lifecycle behavior.
- Restore upstream docs tooling: textually easy, but contradicts the maintained
  Wiki-only architecture. Keep its deletion and update the Wiki independently.

## Evidence

Assessment, review reports, logs, fingerprints and checksums are retained under
`/tmp/semaphore-full-sync-20261007`. The SSH agent failed authentication; upstream
and origin refs were freshly fetched from their verified public HTTPS URLs before
the retained assessment, without changing stored remotes. The automation memory
records execution state; the unrelated backend/LDAP journal remains untouched.
