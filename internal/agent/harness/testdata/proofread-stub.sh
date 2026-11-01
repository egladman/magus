#!/bin/sh
# Stands in for the proofread release binary: proofread-reply.buzz pipes the reply in,
# and the file this leaves is the evidence that the reply check ran at turn end.
calls="${AGENT_HARNESS_PROOFREAD_CALLS:?}"
mkdir -p "$calls"
cat >"$calls/$$.reply"
printf '%s\n' "$*" >"$calls/$$.args"
exit 0
