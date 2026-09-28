#!/bin/sh
# Captures web scenes inside a virtual X screen.
#   video/scenes/capture.sh filter          one scene
#   video/scenes/capture.sh all             every scene, in story order
# A split scene (two consoles side by side) gets two half-width screens on
# the same server; the assembly joins them.
set -eu
cd "$(dirname "$0")/.."

ORDER="ingest sources filter group detail operate live bulk silence themes"
scenes=${1:?scene name or all}
[ "$scenes" = all ] && scenes=$ORDER

for scene in $scenes; do
  # A desktop scene runs inside the sandbox desktop, which brings its own X server.
  if [ -f "scenes/desktop/$scene.mjs" ]; then
    scenes/desktop/session.sh node "scenes/desktop/$scene.mjs"
    continue
  fi
  if grep -q '^export const split = true' "scenes/web/$scene.mjs"; then
    screen="-screen 0 960x1080x24 -screen 1 960x1080x24"
  else
    screen="-screen 0 1920x1080x24"
  fi
  xvfb-run --auto-servernum --server-args="$screen -nocursor" \
    sh -c 'export DISPLAY; node scenes/web/'"$scene"'.mjs'
done
