#!/usr/bin/env bash
# Print the CHANGELOG.md entry for one released version, without its heading.
#
# The changelog is the most carefully written thing in this repository and it
# was invisible on the releases page, where every reader of a release actually
# looks. This is what puts it there.
#
# Usage: scripts/changelog-section.sh 0.1.0-alpha.36 [path/to/CHANGELOG.md]
#
# Exits 2 when the version has no entry, so a caller can decide whether that is
# fatal. Publishing a release is not the moment to discover it, but neither is
# it a reason to withhold the binaries.
set -euo pipefail

version="${1:-}"
changelog="${2:-CHANGELOG.md}"

if [ -z "${version}" ]; then
  echo "usage: ${0##*/} <version> [changelog]" >&2
  exit 64
fi

if [ ! -r "${changelog}" ]; then
  echo "${0##*/}: cannot read ${changelog}" >&2
  exit 66
fi

# Matched literally rather than as a pattern: a version is full of dots, and
# `0.1.0-alpha.3` as a regular expression also matches `0110-alphaX3`.
section=$(
  awk -v want="## [${version}]" '
    index($0, want) == 1 { found = 1; next }
    # Any later top-level heading ends the section, including the link
    # definitions some changelogs keep at the bottom.
    found && /^## / { exit }
    found { print }
  ' "${changelog}"
)

# Trim leading and trailing blank lines; the heading is always followed by one
# and the next heading is always preceded by one.
section=$(printf '%s\n' "${section}" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}')

if [ -z "${section}" ]; then
  echo "${0##*/}: no entry for ${version} in ${changelog}" >&2
  exit 2
fi

printf '%s\n' "${section}"
