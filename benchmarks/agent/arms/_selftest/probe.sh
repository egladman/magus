#!/usr/bin/env bash
# BENCH_PROBE_FAIL=1 forces the failure branch so the abort path stays exercised.
set -euo pipefail
if [[ ${BENCH_PROBE_FAIL:-0} == 1 ]]; then
    echo "selftest probe: forced failure"
    exit 1
fi
if [[ ! -d $1 ]]; then
    echo "selftest probe: no worktree at $1"
    exit 1
fi
echo "selftest probe: worktree present, arm provisions nothing"
