#!/usr/bin/env bash
# grade.sh <patch>: apply a patch to /testbed the way the reference harness does,
# then run the instance's eval script. Runs INSIDE a fresh instance container;
# stdout is the log swegrade reads, so the harness's own markers are printed here.
#
# An empty patch file applies nothing, which is the null control.
set -uo pipefail

patch_file=$1
cd /testbed || exit 1
git config --global --add safe.directory /testbed

if [[ -s $patch_file ]]; then
    applied=0
    first=1
    # The same ladder as swebench.harness.constants.GIT_APPLY_CMDS. The tree is
    # reset before each RETRY, never before the first rung: ignored build
    # products in the image survive `git clean -fd`, but a rejected attempt's
    # partial hunks would not survive the next rung otherwise.
    for cmd in "git apply --verbose" "git apply --verbose --3way" "git apply --verbose --reject" \
        "patch --batch --forward --fuzz=5 -p1 -i"; do
        if ((!first)); then
            git checkout -- . && git clean -fd
        fi
        first=0
        # shellcheck disable=SC2086  # each rung is a fixed command line, split on purpose
        if $cmd "$patch_file"; then
            applied=1
            break
        fi
    done
    if ((applied)); then
        echo ">>>>> Applied Patch"
    else
        echo ">>>>> Patch Apply Failed"
        exit 1
    fi
fi

exec bash /work/eval.sh
