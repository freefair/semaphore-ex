#!/usr/bin/env bash
# Keep failed gates visible and retain evidence from all independent checks.
set -Eeuo pipefail

usage() {
  cat <<'HELP'
Usage: verify.sh --output NEW_DIRECTORY [--quick]

Run from any directory. Quick mode checks maintenance inventories and their tests.
Full mode additionally builds the frontend, tests and vets both Go modules,
compiles Dredd, runs the frontend suite, builds the product, checks Dockerfiles,
and builds/checks all documentation locales. Requires Go, Node/npm, and Docker.
A failed frontend suite stays failed; baseline classification requires review.

Example:
  bash tools/upstream-sync/verify.sh --output /tmp/semaphore-gates
HELP
}

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
cleanup() { trap - EXIT INT TERM; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

run_gate() {
  local name="$1" directory="$2" result=0 started
  shift 2
  started="$(date +%s)"
  printf 'Running %s\n' "$name"
  (cd "$directory" && "$@") > "$OUTPUT/$name.log" 2>&1 || result=$?
  printf '%s\t%s\t%s\n' "$name" "$result" "$(($(date +%s)-started))" >> "$OUTPUT/results.tsv"
  if [[ "$result" -ne 0 ]]; then FAILED=1; printf 'Failed: %s (see %s/%s.log)\n' "$name" "$OUTPUT" "$name" >&2; fi
  return "$result"
}

main() {
  [[ "${GOFLAGS-}" != *-overlay* ]] || die 'Run retained assessment/verification without Go overlays; use the checker directly for mutation experiments'
  local quick=false repository source_before source_after docs_before docs_after baseline git_config_count product_output
  local OUTPUT='' FAILED=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help) usage; return ;;
      --quick) quick=true; shift ;;
      --output) [[ $# -ge 2 ]] || die '--output needs a directory'; OUTPUT="$2"; shift 2 ;;
      *) die "Unknown option $1" ;;
    esac
  done
  [[ -n "$OUTPUT" && ! -e "$OUTPUT" ]] || die '--output must name a new directory'
  repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
  # Untracked source is outside the HEAD/diff fingerprint; require review/staging first.
  [[ -z "$(git -C "$repository" ls-files --others --exclude-standard -- . ':(exclude).claude/task-notes' ':(exclude).claude/rules' ':(exclude)dist')" ]] || die 'Stage reviewed untracked source before verification'
  [[ -z "$(git -C "$repository/docs" ls-files --others --exclude-standard)" ]] || die 'Stage reviewed untracked documentation before verification'
  mkdir -p "$OUTPUT"; OUTPUT="$(cd "$OUTPUT" && pwd -P)"
  baseline="$(git -C "$repository" rev-parse --verify 'origin/develop^{commit}')"
  printf '%s\n' "$baseline" > "$OUTPUT/baseline.txt"
  git_config_count="${GIT_CONFIG_COUNT:-0}"
  [[ "$git_config_count" =~ ^[0-9]+$ ]] || die 'GIT_CONFIG_COUNT must be numeric'
  git -C "$repository" rev-parse HEAD > "$OUTPUT/head.txt"
  git -C "$repository/docs" rev-parse HEAD > "$OUTPUT/docs-head.txt"
  git -C "$repository" status --porcelain=v1 > "$OUTPUT/worktree-status.txt"
  docs_before="$(git -C "$repository/docs" diff --no-ext-diff --binary HEAD | shasum -a 256)"
  printf '%s\n' "$docs_before" > "$OUTPUT/docs-working-diff.sha256"
  source_before="$(git -C "$repository" diff --no-ext-diff --binary HEAD -- . ':(exclude).claude/task-notes' ':(exclude).claude/rules' | shasum -a 256)"
  printf '%s\n' "$source_before" > "$OUTPUT/working-diff.sha256"
  product_output="$(node -e 'process.stdout.write(require("node:path").relative(process.argv[1], process.argv[2]))' "$repository" "$OUTPUT/product")"
  printf 'gate\texit_code\telapsed_seconds\n' > "$OUTPUT/results.tsv"
  if [[ "$quick" == false ]]; then
    # The frontend build replaces api/public; no Go gate may overlap that write.
    run_gate frontend-build "$repository/web" npm run build || true
  fi
  run_gate maintenance "$repository" go run ./tools/upstreamcheck -mode check -base-ref "$baseline" || true
  run_gate maintenance-tests "$repository" go test ./tools/upstreamcheck -count=1 || true
  if [[ "$quick" == false ]]; then
    run_gate root-tests "$repository" env "GIT_CONFIG_COUNT=$((git_config_count+1))" "GIT_CONFIG_KEY_$git_config_count=commit.gpgsign" "GIT_CONFIG_VALUE_$git_config_count=false" go test ./... -count=1 || true
    run_gate enhanced-tests "$repository/test/edition-contract/enhanced" env "GIT_CONFIG_COUNT=$((git_config_count+1))" "GIT_CONFIG_KEY_$git_config_count=commit.gpgsign" "GIT_CONFIG_VALUE_$git_config_count=false" go test ./... -count=1 || true
    run_gate root-vet "$repository" go vet ./... || true
    run_gate enhanced-vet "$repository/test/edition-contract/enhanced" go vet ./... || true
    run_gate api-bundle "$repository" go run ./tools/openapibundle -output "$OUTPUT/api-docs.yml" || true
    run_gate dredd "$repository/.dredd/hooks" go build -o "$OUTPUT/dredd-hooks" . || true
    run_gate frontend-tests "$repository/web" env NODE_OPTIONS="--localstorage-file=$OUTPUT/localstorage" npm run test:unit || true
    run_gate product-build "$repository" go run github.com/go-task/task/v3/cmd/task@v3.53.1 build:edition "OUTPUT_DIR=$product_output" || true
    run_gate server-docker "$repository" docker build --check --file deployment/docker/server/Dockerfile . || true
    run_gate runner-docker "$repository" docker build --check --file deployment/docker/runner/Dockerfile . || true
    run_gate docs-build "$repository/docs" npm run build || true
    run_gate docs-fallbacks "$repository/docs" env DOCS_BUILD_DIR="$repository/docs/build" node --test tests/full-product-navigation.test.cjs tests/security-fallbacks.test.cjs || true
  fi
  [[ "$(git -C "$repository" rev-parse HEAD)" == "$(cat "$OUTPUT/head.txt")" ]] || die 'Root HEAD changed during verification; evidence is stale'
  [[ "$(git -C "$repository/docs" rev-parse HEAD)" == "$(cat "$OUTPUT/docs-head.txt")" ]] || die 'Docs HEAD changed during verification; evidence is stale'
  docs_after="$(git -C "$repository/docs" diff --no-ext-diff --binary HEAD | shasum -a 256)"
  [[ "$docs_before" == "$docs_after" ]] || die 'Documentation source changed during verification; evidence is stale'
  source_after="$(git -C "$repository" diff --no-ext-diff --binary HEAD -- . ':(exclude).claude/task-notes' ':(exclude).claude/rules' | shasum -a 256)"
  [[ "$source_before" == "$source_after" ]] || die 'Tracked source changed during verification; evidence is stale'
  (cd "$OUTPUT" && shasum -a 256 ./*.log ./head.txt ./docs-head.txt ./baseline.txt ./working-diff.sha256 ./docs-working-diff.sha256 ./results.tsv) > "$OUTPUT/checksums.sha256"
  printf 'Verification evidence: %s\n' "$OUTPUT"
  return "$FAILED"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
