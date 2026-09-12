#!/usr/bin/env bash
# Extract the CHANGELOG.md section for a release tag into a release-notes file.
#
# Release candidates (vX.Y.Z-ex.N-rcM) share the section of their final tag, so the
# -rcM / -betaM suffix is stripped before the lookup. The script fails when the
# section is missing: a release without written notes is not a release.
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: release-notes.sh [--changelog FILE] [--output FILE] [TAG]

TAG defaults to the tag that points at HEAD (git describe --tags --exact-match).
--changelog defaults to CHANGELOG.md, --output to dist/release-notes.md.
USAGE
}

changelog="CHANGELOG.md"
output="dist/release-notes.md"
tag=""

while [ $# -gt 0 ]; do
  case "$1" in
    --changelog) changelog="$2"; shift 2 ;;
    --output) output="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    *) tag="$1"; shift ;;
  esac
done

if [ -z "$tag" ]; then
  tag="$(git describe --tags --exact-match HEAD)"
fi

section="${tag%-rc[0-9]*}"
section="${section%-beta[0-9]*}"

mkdir -p "$(dirname "$output")"
awk -v heading="## [${section}]" '
  index($0, heading) == 1 { found = 1; next }
  found && /^## \[/ { exit }
  found { print }
' "$changelog" > "$output"

if [ ! -s "$output" ]; then
  echo "${changelog} has no section '## [${section}]' for tag ${tag}" >&2
  exit 1
fi

echo "release notes for ${tag} written to ${output}"
