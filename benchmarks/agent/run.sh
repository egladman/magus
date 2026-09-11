#!/usr/bin/env bash
# run.sh: execute agent-benchmark runs, one throwaway fixture worktree each.
#
# The runner is deliberately dumb: it provisions, launches one agent session,
# and records everything. Nothing here decides anything about a run's outcome
# except the task's own check.sh exit code.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
RESULTS_DIR="${BENCH_RESULTS_DIR:-$HERE/results}"
# Absolute for the same reason --magus-binary is made absolute below: the agent step
# runs from inside the fixture worktree, where a relative results path names nothing.
[[ $RESULTS_DIR == /* ]] || RESULTS_DIR="$PWD/$RESULTS_DIR"
FIXTURE_REPO="${BENCH_FIXTURE_REPO:-$HERE/../large-monorepo/gen/repo}"
TIMEOUT_S="${BENCH_TIMEOUT_S:-1800}"
POLL_S="${BENCH_POLL_S:-0.5}"

usage() {
    cat <<'EOF'
usage:
  run.sh --arm <arm> --task <task> --reps <n> --model <m> --effort <e>
         --magus-binary <path> [--dry-run] [--control golden|null]
  run.sh --manifest <file>

A task is a directory under tasks/, run on a worktree of the fixture repo, or
swebench:<instance_id>, run inside that SWE-bench instance's container for the
host's own architecture (see swebench/lib.sh; --magus-binary must then be the
linux build for it, and BENCH_EMULATE=1 is the only way to run amd64 elsewhere).

A manifest is a text file of run.sh flag lines; blank lines and lines starting
with # are skipped. Each line is executed as its own run.sh invocation. A line
that fails does not stop the ones after it; the failures are listed at the end
and the exit status is 1 if there were any.

env:
  BENCH_FIXTURE_REPO    git repo each run worktree is cut from
  BENCH_RESULTS_DIR     where run directories are written
  BENCH_TIMEOUT_S       wall-clock cap per agent invocation (default 1800)
  BENCH_EVAL_TIMEOUT_S  wall-clock cap for a swebench eval script (default 1800)
  BENCH_POLL_S          worktree dirty-poll interval (default 0.5)
  BENCH_KEEP_WORKTREE   1 keeps the run worktree (or trial container) for inspection
  BENCH_PROBE_FAIL      honored only by the self-test arm; forces a probe failure
  RUNNER_AGENT          replaces runner/agent.sh (runner/fake-agent.sh for tests)
EOF
}

die() { printf 'run.sh: %s\n' "$*" >&2; exit 2; }
note() { printf 'run.sh: %s\n' "$*" >&2; }

now_ms() {
    if [[ -n "${EPOCHREALTIME:-}" ]]; then
        local us="${EPOCHREALTIME/[.,]/}"
        printf '%s\n' "$((10#$us / 1000))"
    else
        perl -MTime::HiRes -e 'printf "%.0f\n", Time::HiRes::time()*1000'
    fi
}

json_escape() {
    local s=$1
    s=${s//\\/\\\\}
    s=${s//\"/\\\"}
    # A value can span lines: `magus --version` adds a second one when the daemon it
    # found was built from a different commit.
    s=${s//$'\n'/\\n}
    s=${s//$'\t'/\\t}
    printf '%s' "$s"
}

# worktree_digest is the edit signal behind time_to_first_edit_ms. It hashes the
# CONTENT of every path git calls dirty, not just the porcelain lines: a task
# that seeds an untracked file keeps the same porcelain line when an agent
# rewrites it, and polling the lines alone reports the edit as never happening.
worktree_digest() {
    local wt=$1 line path
    git -C "$wt" status --porcelain 2>/dev/null | while IFS= read -r line; do
        path=${line:3}
        printf '%s ' "$line"
        if [[ -f $wt/$path ]]; then
            cksum <"$wt/$path"
        else
            printf '\n'
        fi
    done
}

# meta_get reads one scalar out of a task's meta.json. The file is ours and
# flat, so a jq dependency on every benchmark host is not worth taking on.
meta_get() {
    sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\"\{0,1\}\([^\",}]*\)\"\{0,1\}.*/\1/p" "$1" | head -1
}

# shellcheck source=swebench/lib.sh disable=SC1091
. "$HERE/swebench/lib.sh"

run_one() {
    local arm=$1 task=$2 rep=$3 model=$4 effort=$5 magus_binary=$6 control=$7
    local task_dir="$HERE/tasks/$task" arm_dir="$HERE/arms/$arm"

    [[ -d $task_dir ]] || die "no such task: $task"
    [[ -d $arm_dir ]] || die "no such arm: $arm"
    [[ -d $FIXTURE_REPO/.git ]] || die "fixture repo is not a git repo: $FIXTURE_REPO"

    local budget max_turns fixture_sha magus_version stamp run_id out wt
    budget=$(meta_get "$task_dir/meta.json" budget_usd)
    max_turns=$(meta_get "$task_dir/meta.json" max_turns)
    fixture_sha=$(git -C "$FIXTURE_REPO" rev-parse HEAD)
    magus_version=""
    if [[ -x $magus_binary ]]; then
        magus_version=$("$magus_binary" --version 2>/dev/null || true)
    fi
    magus_version=${magus_version:-unknown}

    # The run-id stamp resolves to a second, and two runs of the same (arm,
    # task, rep) land inside one: the golden and null controls both do. Waiting
    # out the collision costs a second; overwriting costs a run's artifacts.
    while :; do
        stamp=$(date -u +%Y%m%dT%H%M%SZ)
        run_id="$arm-$task-r$rep-$stamp"
        out="$RESULTS_DIR/$run_id"
        if mkdir "$out" 2>/dev/null; then
            break
        fi
        sleep 1
    done
    wt=$(mktemp -d "${TMPDIR:-/tmp}/magus-bench.XXXXXX")

    local started ended exit_reason check_exit t_start t_first_edit t_done
    started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    exit_reason=ok
    check_exit=-1
    t_first_edit=0

    git -C "$FIXTURE_REPO" worktree add --detach "$wt" "$fixture_sha" >>"$out/setup.log" 2>&1

    t_start=$(now_ms)
    if ! bash "$task_dir/seed.sh" "$wt" >>"$out/setup.log" 2>&1; then
        exit_reason=seed_failed
    elif ! bash "$arm_dir/provision.sh" "$wt" "$magus_binary" >>"$out/setup.log" 2>&1; then
        exit_reason=provision_failed
    elif ! bash "$arm_dir/probe.sh" "$wt" >"$out/probe.txt" 2>&1; then
        exit_reason=probe_failed
        note "PROBE FAILED for $run_id; the worktree is not in arm '$arm'. See $out/probe.txt"
    fi
    t_done=$t_start

    if [[ $exit_reason == ok ]]; then
        cp "$task_dir/task.md" "$out/prompt.md"
        case $control in
            golden)
                bash "$task_dir/solution.sh" "$wt" >>"$out/setup.log" 2>&1 || exit_reason=control_error
                ;;
            null) ;;
            *) run_agent "$wt" "$out" "$task_dir" "$max_turns" "$model" "$effort" "$budget" ;;
        esac
        t_done=$(now_ms)

        git -C "$wt" add -A -N >/dev/null 2>&1 || true
        git -C "$wt" diff >"$out/final.diff" 2>>"$out/setup.log" || true
        if [[ -d $wt/.magus/activity ]]; then
            cp -R "$wt/.magus/activity" "$out/activity"
        fi

        set +e
        bash "$task_dir/check.sh" "$wt" >"$out/check.txt" 2>&1
        check_exit=$?
        set -e
        printf '%s\n' "$check_exit" >"$out/check.exit"
    fi

    # A control whose setup never reached check.sh says nothing about the
    # checker, so the setup failure stays as the exit reason.
    if ((check_exit >= 0)); then
        case $control in
            golden)
                if ((check_exit == 0)); then
                    exit_reason=control_golden_ok
                else
                    exit_reason=control_golden_failed
                    note "CONTROL FAILED: the golden solution for task '$task' did not pass its own check ($out)"
                    CONTROLS_FAILED=$((CONTROLS_FAILED + 1))
                fi
                ;;
            null)
                if ((check_exit != 0)); then
                    exit_reason=control_null_ok
                else
                    exit_reason=control_null_failed
                    note "CONTROL FAILED: task '$task' passes its check with no agent at all ($out)"
                    CONTROLS_FAILED=$((CONTROLS_FAILED + 1))
                fi
                ;;
        esac
    fi

    ended=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    write_meta "$out" "$run_id" "$arm" "$task" "$rep" "$model" "$effort" \
        "$max_turns" "$budget" "$magus_binary" "$magus_version" "$fixture_sha" \
        "$started" "$ended" "$exit_reason" "$control" ""
    write_timing "$out" "$t_start" "$t_first_edit" "$t_done"

    if [[ ${BENCH_KEEP_WORKTREE:-} == 1 ]]; then
        note "kept worktree $wt"
    else
        git -C "$FIXTURE_REPO" worktree remove --force "$wt" >>"$out/setup.log" 2>&1 ||
            { rm -rf "$wt"; git -C "$FIXTURE_REPO" worktree prune >>"$out/setup.log" 2>&1; }
    fi

    printf '%s %s check=%s\n' "$run_id" "$exit_reason" "$check_exit"
}

# run_agent launches the one agent session and watches it. T_FIRST_EDIT and
# EXIT_REASON are set through the caller's locals, which is why this is not a
# subshell.
run_agent() {
    local wt=$1 out=$2 task_dir=$3 max_turns=$4 model=$5 effort=$6 budget=$7
    local agent="${RUNNER_AGENT:-$HERE/runner/agent.sh}"

    # A task may seed a dirty worktree, so "dirty" is not the edit signal;
    # "different from how seeding and provisioning left it" is.
    local baseline
    baseline=$(worktree_digest "$wt")

    RUNNER_TASK_DIR="$task_dir" RUNNER_BUDGET_USD="$budget" \
        "$agent" "$wt" "$out/prompt.md" "$out/transcript.jsonl" "$max_turns" "$model" "$effort" \
        >"$out/agent.log" 2>&1 &
    local apid=$! deadline agent_exit=0
    deadline=$(( $(now_ms) + TIMEOUT_S * 1000 ))

    while kill -0 "$apid" 2>/dev/null; do
        if ((t_first_edit == 0)) && [[ $(worktree_digest "$wt") != "$baseline" ]]; then
            t_first_edit=$(now_ms)
        fi
        if (($(now_ms) > deadline)); then
            kill -TERM "$apid" 2>/dev/null || true
            exit_reason=timeout
            break
        fi
        sleep "$POLL_S"
    done
    wait "$apid" || agent_exit=$?
    if [[ $exit_reason == ok ]] && ((agent_exit != 0)); then
        exit_reason=agent_error
    fi
    if ((t_first_edit == 0)) && [[ $(worktree_digest "$wt") != "$baseline" ]]; then
        t_first_edit=$(now_ms)
    fi
}

# The 17th argument is a block of extra members a task source adds, already
# JSON and comma-terminated; empty for a fixture task.
write_meta() {
    local out=$1
    cat >"$out/meta.json" <<EOF
{
  "run_id": "$(json_escape "$2")",
  "arm": "$(json_escape "$3")",
  "task": "$(json_escape "$4")",
  "rep": $5,
  "model": "$(json_escape "$6")",
  "effort": "$(json_escape "$7")",
  "max_turns": $8,
  "budget_usd": $9,
  "magus_binary": "$(json_escape "${10}")",
  "magus_version": "$(json_escape "${11}")",
  "fixture_sha": "$(json_escape "${12}")",
  "started": "${13}",
  "ended": "${14}",
  "exit_reason": "${15}",
  ${17}
  "control": "$(json_escape "${16}")"
}
EOF
}

write_timing() {
    local out=$1 start=$2 first_edit=$3 done_ms=$4 first="null"
    if ((first_edit > 0)); then
        first=$((first_edit - start))
    fi
    cat >"$out/timing.json" <<EOF
{
  "wall_ms": $(( $(now_ms) - start )),
  "time_to_first_edit_ms": $first,
  "time_to_done_ms": $((done_ms - start))
}
EOF
}

# run_manifest keeps going after a failed line: the controls lead a manifest,
# and a failing control must be reported, not abort the grid behind it.
run_manifest() {
    local file=$1 line lineno=0 failed=()
    [[ -f $file ]] || die "no such manifest: $file"
    while IFS= read -r line || [[ -n $line ]]; do
        lineno=$((lineno + 1))
        if [[ -n ${line// /} && $line != \#* ]]; then
            local parts
            read -r -a parts <<<"$line"
            if ! "$0" "${parts[@]}"; then
                note "manifest line $lineno failed: $line"
                failed+=("$lineno")
            fi
        fi
    done <"$file"
    if ((${#failed[@]} > 0)); then
        note "${#failed[@]} manifest line(s) failed: ${failed[*]}"
        exit 1
    fi
}

main() {
    local arm="" task="" reps=1 model="" effort="" magus_binary="" control="" dry=0 manifest=""

    while (($# > 0)); do
        case $1 in
            --arm) arm=$2; shift 2 ;;
            --task) task=$2; shift 2 ;;
            --reps) reps=$2; shift 2 ;;
            --model) model=$2; shift 2 ;;
            --effort) effort=$2; shift 2 ;;
            --magus-binary) magus_binary=$2; shift 2 ;;
            --control) control=$2; shift 2 ;;
            --manifest) manifest=$2; shift 2 ;;
            --dry-run) dry=1; shift ;;
            -h | --help) usage; exit 0 ;;
            *) usage >&2; die "unknown argument: $1" ;;
        esac
    done

    if [[ -n $manifest ]]; then
        run_manifest "$manifest"
        return
    fi

    [[ -n $arm && -n $task && -n $model && -n $effort && -n $magus_binary ]] ||
        { usage >&2; die "--arm, --task, --model, --effort and --magus-binary are required"; }
    [[ -z $control || $control == golden || $control == null ]] ||
        die "--control takes golden or null"
    # Absolute before anything else reads it: the arm writes its directory onto the run's
    # PATH and the runner cds into the worktree, where a relative path resolves to nothing.
    # A manifest line cannot expand $PWD, so this is what lets one say ../../magus.
    # A manifest is written once for every host, so {arch} stands for the container's
    # Go architecture (dist/linux-{arch}/magus) and is filled in here.
    if [[ $task == swebench:* ]]; then
        magus_binary=${magus_binary//\{arch\}/$(swebench_goarch)}
    fi
    if [[ $magus_binary != /* ]]; then
        local bin_dir
        bin_dir=$(cd "$(dirname "$magus_binary")" 2>/dev/null && pwd) ||
            die "--magus-binary $magus_binary: directory does not exist"
        magus_binary=$bin_dir/$(basename "$magus_binary")
    fi
    local swebench=0
    if [[ $task == swebench:* ]]; then
        swebench=1
        swebench_check_binary "$magus_binary"
    fi

    if ((dry)); then
        if ((swebench)); then
            printf 'plan: source=swebench instance=%s arm=%s reps=%s model=%s effort=%s control=%s\n' \
                "${task#swebench:}" "$arm" "$reps" "$model" "$effort" "${control:-none}"
        else
            printf 'plan: fixture=%s arm=%s task=%s reps=%s model=%s effort=%s control=%s\n' \
                "$FIXTURE_REPO" "$arm" "$task" "$reps" "$model" "$effort" "${control:-none}"
        fi
        printf 'plan: magus-binary=%s timeout=%ss results=%s\n' \
            "$magus_binary" "$TIMEOUT_S" "$RESULTS_DIR"
        local r
        for ((r = 1; r <= reps; r++)); do
            if ((swebench)); then
                printf 'plan: %s-swebench-%s-r%s-<utc-stamp>: trial image, trial container: seed, provision, probe, %s, diff; fresh container: apply, eval; swegrade\n' \
                    "$arm" "${task#swebench:}" "$r" "${control:-agent}"
            else
                printf 'plan: %s-%s-r%s-<utc-stamp>: worktree, seed, provision, probe, %s, diff, check\n' \
                    "$arm" "$task" "$r" "${control:-agent}"
            fi
        done
        return
    fi

    mkdir -p "$RESULTS_DIR"
    local r
    for ((r = 1; r <= reps; r++)); do
        if ((swebench)); then
            swebench_run_one "$arm" "$task" "$r" "$model" "$effort" "$magus_binary" "$control"
        else
            run_one "$arm" "$task" "$r" "$model" "$effort" "$magus_binary" "$control"
        fi
    done
}

CONTROLS_FAILED=0
main "$@"
((CONTROLS_FAILED == 0)) || exit 1
