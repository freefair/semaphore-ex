# Repair Git SSH command construction

## Scope

Fix duplicated SSH executables and empty known-hosts paths in the existing Git command builders on `develop`.
Preserve the configured host-key policy and its `no` default.
Remote deployment and publication require a separate request.

## Implementation

1. Reproduce the duplicated executable and empty known-hosts option before changing executable source.
2. In `pkg/ssh/agent.go`, share host-key options between `GetGitEnv` and `TaskGitSSHCommand`, return options only, and use `<TmpPath>/known_hosts` when `yes` or `accept-new` has no explicit path.
3. Cover both builders and all three modes in `pkg/ssh/agent_test.go`, including explicit paths, fallback paths, command argument parsing, and unchanged identity selection.
4. Correct the `util/config.go` field documentation and regenerate references with `task docs:gen`.
5. Record the decision in ADR 0017 and update the product status.
6. Run SSH and affected caller tests, inspect the final diff, and commit the docs and application changes locally.

## Decision

An options-only shared helper fixes both call paths without changing host authentication policy.
Implement the documented temporary-directory fallback instead of requiring new configuration for existing installations.
Changing the default to `accept-new` would change first-use trust behavior and would not itself repair the empty filename.
Fixing only the extra executable would leave the two stricter modes broken when their path is omitted.
