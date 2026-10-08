#!/usr/bin/env bash
# Install or upgrade the Promview desktop client from the newest GitHub release.
#
# Every release so far is a pre-release, and GitHub's "latest" endpoint skips
# those, so the newest one is taken from the release list instead. The package
# format follows the system's package manager, which makes the same command an
# install on a fresh machine and an upgrade on one that already has the client.
#
#   scripts/install-desktop.sh                 install or upgrade to the newest
#   scripts/install-desktop.sh v0.1.0-beta.3   a specific release
#   scripts/install-desktop.sh --print         show what would be installed
#
# The packages are unsigned, so there is nothing to verify beyond the HTTPS
# connection to GitHub. Only the final install step asks for root.
set -euo pipefail

repo="cropalato/promview"
print_only=0
tag=""

for arg in "$@"; do
  case "${arg}" in
    --print) print_only=1 ;;
    -h | --help)
      sed -n '2,/^set -/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'
      exit 0
      ;;
    v*) tag="${arg}" ;;
    *)
      echo "install-desktop: unknown argument '${arg}'" >&2
      exit 2
      ;;
  esac
done

if [ "$(uname -s)" != "Linux" ] || [ "$(uname -m)" != "x86_64" ]; then
  echo "install-desktop: only Linux x86_64 packages are built; Windows installers are on the releases page" >&2
  exit 1
fi

# One package manager decides the format and the install command.
if command -v pacman >/dev/null 2>&1; then
  suffix=".pkg.tar.zst"
  install_cmd=(pacman -U --noconfirm)
elif command -v apt-get >/dev/null 2>&1; then
  suffix=".deb"
  install_cmd=(apt-get install -y)
elif command -v dnf >/dev/null 2>&1; then
  suffix=".rpm"
  install_cmd=(dnf install -y)
elif command -v zypper >/dev/null 2>&1; then
  suffix=".rpm"
  install_cmd=(zypper --non-interactive install --allow-unsigned-rpm)
else
  echo "install-desktop: no supported package manager (pacman, apt, dnf, zypper)" >&2
  exit 1
fi

api="https://api.github.com/repos/${repo}/releases"
if [ -n "${tag}" ]; then
  release=$(curl -fsSL "${api}/tags/${tag}")
else
  # The list is newest first and includes pre-releases.
  release=$(curl -fsSL "${api}?per_page=1")
fi

tag=$(printf '%s\n' "${release}" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
url=$(printf '%s\n' "${release}" | grep -oE '"browser_download_url": *"[^"]+"' |
  sed -E 's/.*: *"([^"]+)"/\1/' | grep -F -- "${suffix}" | head -1 || true)

if [ -z "${tag}" ] || [ -z "${url}" ]; then
  echo "install-desktop: no ${suffix} asset found on ${tag:-the newest release}" >&2
  exit 1
fi

echo "release: ${tag}"
echo "package: ${url}"
if [ "${print_only}" -eq 1 ]; then
  exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "${work}"' EXIT
# apt reads the file as an unprivileged user and warns when it cannot.
chmod 755 "${work}"
file="${work}/$(basename "${url}")"
curl -fL --progress-bar -o "${file}" "${url}"

if [ "$(id -u)" -ne 0 ]; then
  sudo "${install_cmd[@]}" "${file}"
else
  "${install_cmd[@]}" "${file}"
fi
