#!/usr/bin/env bash
# Verify every released version in CHANGELOG.md can be extracted for release
# notes. scripts/changelog-section.sh runs once per release and nowhere else,
# so without this the first time a change to it is exercised is the release
# that depends on it - the same trap the release workflow's own comments
# describe.
#
# It also catches the changelog end of that: a heading that does not match the
# `## [version] - date` shape, or a version whose section is empty, is a
# release that would publish notes saying nothing.
set -euo pipefail

cd "$(dirname "$0")/.."

changelog="CHANGELOG.md"
extract="scripts/changelog-section.sh"

# Released versions only. Unreleased has no tag and no release to annotate.
mapfile -t versions < <(
  grep -oE '^## \[[^]]+\]' "${changelog}" \
    | sed -e 's/^## \[//' -e 's/\]$//' \
    | grep -v '^Unreleased$'
)

if [ "${#versions[@]}" -eq 0 ]; then
  echo "check-changelog: no released versions found in ${changelog}" >&2
  exit 1
fi

failed=0
for version in "${versions[@]}"; do
  if ! "${extract}" "${version}" "${changelog}" >/dev/null; then
    echo "check-changelog: cannot extract notes for ${version}" >&2
    failed=1
  fi
done

if [ "${failed}" -ne 0 ]; then
  exit 1
fi

echo "check-changelog: ${#versions[@]} released versions extract cleanly"
