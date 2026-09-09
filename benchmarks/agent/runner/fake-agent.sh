#!/usr/bin/env bash
# fake-agent.sh: a RUNNER_AGENT stand-in that spends no money.
#
# Same six-argument contract as agent.sh. It emits a schema-valid stream-json
# transcript (init, one assistant turn with all four token counters, one result)
# so the extractor and the analysis side can be exercised without a model call.
# FAKE_AGENT_SOLVE=1 makes it apply the task's oracle solution, which is how the
# runner proves a passing run produces every artifact in the schema.
set -euo pipefail

wt=$1
prompt_file=$2
transcript=$3
max_turns=$4
model=$5
effort=$6

session="fake-$$"
first_line=$(head -1 "$prompt_file" 2>/dev/null || true)
start_ms=$(perl -MTime::HiRes -e 'printf "%.0f\n", Time::HiRes::time()*1000')

if [[ ${FAKE_AGENT_SOLVE:-0} == 1 ]]; then
    # Late enough that the runner's dirty-poll measures a first edit rather than
    # racing the worktree into its final state.
    sleep 1
    bash "${RUNNER_TASK_DIR:?RUNNER_TASK_DIR is required with FAKE_AGENT_SOLVE=1}/solution.sh" "$wt" >&2
    summary="applied the oracle solution"
else
    summary="did nothing"
fi

end_ms=$(perl -MTime::HiRes -e 'printf "%.0f\n", Time::HiRes::time()*1000')
usage='"usage":{"input_tokens":1204,"output_tokens":63,"cache_read_input_tokens":18320,"cache_creation_input_tokens":256}'

{
    printf '{"type":"system","subtype":"init","session_id":"%s","cwd":"%s","model":"%s","permissionMode":"acceptEdits","tools":["Read","Edit","Bash"],"mcp_servers":[],"effort":"%s"}\n' \
        "$session" "$wt" "$model" "$effort"
    printf '{"type":"assistant","session_id":"%s","message":{"id":"msg_%s","type":"message","role":"assistant","model":"%s","content":[{"type":"text","text":"fake agent (%s) for prompt: %s"}],"stop_reason":"end_turn","stop_sequence":null,%s}}\n' \
        "$session" "$session" "$model" "$summary" "${first_line//\"/\\\"}" "$usage"
    printf '{"type":"result","subtype":"success","is_error":false,"duration_ms":%s,"duration_api_ms":%s,"num_turns":1,"max_turns":%s,"result":"fake agent %s","session_id":"%s","total_cost_usd":0,%s}\n' \
        "$((end_ms - start_ms))" "$((end_ms - start_ms))" "$max_turns" "$summary" "$session" "$usage"
} >"$transcript"
