#!/usr/bin/env bash
# Fail when documentation pins a release older than the newest one.
#
# This has now drifted four separate times: the Docker Hub listing shipped a
# version 33 releases old, the README's Helm example sat six behind, and the
# Kubernetes guide nine. Every one was a reader following an instruction that
# installed something other than what the page described.
#
# Only installation commands are checked - `--version <x>` and image tags. Prose
# that names a version historically ("carried in values since 0.1.0-alpha.35")
# is a fact about the past and must not be rewritten.
set -euo pipefail

cd "$(dirname "$0")/.."

# The newest released version is whatever the changelog most recently cut.
current=$(grep -oE '^## \[[0-9][^]]*\]' CHANGELOG.md | head -1 | sed -e 's/^## \[//' -e 's/\]$//')
if [ -z "${current}" ]; then
  echo "check-doc-versions: no released version found in CHANGELOG.md" >&2
  exit 1
fi

failed=0
while IFS= read -r hit; do
  echo "check-doc-versions: ${hit}" >&2
  echo "    pins a release that is not ${current}" >&2
  failed=1
done < <(
  grep -rnE -- '--version[[:space:]]+[0-9]+\.[0-9]+\.[0-9]+[^[:space:]]*' README.md docs/*.md \
    | grep -vE -- "--version[[:space:]]+${current}([^0-9]|$)" || true
)

# Image tags may be the moving `alpha` pointer or the exact current release;
# anything else is a reader pulling an image older than the page they are on.
while IFS= read -r hit; do
  echo "check-doc-versions: ${hit}" >&2
  echo "    pins an image tag that is neither 'alpha' nor ${current}" >&2
  failed=1
done < <(
  grep -rnE 'promview:[0-9]+\.[0-9]+\.[0-9]+[^[:space:]`]*' README.md docs/*.md \
    | grep -vE "promview:${current}([^0-9]|$)" || true
)

if [ "${failed}" -ne 0 ]; then
  exit 1
fi

echo "check-doc-versions: installation examples all name ${current}"
