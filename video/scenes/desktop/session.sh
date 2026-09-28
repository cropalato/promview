#!/bin/sh
# A small Linux desktop inside a virtual X screen, for filming the Tauri client:
# its own D-Bus session (so the tray and notifications go to the panel and
# daemon started here, never to the real desktop), openbox for windows, tint2
# for a panel with a system tray, snixembed to bridge StatusNotifierItem to
# that tray, dunst for notifications. Runs the given command inside, then
# tears everything down.
#
#   video/scenes/desktop/session.sh node scenes/desktop/tray.mjs
set -eu
cd "$(dirname "$0")/../.."
ETC=$PWD/scenes/desktop/etc
export PROMVIEW_DESKTOP_CONFIG=$ETC/promview-desktop.toml
# The shell running this may point its own desktop client elsewhere; an
# exported variable wins over the file, so say it explicitly.
export PROMVIEW_SERVER_URL=${DEMO_SERVER_URL:-http://localhost:8080}
export PROMVIEW_DESKTOP_BIN=${PROMVIEW_DESKTOP_BIN:-$PWD/../desktop/src-tauri/target/release/promview-desktop}
export ALACRITTY_CONFIG=$ETC/alacritty.toml

exec xvfb-run --auto-servernum --server-args="-screen 0 1920x1080x24" \
  dbus-run-session -- sh -eu -c '
    export DISPLAY GDK_BACKEND=x11 XCURSOR_THEME=Adwaita XCURSOR_SIZE=24 GTK_THEME=Adwaita:dark
    export WEBKIT_DISABLE_DMABUF_RENDERER=1 WEBKIT_DISABLE_COMPOSITING_MODE=1
    xsetroot -solid "#242933" || true
    xsetroot -cursor_name left_ptr || true
    openbox --config-file "$1/openbox.xml" & pids=$!
    tint2 -c "$1/tint2rc" & pids="$pids $!"
    dunst -config "$1/dunstrc" & pids="$pids $!"
    # --fork returns only once the StatusNotifierWatcher is on the bus, so the
    # client started next finds it. X clients die with the X server, so only
    # what was started here needs killing.
    snixembed --fork
    sleep 0.5
    trap "kill $pids 2>/dev/null" EXIT INT TERM
    shift
    "$@"
  ' sh "$ETC" "$@"
