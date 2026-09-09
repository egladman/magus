#!/usr/bin/env sh
# Provision ARM-0 "rampant": the magus binary on PATH and a magusfile in the
# tree, and no agent surface at all. See ../README.md for the switch table.
set -eu

# shellcheck disable=SC2034  # read by lib.sh, which shellcheck does not follow without -x
ARM_NAME=rampant
# shellcheck source=../lib.sh disable=SC1091
. "$(dirname "$0")/../lib.sh"

WT=$(arm_worktree "${1:-}")
BIN=$(arm_binary "${2:-}")
ARMS=$(cd "$(dirname "$0")/.." && pwd)

arm_reset "$WT"

arm_write_env "$WT" "$BIN" <<'ENV'
MAGUS_HINTS_ENABLED=false; export MAGUS_HINTS_ENABLED
ENV

# The hook entries stay, so the settings file has the shape the full arm's has
# and the two differ in what the hooks DO. `true` is silent; pointing them at a
# missing binary would inject a "guard is NOT running" notice, which is itself
# context this arm is meant not to have.
cat > "$WT/.claude/settings.json" <<'JSON'
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "true", "timeout": 10 }]
      },
      {
        "matcher": "Edit|Write|NotebookEdit",
        "hooks": [{ "type": "command", "command": "true", "timeout": 10 }]
      },
      {
        "matcher": "Read",
        "hooks": [{ "type": "command", "command": "true", "timeout": 10 }]
      }
    ]
  }
}
JSON

cp "$ARMS/repo-description.md" "$WT/CLAUDE.md"

printf 'arm rampant: provisioned %s (magus %s)\n' "$WT" "$BIN"
