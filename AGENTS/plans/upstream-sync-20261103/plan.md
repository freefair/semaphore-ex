# Upstream session configuration compatibility

## Scope

Merge upstream `47f1d3b49663bb35bccc089c0ad27c5d88f9b759` into product
`develop`, starting at `6f7f7aae3cc9b48c5fbd0a69b9019f2d89ace28f`.
Publish the verified product and related Wiki changes through ordinary pushes.

## Approach

1. Retain fetched assessment, fingerprints of unrelated files and a recovery ref.
2. Resolve session configuration conflicts in `util/config*.go`; adopt configurable
   inactivity in API/service authentication while retaining nil safety, bounded
   durations, validation and compatibility for shipped operator configuration.
3. Review `db/Session*`, authentication tests, schema and exported-contract inventory.
   Keep the retired docs gitlink absent. Document decisions in Wiki ADR and guides.
4. Run focused security regressions, separate Terra and Claude reviews, and all
   retained gates using CI-pinned Go 1.26.10 and Node 24.19.0. Verify changed visible
   behavior in the browser if the final change affects a browser path.
5. Compare fresh origin tips, commit and push normally, read back exact SHAs and
   require successful Dev and Full Product Build runs for that product commit.
6. Clean task-owned fixtures and tool downloads; retain evidence and recovery refs.

## Alternatives

- Keep only old settings: minimizes code but loses upstream's configurable inactivity.
- Replace the fork configuration wholesale: smaller conflict resolution but risks
  losing shipped duration safety and operator settings.
- Integrate narrowly with compatibility and regression coverage: chosen because it
  preserves existing authentication contracts while delivering the upstream change.

## Boundaries

No deployments, release tags, history rewrites, shared runtime mutations, provider
repository changes, unrelated source edits or removal of existing recovery refs.
