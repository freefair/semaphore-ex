#!/usr/bin/env bash

set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repository_dir="$(cd -- "${script_dir}/../.." && pwd)"
candidate_image="${SEMAPHORE_HA_CANDIDATE_IMAGE:-semaphore-enhanced-server:ha-candidate}"
skew_image="${SEMAPHORE_HA_SKEW_IMAGE:-semaphore-enhanced-server:ha-skew-old}"
work_dir=''

cleanup() {
  trap - SIGINT SIGTERM ERR EXIT
  if [[ -n "${work_dir}" ]]; then
    case "${work_dir}" in
      "${TMPDIR:-/tmp}"/semaphore-ha-build.*) rm -rf -- "${work_dir}" ;;
    esac
  fi
}
trap cleanup SIGINT SIGTERM ERR EXIT

usage() {
  cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [-h|--help]

Build disposable full-product server images for the local HA gate.

Environment:
  SEMAPHORE_HA_CANDIDATE_IMAGE  Candidate tag (default: semaphore-enhanced-server:ha-candidate)
  SEMAPHORE_HA_SKEW_IMAGE       Compatible skew-fixture tag (default: semaphore-enhanced-server:ha-skew-old)

Example:
  $(basename "${BASH_SOURCE[0]}")
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
  '') ;;
  *) die "unknown option: ${1}" ;;
esac

for command in docker git go rsync; do
  command -v "${command}" >/dev/null 2>&1 || die "required command is unavailable: ${command}"
done

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/semaphore-ha-build.XXXXXX")"
context_dir="${work_dir}/context"
mkdir -p "${context_dir}"
rsync -a \
  --exclude '.git/' \
  --exclude '.claude/' \
  --exclude 'dist/' \
  --exclude 'web/node_modules/' \
  --exclude 'test/e2e/node_modules/' \
  --exclude '0.0.0.0*' \
  "${repository_dir}/" "${context_dir}/"
core_revision="$(git -C "${repository_dir}" rev-parse HEAD)"
source_date_epoch="$(git -C "${repository_dir}" show -s --format=%ct HEAD)"
source_sha="$(git -C "${repository_dir}" log --pretty=format:%h -n 1)"
docker build \
  --build-arg CORE_REVISION="${core_revision}" \
  --build-arg ENHANCED_REVISION="${core_revision}" \
  --build-arg SOURCE_DATE_EPOCH="${source_date_epoch}" \
  --build-arg SOURCE_TAG=ha-candidate \
  --build-arg SOURCE_SHA="${source_sha}" \
  --tag "${candidate_image}" \
  --file "${context_dir}/deployment/docker/server/Dockerfile" \
  "${context_dir}"

docker build \
  --build-arg BASE_IMAGE="${candidate_image}" \
  --tag "${skew_image}" \
  --file "${context_dir}/test/ha-resilience/Dockerfile.skew" \
  "${context_dir}"

printf 'Built %s and %s\n' "${candidate_image}" "${skew_image}"
