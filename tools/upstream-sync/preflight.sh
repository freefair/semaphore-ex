#!/usr/bin/env bash
# Assess both forks without changing their worktrees, indexes, or branch tips.
set -Eeuo pipefail

usage() {
  cat <<'HELP'
Usage: preflight.sh [--fetch] --output NEW_DIRECTORY REPOSITORY_DIR

Capture exact root/docs refs, upstream seam and migration diffs, fork changes,
and merge conflict previews. Cached refs are used unless --fetch is supplied.
The output directory must be new. Conflict previews are evidence, not approval.

Example:
  bash tools/upstream-sync/preflight.sh --fetch --output /tmp/semaphore-review .
HELP
}

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
cleanup() { trap - EXIT INT TERM; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

check_remote() {
  local repository="$1" remote="$2" expected="$3" actual
  actual="$(git -C "$repository" remote get-url "$remote")"
  [[ "${actual%.git}" == "${expected%.git}" ]] || die "Unexpected $remote identity in $repository"
}

capture_repository() {
  local repository="$1" label="$2" branch="$3" output="$4" fetch_refs="$5"
  local head origin upstream base object_dir result fingerprint_before fingerprint_after
  [[ "$(git -C "$repository" branch --show-current)" == "$branch" ]] || die "$label must be on $branch"
  if [[ "$fetch_refs" == true ]]; then
    GIT_SSH_COMMAND='ssh -o IdentitiesOnly=yes' git -C "$repository" fetch origin "$branch"
    GIT_SSH_COMMAND='ssh -o IdentitiesOnly=yes' git -C "$repository" fetch upstream "$branch"
  fi
  head="$(git -C "$repository" rev-parse --verify 'HEAD^{commit}')"
  origin="$(git -C "$repository" rev-parse --verify "origin/$branch^{commit}")"
  upstream="$(git -C "$repository" rev-parse --verify "upstream/$branch^{commit}")"
  base="$(git -C "$repository" merge-base "$head" "$upstream")"
  [[ -n "$base" ]] || die "$label has unrelated history"
  mkdir -p "$output/$label/objects"
  {
    printf 'head: %s\norigin: %s\nupstream: %s\nmerge_base: %s\n' "$head" "$origin" "$upstream" "$base"
    printf 'branch: %s\nfetched: %s\n' "$branch" "$fetch_refs"
    printf 'upstream_only: %s\nfork_only: %s\n' \
      "$(git -C "$repository" rev-list --count "$head..$upstream")" \
      "$(git -C "$repository" rev-list --count "$upstream..$head")"
  } > "$output/$label/refs.yml"
  git -C "$repository" status --porcelain=v1 > "$output/$label/worktree-status.txt"
  # Bind dirty, staged input too. Personal journals and generated mirrors are not product inputs.
  git -C "$repository" diff --no-ext-diff --binary HEAD -- . ':(exclude).claude/task-notes' ':(exclude).claude/rules' > "$output/$label/working-tree.patch"
  fingerprint_before="$(shasum -a 256 "$output/$label/working-tree.patch" | awk '{print $1}')"
  printf '%s\n' "$fingerprint_before" > "$output/$label/working-tree.sha256"
  git -C "$repository" diff --no-ext-diff --name-status "$base" "$upstream" > "$output/$label/upstream-files.txt"
  git -C "$repository" diff --no-ext-diff --stat "$upstream" "$head" > "$output/$label/fork-delta.txt"
  if [[ "$label" == root ]]; then
    git -C "$repository" diff --no-ext-diff "$base" "$upstream" -- pro pro_interfaces 'db/*_pro.go' > "$output/$label/upstream-seams.patch"
    git -C "$repository" diff --no-ext-diff "$base" "$upstream" -- db/Migration.go db/sql/migrations > "$output/$label/upstream-migrations.patch"
    git -C "$repository" diff --no-ext-diff "$upstream" "$head" -- maintenance > "$output/$label/ownership-and-contracts.patch"
  fi
  # Isolate merge-tree's loose objects as well as its output; no repository writes.
  object_dir="$(git -C "$repository" rev-parse --path-format=absolute --git-path objects)"
  result=0
  GIT_OBJECT_DIRECTORY="$output/$label/objects" GIT_ALTERNATE_OBJECT_DIRECTORIES="$object_dir" \
    git -C "$repository" merge-tree --write-tree --name-only "$head" "$upstream" \
    > "$output/$label/merge-preview.txt" 2> "$output/$label/merge-preview-errors.txt" || result=$?
  [[ "$result" -le 1 ]] || die "$label merge preview failed; see retained errors"
  printf 'conflicts: %s\n' "$result" > "$output/$label/merge-result.yml"
  fingerprint_after="$(git -C "$repository" diff --no-ext-diff --binary HEAD -- . ':(exclude).claude/task-notes' ':(exclude).claude/rules' | shasum -a 256 | awk '{print $1}')"
  [[ "$fingerprint_before" == "$fingerprint_after" ]] || die "$label changed during capture"
  [[ "$(git -C "$repository" rev-parse HEAD)" == "$head" ]] || die "$label HEAD changed during capture"
}

main() {
  [[ "${GOFLAGS-}" != *-overlay* ]] || die 'Run retained assessment/verification without Go overlays; use the checker directly for mutation experiments'
  local fetch_refs=false output='' repository='' option label location fingerprint
  while [[ $# -gt 0 ]]; do
    option="$1"
    case "$option" in
      -h|--help) usage; return ;;
      --fetch) fetch_refs=true; shift ;;
      --output) [[ $# -ge 2 ]] || die '--output needs a directory'; output="$2"; shift 2 ;;
      -*) die "Unknown option $option" ;;
      *) [[ -z "$repository" ]] || die 'Only one repository is accepted'; repository="$1"; shift ;;
    esac
  done
  [[ -n "$repository" && -n "$output" ]] || die 'Repository and --output are required'
  repository="$(cd "$repository" && pwd -P)"
  [[ "$(git -C "$repository" rev-parse --show-toplevel)" == "$repository" ]] || die 'Use the repository root'
  [[ "$(git -C "$repository/docs" rev-parse --show-toplevel)" == "$repository/docs" ]] || die 'Initialize the docs submodule first'
  check_remote "$repository" origin git@github.com:freefair/semaphore-ex.git
  check_remote "$repository" upstream git@github.com:semaphoreui/semaphore.git
  check_remote "$repository/docs" origin git@github.com:freefair/semaphore-docs.git
  check_remote "$repository/docs" upstream git@github.com:semaphoreui/semaphore-docs.git
  [[ -z "$(git -C "$repository" ls-files --others --exclude-standard -- . ':(exclude).claude/task-notes' ':(exclude).claude/rules' ':(exclude)dist')" ]] || die 'Stage reviewed untracked source before assessment'
  [[ -z "$(git -C "$repository/docs" ls-files --others --exclude-standard)" ]] || die 'Stage reviewed untracked docs before assessment'
  [[ ! -e "$output" ]] || die 'Output already exists; preserve earlier evidence and choose a new directory'
  mkdir -p "$output"
  output="$(cd "$output" && pwd -P)"
  capture_repository "$repository" root develop "$output" "$fetch_refs"
  capture_repository "$repository/docs" docs main "$output" "$fetch_refs"
  (
    cd "$repository"
    go run ./tools/upstreamcheck -root "$repository" -mode incoming -incoming-ref "$(awk '$1 == "upstream:" {print $2}' "$output/root/refs.yml")" > "$output/root/migration-decisions.yml" || exit "$?"
    go run ./tools/upstreamcheck -root "$repository" -mode check -base-ref "$(awk '$1 == "origin:" {print $2}' "$output/root/refs.yml")"
  ) > "$output/contracts-and-migrations.log" 2>&1 || die 'Ownership or contract check failed; review retained evidence'
  for label in root docs; do
    location="$repository"; [[ "$label" == root ]] || location="$repository/docs"
    fingerprint="$(git -C "$location" diff --no-ext-diff --binary HEAD -- . ':(exclude).claude/task-notes' ':(exclude).claude/rules' | shasum -a 256 | awk '{print $1}')"
    [[ "$fingerprint" == "$(cat "$output/$label/working-tree.sha256")" ]] || die "$label source changed during assessment"
    [[ "$(git -C "$location" rev-parse HEAD)" == "$(awk '$1 == "head:" {print $2}' "$output/$label/refs.yml")" ]] || die "$label HEAD changed during assessment"
  done
  (cd "$output" && shasum -a 256 root/*.yml root/*.patch root/*.sha256 docs/*.yml docs/*.patch docs/*.sha256 contracts-and-migrations.log) > "$output/checksums.sha256"
  printf 'Assessment retained in %s\nReview conflicts and new contracts before merging; no merge or push was performed.\n' "$output"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
