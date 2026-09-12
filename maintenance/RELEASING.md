# Releasing Semaphore EX

A release is a tag on a green `develop` head, a CHANGELOG section, signed archives and
packages on GitHub Releases, and container images on the GitHub Container Registry.
The tag triggers everything; nothing is built or published by hand.

## Version scheme

`vX.Y.Z-ex.N`: `X.Y.Z` is the upstream Semaphore UI line the head is based on
(the next upstream release the merged `develop` precedes or matches), `N` counts fork
releases on that line, starting at 1. Release candidates are `vX.Y.Z-ex.N-rcM`.

The suffix keeps fork tags out of upstream's tag namespace; a plain `vX.Y.Z` tag would
collide with the upstream tag of the same name and break `git fetch upstream`.

Package versions follow nfpm's mapping of the semver pre-release: `v2.20.0-ex.1` becomes
deb `2.20.0~ex.1` and rpm `2.20.0~ex.1`, both below upstream `2.20.0`. A release candidate
becomes `2.20.0~ex.1-rc1` (rpm `2.20.0~ex.1_rc1`), which dpkg and rpm consider *newer* than
the final package because the `-rc1` part lands in the revision slot. goreleaser does not
template the nfpm `prerelease` field, so this cannot be rewritten at build time: treat rc
packages as verification artifacts and remove them (`apt remove semaphore-ex`) before
installing the final package on the same host.

| Tag | Workflow | GitHub release | Images |
|---|---|---|---|
| `v2.20.0-ex.1-rc1` | `Full Product Beta` | draft, marked pre-release | `:v2.20.0-ex.1-rc1` |
| `v2.20.0-ex.1` | `Full Product Release` | draft, final | `:v2.20.0-ex.1` and `:latest` |
| manual run of `Full Product Beta` | dry run | none; signed snapshot as workflow artifact | built, not pushed |

Both workflows first run `Full Product Build` as a gate (tests, reproducible double build,
browser smoke, container smoke, HA resilience) and only then build the release.

## Before tagging

1. `develop` head: `Dev` and `Full Product Build` are green on exactly that SHA
   (`gh run list --branch develop --limit 4`).
2. `CHANGELOG.md` has a `## [vX.Y.Z-ex.N]` section. `bash tools/release-notes.sh vX.Y.Z-ex.N`
   must succeed; the release job fails without it.
3. Dry run in GitHub Actions: `gh workflow run "Full Product Beta" --ref develop`. It runs the
   full gate, imports the signing key, builds a signed goreleaser snapshot and uploads it as
   the `release-dry-run-<sha>` artifact, and builds both images without pushing. Download
   the artifact and verify the checksum signature. (`task release:test` is the unsigned local
   equivalent; it builds every target in parallel, so throttle it with `GOFLAGS=-p=4`.)
4. Go toolchain current: `GOTOOLCHAIN=go<pinned> go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
   reports no reachable findings. The pin lives in the workflows and Dockerfiles.
5. `npm audit --omit=dev --prefix web` shows only the documented inherited findings.

## Release candidate

```bash
git tag -a v2.20.0-ex.1-rc1 -m "Semaphore EX v2.20.0-ex.1-rc1"
git push origin v2.20.0-ex.1-rc1
gh run watch --repo freefair/semaphore-ex
```

Verify the candidate:

```bash
gh release view v2.20.0-ex.1-rc1 --repo freefair/semaphore-ex
gh release download v2.20.0-ex.1-rc1 --repo freefair/semaphore-ex --pattern 'semaphore-ex_*checksums.txt*'
gpg --import deployment/packaging/semaphore-ex-release.asc
gpg --verify semaphore-ex_*_checksums.txt.sig semaphore-ex_*_checksums.txt
docker run --rm ghcr.io/freefair/semaphore-ex:v2.20.0-ex.1-rc1 semaphore version
docker run --rm ghcr.io/freefair/semaphore-ex-runner:v2.20.0-ex.1-rc1 semaphore version
```

Start the server image with SQLite, log in, and check `/api/info` reports
`"edition": "enhanced"` and the tag as version. Install the `deb` in a clean `debian:stable`
container and run `semaphore version`. Open the login page in a browser.

## Final release

Tag the same commit with the final version and push. When the workflow is green, review
the draft release on GitHub (assets, notes, pre-release flag off) and publish it:

```bash
gh release edit v2.20.0-ex.1 --repo freefair/semaphore-ex --draft=false
```

Move the `[Unreleased]` entries of `CHANGELOG.md` into the next section afterwards.

## Secrets and the release key

The release workflows use repository secrets `GPG_KEY_3` (base64 of the armored private
key), `GPG_PASS_3` (passphrase) and the repository variable `GPG_KEY_ID_3`. Container
images are pushed with the workflow's `GITHUB_TOKEN`; no registry secret exists.

The signing key `Semaphore EX Release <semaphore-ex@freefair.io>` is an Ed25519 key with
a two-year expiry. The private key and passphrase are stored in 1Password
(vault `Infrastructure`, items "Semaphore EX release GPG"). The public key is committed as
`deployment/packaging/semaphore-ex-release.asc`.

Rotation: generate a new key the same way (ephemeral `GNUPGHOME`, passphrase from
1Password, never printed), replace the three GitHub values, commit the new public key,
and mention the rotation in `CHANGELOG.md`. Old releases stay verifiable with the old
public key; keep it in the repository history.

## Repository rules

Rulesets on `freefair/semaphore-ex`: `develop` cannot be deleted; tags matching `v*` cannot
be updated or deleted. Force pushes to `develop` remain possible because the upstream
maintenance procedure occasionally needs them.
