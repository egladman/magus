#!/usr/bin/env bash
# agent.sh <worktree> <prompt-file> <transcript-out> <max-turns> <model> <effort>
#
# The only place a paid agent session is launched. Everything the runner knows
# about "an agent ran" is this contract, so a fake or a different harness can be
# swapped in wholesale through RUNNER_AGENT.
#
# stdout is the raw stream-json transcript. Diagnostics go to stderr, which the
# runner captures separately as agent.log.
set -euo pipefail

wt=$1
prompt_file=$2
transcript=$3
max_turns=$4
model=$5
effort=$6

# The environment is scrubbed to this whitelist plus every name the worktree's
# .benchmark/env.sh exports, which is how an arm sets its levers (provision.sh
# writes it per run: the pinned binary on PATH, the empty MCP config, rotation
# off). Settings are limited to --setting-sources project, but HOME is kept, so
# whatever the host reads from ~/.claude (memory, user-scoped skills) reaches
# both arms equally; the arms differ only in what the worktree carries.
#
# Credentials pass through by NAME only: an API key, or the long-lived token `claude
# setup-token` mints for headless use. The operator exports one of them before launching
# the runner; nothing here reads, stores or prints a value.
env_keep=(HOME PATH USER LOGNAME SHELL TERM TMPDIR LANG LC_ALL ANTHROPIC_API_KEY ANTHROPIC_BASE_URL CLAUDE_CODE_OAUTH_TOKEN)
if [[ -f $wt/.benchmark/env.sh ]]; then
    # shellcheck disable=SC1091 # written per run by the arm's provision.sh
    . "$wt/.benchmark/env.sh"
    while IFS= read -r name; do
        env_keep+=("$name")
    done < <(grep -o '^[A-Z_][A-Z0-9_]*=' "$wt/.benchmark/env.sh" | tr -d '=')
fi

env_args=()
for name in "${env_keep[@]}"; do
    if [[ -n ${!name:-} ]]; then
        env_args+=("$name=${!name}")
    fi
done

cmd=(claude -p
    --output-format stream-json --verbose
    --permission-mode acceptEdits
    --model "$model"
    --effort "$effort"
    --setting-sources project)

# Turn caps are a task-level budget the CLI has not exposed since 2.x; the wall
# clock and the dollar cap are what actually bound a run. Recorded either way so
# a transcript says which caps were live.
if claude --help 2>/dev/null | grep -q -- '--max-turns'; then
    cmd+=(--max-turns "$max_turns")
else
    printf 'agent.sh: claude has no --max-turns; turn cap %s is recorded, not enforced\n' "$max_turns" >&2
fi

if [[ -n ${RUNNER_BUDGET_USD:-} && ${RUNNER_BUDGET_USD} != 0 ]]; then
    cmd+=(--max-budget-usd "$RUNNER_BUDGET_USD")
fi

# MCP registration is user-scoped, so an unconstrained session would carry the
# operator's own servers into both arms. Strict config makes the arm the only
# thing that decides which tool schemas reach the context window.
mcp_config='{"mcpServers":{}}'
if [[ -n ${RUNNER_MCP_CONFIG:-} ]]; then
    mcp_config=$RUNNER_MCP_CONFIG
fi
cmd+=(--mcp-config "$mcp_config" --strict-mcp-config)

cd "$wt"
exec env -i "${env_args[@]}" "${cmd[@]}" <"$prompt_file" >"$transcript"
