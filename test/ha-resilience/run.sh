#!/usr/bin/env bash

set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repository_dir="$(cd -- "${script_dir}/../.." && pwd)"
keep_environment=false
report_path="${SEMAPHORE_HA_REPORT_PATH:-dist/ha-resilience/report.json}"

cleanup() {
  trap - SIGINT SIGTERM ERR EXIT
}
trap cleanup SIGINT SIGTERM ERR EXIT

usage() {
  cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [--report PATH] [--keep] [-h|--help]

Run the disposable multi-node Enhanced HA fault and rolling-replacement gate.

Options:
  --report PATH  Machine-readable JSON report (default: dist/ha-resilience/report.json)
  --keep         Keep the isolated Compose project after the run
  -h, --help     Show this help and exit

Environment:
  SEMAPHORE_HA_SERVER_A_IMAGE     Initial skew-fixture server image (required)
  SEMAPHORE_HA_SERVER_B_IMAGE     Candidate server image (required)
  SEMAPHORE_HA_REPLACEMENT_IMAGE Candidate replacement server image (required)
  SEMAPHORE_HA_RUNNER_IMAGE       Candidate runner image (required)
  SEMAPHORE_HA_REPORT_PATH        Alternative default report path

Example:
  SEMAPHORE_HA_SERVER_A_IMAGE=semaphore-enhanced-server:ha-skew-old \\
  SEMAPHORE_HA_SERVER_B_IMAGE=semaphore-enhanced-server:ci \\
  SEMAPHORE_HA_REPLACEMENT_IMAGE=semaphore-enhanced-server:ci \\
  SEMAPHORE_HA_RUNNER_IMAGE=semaphore-enhanced-runner:ci \\
  $(basename "${BASH_SOURCE[0]}")
EOF
}

die() {
  printf 'error: %s\n' "$1" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --report)
      [[ $# -ge 2 ]] || die "--report requires a path"
      report_path="$2"
      shift 2
      ;;
    --keep)
      keep_environment=true
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

for command in docker go; do
  command -v "${command}" >/dev/null 2>&1 || die "required command is unavailable: ${command}"
done
for variable in SEMAPHORE_HA_SERVER_A_IMAGE SEMAPHORE_HA_SERVER_B_IMAGE SEMAPHORE_HA_REPLACEMENT_IMAGE SEMAPHORE_HA_RUNNER_IMAGE; do
  [[ -n "${!variable:-}" ]] || die "required environment variable is empty: ${variable}"
done

arguments=(
  --repository "${repository_dir}"
  --report "${report_path}"
)
if [[ "${keep_environment}" == true ]]; then
  arguments+=(--keep)
fi

cd -- "${repository_dir}"
exec go run ./test/ha-resilience/cmd/ha-resilience "${arguments[@]}"
