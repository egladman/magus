#!/usr/bin/env bash
# screenshots.sh - regenerate the committed console screenshots.
#
# Why a hack and not a magus target: two reasons, both load-bearing.
#
# It needs a real browser, and a browser is not a dependency magus should acquire to
# build itself. Chrome is whatever you already have installed; nothing here is fetched
# and nothing here runs in CI.
#
# And a target wrapping this would deadlock. The assembly below runs three magus
# commands against this same workspace; invoked from inside a target, the outer run is
# already holding the root project lock those inner runs need, so it hangs until the
# timeout rather than failing usefully. Run this script directly.
#
# The one thing worth knowing before editing: the console MUST be served from the
# assembled deploy tree (gen/site), where it lives under /console/. Served from
# console/gen at a server root instead, the shell never matches its own base path, so a
# surface URL like /dashboard/ does not resolve to a surface and every capture silently
# comes out as the launcher. That mistake costs an hour; the assembly step below is what
# prevents it.
#
#   ./hacks/screenshots.sh            # assemble, serve, capture everything
#   ./hacks/screenshots.sh dashboard  # just one, by name
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

out_dir=assets/screenshots
port=${MAGUS_SHOT_PORT:-8231}

find_chrome() {
  local c
  for c in \
    "${CHROME:-}" \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
    "/Applications/Chromium.app/Contents/MacOS/Chromium" \
    "$(command -v google-chrome || true)" \
    "$(command -v chromium || true)"; do
    [ -n "$c" ] && [ -x "$c" ] && { printf '%s' "$c"; return 0; }
  done
  echo "screenshots: no Chrome or Chromium found. Set CHROME=/path/to/binary." >&2
  return 1
}

# name|path under the served tree|width|height|scale|mobile(0|1)
# The #demo fragment is what each surface reads to enter the daemon-free showcase, so
# these need no daemon and no workspace.
# Only what the site actually shows. Adding a surface here is the whole cost of adding a
# screenshot; committing one nothing references is just weight in the repo.
#
# The -mobile shots are a real phone: 375x812 at 3x with mobile emulation ON, so they carry BOTH
# things that make the console different there - the layout below the shell's 48rem inversion (the
# navigation rail gives way to the launcher grid, the diff's file index collapses to a reopen rail,
# the log waterfall holds its drawn width and scrolls) AND the touch-target sizing, which is keyed
# on (pointer: coarse) rather than on width and so does not follow from a narrow window alone.
#
# Both of those need CDP device emulation; the plain --screenshot flag form cannot express either.
# hacks/screenshot.mjs is that driver and explains the two limits it exists to get past.
shots=(
  "console-dashboard|console/dashboard/#demo|1280|820|2|0"
  "console-graph|console/graph/#demo|1280|820|2|0"
  "console-logs|console/logs/#demo|1280|820|2|0"
  "console-activity|console/activity/#demo|1280|820|2|0"
  "console-diff|console/diff/#demo|1280|820|2|0"
  "console-diff-mobile|console/diff/#demo|375|812|3|1"
  "console-logs-mobile|console/logs/#demo|375|812|3|1"
  "console-dashboard-mobile|console/dashboard/#demo|375|812|3|1"
)

want=${1:-}

chrome=$(find_chrome)

# ./magus over whatever is on PATH. A released binary can be older than this tree - it
# rejects a magusfile key or module it does not carry yet and fails at workspace load,
# which surfaces here as a confusing "module not found" from inside the assembly step.
magus_bin=magus
[ -x ./magus ] && magus_bin=./magus

echo "==> assembling the deploy tree (docs at the root, console under /console/)"
"$magus_bin" run generate docs --silent
"$magus_bin" run build console --silent
"$magus_bin" run deploy-generate . --silent

mkdir -p "$out_dir"

echo "==> serving gen/site on 127.0.0.1:$port"
python3 -m http.server "$port" --bind 127.0.0.1 --directory gen/site >/dev/null 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true' EXIT
# Wait for it rather than sleeping a guessed interval.
for _ in $(seq 1 40); do
  curl -fsS -o /dev/null "http://127.0.0.1:$port/" 2>/dev/null && break
  sleep 0.25
done

for shot in "${shots[@]}"; do
  IFS='|' read -r name path w h scale mobile <<<"$shot"
  [ -n "$want" ] && [ "$want" != "$name" ] && continue
  label=$([ "$mobile" = 1 ] && echo "mobile" || echo "desktop")
  echo "==> $name  (${w}x${h} @${scale}x, $label)"
  node "$repo_root/hacks/screenshot.mjs" \
    "$chrome" "http://127.0.0.1:$port/$path" "$out_dir/$name.png" "$w" "$h" "$scale" "$mobile"
  [ -s "$out_dir/$name.png" ] || { echo "screenshots: $name produced nothing" >&2; exit 1; }
done

echo "==> wrote:"
ls -la "$out_dir"
