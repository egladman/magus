#!/bin/sh
# install-login-item.sh — make bubblegum start at login via a LaunchAgent.
#
#   ./install-login-item.sh            install + start it now
#   ./install-login-item.sh uninstall  stop + remove it
#
# Run it from anywhere; it finds bubblegum.buzz next to itself. It needs the
# `buzz` interpreter on PATH — or point at one explicitly:
#
#   BUZZ=/path/to/buzz ./install-login-item.sh
#
# The buzz binary must already have Accessibility granted (you did that the
# first time you ran bubblegum) — launchd starts the same binary, same grant.
set -eu

LABEL="com.bubblegum.wm"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG="$HOME/Library/Logs/bubblegum.log"
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
DOMAIN="gui/$(id -u)"

unload() {
    # bootout is the modern form; fall back to unload on older macOS.
    launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null \
        || launchctl unload "$PLIST" 2>/dev/null \
        || true
}

if [ "${1:-}" = "uninstall" ]; then
    unload
    rm -f "$PLIST"
    echo "uninstalled — removed $PLIST"
    exit 0
fi

BUZZ="${BUZZ:-$(command -v buzz 2>/dev/null || true)}"
if [ -z "$BUZZ" ]; then
    echo "error: can't find the 'buzz' interpreter on PATH." >&2
    echo "       re-run as:  BUZZ=/path/to/buzz $0" >&2
    exit 1
fi

mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"

# RunAtLoad starts it at login (and right now). No KeepAlive: a clean `quit`
# stays quit, and a crash stays down rather than respawning in a loop (e.g. if
# Accessibility gets revoked by an OS update). To auto-restart on *crash only*
# — quit still exits 0, so it won't fight your quit — add this block to the dict:
#     <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>$LABEL</string>
    <key>ProgramArguments</key>
    <array>
        <string>$BUZZ</string>
        <string>-L</string>
        <string>$SCRIPT_DIR</string>
        <string>$SCRIPT_DIR/bubblegum.buzz</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>ProcessType</key>
    <string>Interactive</string>
    <key>StandardOutPath</key>
    <string>$LOG</string>
    <key>StandardErrorPath</key>
    <string>$LOG</string>
</dict>
</plist>
EOF
echo "wrote $PLIST"

unload # in case a previous version is loaded
launchctl bootstrap "$DOMAIN" "$PLIST" 2>/dev/null || launchctl load "$PLIST"
echo "loaded — bubblegum will start at login (and just started now)."
echo "  interpreter: $BUZZ"
echo "  logs:        $LOG"
echo "  uninstall:   $0 uninstall"
