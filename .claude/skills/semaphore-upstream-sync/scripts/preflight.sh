#!/usr/bin/env bash

set -Eeuo pipefail

cleanup() {
  trap - SIGINT SIGTERM ERR EXIT
}

trap cleanup SIGINT SIGTERM ERR EXIT

usage() {
  cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") REPOSITORY_DIR

Optional read-only identity and slice check before an upstream merge.
Expects both root remotes to have been fetched. For the required retained
assessment, run tools/upstream-sync/preflight.sh from the repository root:
  bash tools/upstream-sync/preflight.sh --fetch --output NEW_DIRECTORY .

Example:
  $(basename "${BASH_SOURCE[0]}") /path/to/semaphore-ex
EOF
}

die() {
  printf 'error: %s\n' "$1" >&2
  exit 1
}

case "${1-}" in
  -h | --help)
    usage
    exit 0
    ;;
  '')
    die 'repository directory is required'
    ;;
esac

[[ "$#" -eq 1 ]] || die 'exactly one repository directory is required'
repository_dir="$(cd -- "$1" && pwd -P)"

git -C "${repository_dir}" rev-parse --is-inside-work-tree >/dev/null 2>&1 \
  || die 'directory is not a Git worktree'

branch="$(git -C "${repository_dir}" branch --show-current)"
[[ "${branch}" == 'develop' ]] || die "current branch is ${branch:-detached}, expected develop"

origin_url="$(git -C "${repository_dir}" remote get-url origin 2>/dev/null || true)"
upstream_url="$(git -C "${repository_dir}" remote get-url upstream 2>/dev/null || true)"
[[ "${origin_url}" == 'git@github.com:freefair/semaphore-ex.git' ]] \
  || die "unexpected origin URL: ${origin_url:-missing}"
[[ "${upstream_url}" == 'git@github.com:semaphoreui/semaphore.git' ]] \
  || die "unexpected upstream URL: ${upstream_url:-missing}"

git -C "${repository_dir}" rev-parse --verify origin/develop >/dev/null 2>&1 \
  || die 'origin/develop is unavailable; fetch origin first'
git -C "${repository_dir}" rev-parse --verify upstream/develop >/dev/null 2>&1 \
  || die 'upstream/develop is unavailable; fetch upstream first'

merge_base="$(git -C "${repository_dir}" merge-base origin/develop upstream/develop)"
[[ -n "${merge_base}" ]] || die 'origin/develop and upstream/develop have no merge base'

slice_dir="${repository_dir}/docs/docs/developer-guide/plans/pro-slices"
[[ -f "${slice_dir}/README.md" ]] || die 'slice index is unavailable'
[[ -f "${slice_dir}/upstream-maintenance.md" ]] || die 'upstream maintenance runbook is unavailable'

spec_count="$(find "${slice_dir}" -maxdepth 1 -type f -name '[0-9][0-9][0-9]-*.md' | wc -l | tr -d ' ')"
index_count="$(grep -Ec '^\| [0-9]{3} \| \[' "${slice_dir}/README.md")"
[[ "${spec_count}" == "${index_count}" ]] \
  || die "slice inventory mismatch: ${spec_count} specs, ${index_count} index entries"

if git -C "${repository_dir}/docs" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  docs_origin="$(git -C "${repository_dir}/docs" remote get-url origin 2>/dev/null || true)"
  docs_upstream="$(git -C "${repository_dir}/docs" remote get-url upstream 2>/dev/null || true)"
  case "${docs_origin}" in
    git@github.com:freefair/semaphore-docs | git@github.com:freefair/semaphore-docs.git) ;;
    *) die "unexpected docs origin URL: ${docs_origin:-missing}" ;;
  esac
  case "${docs_upstream}" in
    git@github.com:semaphoreui/semaphore-docs | git@github.com:semaphoreui/semaphore-docs.git) ;;
    *) die "unexpected docs upstream URL: ${docs_upstream:-missing}" ;;
  esac
fi

read -r upstream_only fork_only < <(
  git -C "${repository_dir}" rev-list --left-right --count upstream/develop...develop
)
dirty_entries="$(git -C "${repository_dir}" status --porcelain | wc -l | tr -d ' ')"

printf 'branch=%s\n' "${branch}"
printf 'head=%s\n' "$(git -C "${repository_dir}" rev-parse HEAD)"
printf 'origin_develop=%s\n' "$(git -C "${repository_dir}" rev-parse origin/develop)"
printf 'upstream_develop=%s\n' "$(git -C "${repository_dir}" rev-parse upstream/develop)"
printf 'merge_base=%s\n' "${merge_base}"
printf 'upstream_only=%s\n' "${upstream_only}"
printf 'fork_only=%s\n' "${fork_only}"
printf 'dirty_entries=%s\n' "${dirty_entries}"
printf 'slice_specs=%s\n' "${spec_count}"
