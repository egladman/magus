#!/bin/sh
# SessionStart bootstrap for magus checkouts (main repo and worktrees alike).
# A fresh worktree starts with an untrusted mise.toml and uninstalled tools, so
# magus, the SCIP indexers, and the rest of the toolchain silently vanish from
# PATH until someone notices. This hook makes that state self-healing and keeps
# the installed agent skills in lockstep with the magus binary (the same check
# `magus graph verify` gates in CI).
#
# Output contract: emits Claude Code hook JSON on stdout; anything worth the
# model knowing lands in additionalContext, and stdout noise is suppressed.

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0

notes=""

if command -v mise >/dev/null 2>&1; then
  mise trust --quiet >/dev/null 2>&1 || true
  # No-op when everything is installed; links from the shared mise cache when a
  # sibling checkout already has the tools, so this stays fast.
  if ! mise install --quiet >/dev/null 2>&1; then
    notes="mise install failed; run mise install manually to see why."
  fi
fi

# Local dev in this repo runs HEAD, not a release binary (Eli's standing
# preference), so when no magus is on PATH fall back to go run against this
# checkout. go run reuses the build cache, so after the first compile in a
# fresh worktree this stays cheap.
if command -v magus >/dev/null 2>&1; then
  MAGUS="magus"
elif command -v go >/dev/null 2>&1; then
  MAGUS="go run ./cmd/magus"
else
  MAGUS=""
  notes="$notes Neither magus nor go is on PATH, so agent-skill freshness was not checked."
fi

if [ -n "$MAGUS" ]; then
  verify=$($MAGUS graph verify 2>&1)
  case "$verify" in
  *STALE*)
    if $MAGUS agent install claude --force >/dev/null 2>&1; then
      notes="$notes Agent skills were stale and have been reinstalled; review and commit the .claude/skills diff."
    else
      notes="$notes Agent skills are stale but reinstall failed; run: $MAGUS agent install claude --force."
    fi
    ;;
  esac

  # The daemon serves MCP (the magus_memory/magus_query tools Claude uses), so
  # sessions need it up. Probe cheaply instead of invoking magus: readyz answers
  # whenever a daemon holds the port, whatever its health. `server start`
  # auto-backgrounds itself now, so call it directly (no detached subshell) - it
  # returns once the daemon is accepting, or immediately if one is already up.
  if ! curl -s -m 1 -o /dev/null http://127.0.0.1:7391/readyz 2>/dev/null; then
    $MAGUS server start >/dev/null 2>&1
    notes="$notes Daemon was not running; started it in the background from HEAD (MCP at 127.0.0.1:7391/mcp; may take a moment on a cold build cache)."
  fi
fi

if command -v jq >/dev/null 2>&1; then
  jq -cn --arg ctx "$notes" \
    '{suppressOutput: true} + (if $ctx == "" then {} else {hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: ("magus bootstrap:" + $ctx)}} end)'
else
  # Notes never contain quotes or backslashes, so plain interpolation is safe.
  if [ -n "$notes" ]; then
    printf '{"suppressOutput": true, "hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": "magus bootstrap:%s"}}\n' "$notes"
  else
    printf '{"suppressOutput": true}\n'
  fi
fi
exit 0
