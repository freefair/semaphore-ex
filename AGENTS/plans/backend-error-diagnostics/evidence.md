# Error diagnostics reproduction evidence

These are characterization checks against source commit `152a50ca8e6c44273a505e006977e918c184d48f`. PASS means the reported defect was reproduced, not repaired. All dependencies were synthetic, localhost-only, or in-memory.

## Executed checks

| Case | Observed result |
|---|---|
| Internal database error through `helpers.WriteError` | HTTP 400, empty body; cause only in an uncorrelated server log |
| JSON field type error through `helpers.Bind` | HTTP 400, empty body |
| Global credential controller database failure | HTTP 500, `Global credential service failure`; zero logger bytes |
| Audit webhook controller database failure | HTTP 500, `Audit webhook operation failed`; zero logger bytes |
| Notification controller database failure | HTTP 503, `Notification governance is unavailable`; zero logger bytes |
| LDAP test with `service_bind_failed` readiness | HTTP 503, only `LDAP_PROVIDER_UNAVAILABLE`; readiness and stage absent; zero logger bytes |
| LDAP against a self-signed local TLS endpoint | `LDAP provider unavailable: connect`; certificate cause unavailable through `errors.As` |
| Notification worker claim failure | Claim attempted; zero logger bytes; normal worker close |
| Docker policy insert with missing table | Actual SQL error: `no such table: docker_execution_policy`; returned error: `Docker execution policy revision conflict` |
| Project restore with failed alias insert | Created project returned, nil error, zero logger bytes |

## Commands and outcomes

Temporary harness directory: `/tmp/semaphore-error-audit/repro`.

```text
GOCACHE=/tmp/semaphore-error-audit/go-cache go test /tmp/semaphore-error-audit/repro/audit_test.go -v -count=1
PASS
ok command-line-arguments 0.496s

GOCACHE=/tmp/semaphore-error-audit/go-cache go test /tmp/semaphore-error-audit/repro/audit_test.go /tmp/semaphore-error-audit/repro/workers_test.go -run 'TestAuditNotificationWorker|TestAuditMissingDatabase' -v -count=1
PASS
ok command-line-arguments 1.296s

GOCACHE=/tmp/semaphore-error-audit/go-cache go test /tmp/semaphore-error-audit/repro/audit_test.go /tmp/semaphore-error-audit/repro/restore_test.go -run TestAuditRestore -v -count=1
PASS
ok command-line-arguments 0.603s
```

The first attempt of the first command failed only when `httptest` attempted a local listener inside the sandbox (`bind: operation not permitted`). Repeating the same test command with local listener permission produced the passing result above. The six preceding non-listener cases had already reproduced their findings in the first run.

`go list` succeeded with a writable temporary `GOCACHE`; it emitted a nonfatal module stat-cache write warning for the restricted default module cache. Its output enumerated 65 packages and 608 unique build-selected files.

The source parser completed successfully:

```text
Parsed 679 non-test Go files; 2235 syntactic candidates (not confirmed defects).
```

The harnesses are retained as `.go.txt` research attachments beside this file. Copy them to the temporary paths above to rerun from the repository root. They are deliberately outside the product's Go test discovery: their assertions describe broken current behavior and must be inverted/adapted into regression tests during remediation.

The TLS listener and worker were closed by their tests; the database lived only in memory. No external service was modified.
