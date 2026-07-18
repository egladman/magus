#!/bin/sh
# PreToolUse (Bash) nudge: when an agent is about to run a raw build/test/lint
# tool in this magus workspace, remind it that a target covers the work. The
# skills teach this, but a model deep in a task pattern-matches to its default
# harness; a nudge at the moment of the shell call is what actually redirects
# it. Non-blocking by design: additionalContext only, never a deny, so a
# legitimately uncovered command just proceeds.

cmd=$(jq -r '.tool_input.command // empty' 2>/dev/null)
[ -n "$cmd" ] || { printf '{}\n'; exit 0; }

# Already going through magus (or its go-run form): nothing to say.
case "$cmd" in
*magus*) printf '{}\n' && exit 0 ;;
esac

# Raw tools that top-level targets cover here (build/test/lint/format/codegen).
# Deliberately narrow: content greps, git, and one-off scripts are not nudged.
if printf '%s' "$cmd" | grep -qE '(^|[;&|] *)(go (test|build|vet)|gofmt|tsc|eslint|prettier|vitest|pytest|buf (lint|build|generate)|(pnpm|npm) (run |test)|npx )'; then
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"magus workspace: a magus target likely covers this command (see the magus-run skill). Prefer the magus_run_target MCP tool, or magus run <target> -s on the CLI; raw tool runs bypass the cache, sandbox, and affected tracking. If no target covers it, proceed as planned."}}\n'
else
  printf '{}\n'
fi
exit 0
