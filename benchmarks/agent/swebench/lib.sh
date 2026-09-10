# shellcheck shell=bash
# The SWE-bench task source for run.sh: `--task swebench:<instance_id>`. Sourced
# by run.sh, never executed; it uses run.sh's die, note, now_ms, json_escape,
# write_meta, write_timing and its RESULTS_DIR, TIMEOUT_S and POLL_S.
#
# A fixture task runs on a host worktree. A SWE-bench instance cannot: the
# repo's Python environment exists only inside the instance's published image,
# so the whole trial (seed, provision, probe, agent, diff) runs in a container
# layered on that image, and grading runs in a second, fresh container from the
# instance image itself, where the agent's diff is applied and the row's
# eval_script produces the log swegrade reads.
#
# Images come from Epoch AI's registry, which publishes every Verified instance
# for x86_64 and a best-effort set for arm64, selected by the HOST's architecture
# so a trial never runs emulated: wall clock is a headline metric, and an
# emulated container is several times slower than a native one. The upstream
# Docker Hub images are amd64 only and are used only under BENCH_EMULATE=1. An
# arm64 image the registry calls untested is validated the same way every
# instance is, by its golden and null controls.

SWEBENCH_DIR="$HERE/swebench"
SWEBENCH_ROOT="$(cd "$HERE/../.." && pwd)"
# swegrade runs on the host: it reads a log and a row and touches nothing else, and
# building it from the root module keeps it under the workspace's own tests.
SWEBENCH_GRADER="$HERE/bin/swegrade"
SWEBENCH_REGISTRY="ghcr.io/epoch-research/swe-bench.eval"
# The reference harness's own per-instance eval timeout.
SWEBENCH_EVAL_TIMEOUT_S="${BENCH_EVAL_TIMEOUT_S:-1800}"

# swebench_arch maps the host to the registry's architecture label, or to amd64
# under BENCH_EMULATE=1, where the upstream image is the only one that exists.
swebench_arch() {
    if [[ ${BENCH_EMULATE:-} == 1 ]]; then
        printf 'x86_64\n'
        return
    fi
    case $(uname -m) in
        arm64 | aarch64) printf 'arm64\n' ;;
        x86_64 | amd64) printf 'x86_64\n' ;;
        *) die "no SWE-bench images exist for host architecture $(uname -m)" ;;
    esac
}

# swebench_platform is the --platform docker is told, always explicit so a
# multi-arch manifest can never quietly hand back the wrong build.
swebench_platform() {
    case $(swebench_arch) in
        arm64) printf 'linux/arm64\n' ;;
        *) printf 'linux/amd64\n' ;;
    esac
}

# swebench_goarch is the Go spelling of the same choice, which names the release
# build and the dist/linux-<goarch> directory a manifest's {arch} expands to.
swebench_goarch() {
    case $(swebench_arch) in
        arm64) printf 'arm64\n' ;;
        *) printf 'amd64\n' ;;
    esac
}

# swebench_image names the instance image to run: the row's own Docker Hub
# image only when emulating, otherwise the registry build for the host.
swebench_image() {
    local row=$1
    if [[ ${BENCH_EMULATE:-} == 1 ]]; then
        jq -r '.image' <<<"$row"
        return
    fi
    printf '%s.%s.%s\n' "$SWEBENCH_REGISTRY" "$(swebench_arch)" "$(jq -r '.instance_id' <<<"$row")"
}

# swebench_row prints the dataset row for an instance id: the committed pilot
# subset first, then the fetched full table when it is present.
swebench_row() {
    local id=$1 file row
    for file in "$SWEBENCH_DIR/pilot.jsonl" "$SWEBENCH_DIR/verified.jsonl"; do
        [[ -f $file ]] || continue
        row=$(jq -c --arg id "$id" 'select(.instance_id == $id)' "$file")
        if [[ -n $row ]]; then
            printf '%s\n' "$row"
            return 0
        fi
    done
    return 1
}

# swebench_check_binary refuses anything but a linux ELF for the container's
# architecture: the file is copied into the image, where a host build would fail
# only once the arm tried to run it, several minutes in.
swebench_check_binary() {
    local bin=$1 magic machine goarch want
    goarch=$(swebench_goarch)
    case $goarch in
        arm64) want=b700 ;;
        *) want=3e00 ;;
    esac
    if [[ -f $bin ]]; then
        magic=$(od -An -tx1 -N4 "$bin" | tr -d ' \n')
        machine=$(od -An -tx1 -j18 -N2 "$bin" | tr -d ' \n')
        [[ $magic == 7f454c46 && $machine == "$want" ]] && return 0
        note "--magus-binary $bin is not a linux/$goarch ELF binary"
    else
        note "--magus-binary $bin does not exist"
    fi
    die "a swebench trial runs in a linux/$goarch container, so it needs that build of magus; from the workspace root:
  magus run release-build . -- linux $goarch
  mkdir -p dist/linux-$goarch
  tar -xzf dist/magus_*_linux_${goarch}_static.tar.gz -C dist/linux-$goarch magus
then pass --magus-binary <root>/dist/linux-$goarch/magus"
}

# swebench_digest is worktree_digest for a checkout inside a container; the
# same content hash of every dirty path, so time_to_first_edit_ms means the
# same thing here as on a host worktree.
swebench_digest() {
    docker exec "$1" sh -c '
        cd /testbed && git status --porcelain | while IFS= read -r line; do
            path=${line#???}
            printf "%s " "$line"
            if [ -f "$path" ]; then cksum <"$path"; else echo; fi
        done' 2>/dev/null
}

# swebench_exec runs one step inside the trial container from the mounted
# checkout, which is how the unchanged arm scripts provision a container path.
swebench_exec() {
    local cname=$1
    shift
    docker exec "$cname" "$@"
}

swebench_run_one() {
    local arm=$1 task=$2 rep=$3 model=$4 effort=$5 magus_binary=$6 control=$7
    local id=${task#swebench:} arm_dir="$HERE/arms/$arm" row image base_commit

    [[ -d $arm_dir ]] || die "no such arm: $arm"
    row=$(swebench_row "$id") || die "no SWE-bench instance $id in swebench/pilot.jsonl or swebench/verified.jsonl (run swebench/fetch.sh for the full table)"
    image=$(swebench_image "$row")
    base_commit=$(jq -r '.base_commit' <<<"$row")

    local budget max_turns bench_image magus_version stamp run_id out cname platform
    budget=$(meta_get "$SWEBENCH_DIR/meta.json" budget_usd)
    max_turns=$(meta_get "$SWEBENCH_DIR/meta.json" max_turns)
    platform=$(swebench_platform)

    bench_image=$("$SWEBENCH_DIR/images.sh" "$image" "$platform" "$magus_binary") ||
        die "building the trial image for $id failed"
    [[ -x $SWEBENCH_GRADER ]] || die "swegrade is not built at $SWEBENCH_GRADER; from the workspace root:
  magus run swegrade-build ."
    magus_version=$(docker run --rm --platform "$platform" "$bench_image" magus --version 2>/dev/null || true)
    magus_version=${magus_version:-unknown}

    while :; do
        stamp=$(date -u +%Y%m%dT%H%M%SZ)
        run_id="$arm-swebench-$id-r$rep-$stamp"
        out="$RESULTS_DIR/$run_id"
        if mkdir "$out" 2>/dev/null; then
            break
        fi
        sleep 1
    done
    cname="magus-bench-$run_id"

    local started ended exit_reason check_exit t_start t_first_edit t_done
    started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    exit_reason=ok
    check_exit=-1
    t_first_edit=0

    # Everything the containers read is staged in the run directory, which is
    # bind-mounted at /work in both: the prompt lands outside the repo, and the
    # transcript streams straight to the host.
    printf '%s\n' "$row" >"$out/row.json"
    jq -r '.problem_statement' <<<"$row" >"$out/prompt.md"
    jq -r '.patch' <<<"$row" >"$out/gold.patch"
    jq -r '.test_patch' <<<"$row" >"$out/test.patch"
    jq -r '.eval_script' <<<"$row" >"$out/eval.sh"

    docker run -d --platform "$platform" --name "$cname" \
        -v "$out:/work" -v "$SWEBENCH_ROOT:/src:ro" \
        "$bench_image" sleep infinity >>"$out/setup.log" 2>&1 ||
        die "starting the trial container for $run_id failed (see $out/setup.log)"

    t_start=$(now_ms)
    if ! swebench_exec "$cname" sh /src/benchmarks/agent/swebench/seed.sh /testbed "$id" >>"$out/setup.log" 2>&1; then
        exit_reason=seed_failed
    elif ! swebench_exec "$cname" sh "/src/benchmarks/agent/arms/$arm/provision.sh" /testbed /usr/local/bin/magus >>"$out/setup.log" 2>&1; then
        exit_reason=provision_failed
    elif ! swebench_exec "$cname" sh "/src/benchmarks/agent/arms/$arm/probe.sh" /testbed >"$out/probe.txt" 2>&1; then
        exit_reason=probe_failed
        note "PROBE FAILED for $run_id; the container is not in arm '$arm'. See $out/probe.txt"
    fi
    t_done=$t_start

    if [[ $exit_reason == ok ]]; then
        case $control in
            golden)
                swebench_exec "$cname" git -C /testbed apply --verbose /work/gold.patch >>"$out/setup.log" 2>&1 ||
                    exit_reason=control_error
                ;;
            null) ;;
            *) swebench_run_agent "$cname" "$out" "$max_turns" "$model" "$effort" "$budget" ;;
        esac
        t_done=$(now_ms)

        swebench_exec "$cname" sh -c 'cd /testbed && git add -A -N && git diff' >"$out/final.diff" 2>>"$out/setup.log" || true
        if swebench_exec "$cname" test -d /testbed/.magus/activity; then
            docker cp "$cname:/testbed/.magus/activity" "$out/activity" >>"$out/setup.log" 2>&1 || true
        fi

        swebench_grade "$out" "$image"
        check_exit=$(cat "$out/check.exit")
    fi

    if ((check_exit >= 0)); then
        case $control in
            golden)
                if ((check_exit == 0)); then
                    exit_reason=control_golden_ok
                else
                    exit_reason=control_golden_failed
                    note "CONTROL FAILED: the gold patch for '$id' did not resolve the instance ($out)"
                    CONTROLS_FAILED=$((CONTROLS_FAILED + 1))
                fi
                ;;
            null)
                if ((check_exit != 0)); then
                    exit_reason=control_null_ok
                else
                    exit_reason=control_null_failed
                    note "CONTROL FAILED: instance '$id' resolves with no patch at all ($out)"
                    CONTROLS_FAILED=$((CONTROLS_FAILED + 1))
                fi
                ;;
        esac
    fi

    ended=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    write_meta "$out" "$run_id" "$arm" "$task" "$rep" "$model" "$effort" \
        "$max_turns" "$budget" "$magus_binary" "$magus_version" "$base_commit" \
        "$started" "$ended" "$exit_reason" "$control" \
        "\"task_source\": \"swebench\",
  \"instance_id\": \"$(json_escape "$id")\",
  \"image\": \"$(json_escape "$image")\",
  \"platform\": \"$platform\","
    write_timing "$out" "$t_start" "$t_first_edit" "$t_done"

    if [[ ${BENCH_KEEP_WORKTREE:-} == 1 ]]; then
        note "kept trial container $cname"
    else
        docker rm -f "$cname" >>"$out/setup.log" 2>&1 || true
    fi

    printf '%s %s check=%s\n' "$run_id" "$exit_reason" "$check_exit"
}

# swebench_run_agent launches agent.sh inside the trial container with the same
# contract it has on the host. The credential reaches the container by NAME
# through docker's -e, so it is never on a command line, in a file, or in the
# image. The wall clock is enforced by coreutils timeout inside the container;
# the host loop only reads the outcome. T_FIRST_EDIT and EXIT_REASON are set
# through the caller's locals, which is why this is not a subshell.
swebench_run_agent() {
    local cname=$1 out=$2 max_turns=$3 model=$4 effort=$5 budget=$6
    local baseline env_args=() name
    baseline=$(swebench_digest "$cname")

    for name in ANTHROPIC_API_KEY ANTHROPIC_BASE_URL CLAUDE_CODE_OAUTH_TOKEN; do
        if [[ -n ${!name:-} ]]; then
            env_args+=(-e "$name")
        fi
    done

    docker exec "${env_args[@]}" -e "RUNNER_BUDGET_USD=$budget" "$cname" \
        timeout -s TERM "$TIMEOUT_S" \
        bash /src/benchmarks/agent/runner/agent.sh /testbed /work/prompt.md /work/transcript.jsonl \
        "$max_turns" "$model" "$effort" >"$out/agent.log" 2>&1 &
    local apid=$! agent_exit=0

    while kill -0 "$apid" 2>/dev/null; do
        if ((t_first_edit == 0)) && [[ $(swebench_digest "$cname") != "$baseline" ]]; then
            t_first_edit=$(now_ms)
        fi
        sleep "$POLL_S"
    done
    wait "$apid" || agent_exit=$?
    if ((agent_exit == 124)); then
        exit_reason=timeout
    elif ((agent_exit != 0)); then
        exit_reason=agent_error
    fi
    if ((t_first_edit == 0)) && [[ $(swebench_digest "$cname") != "$baseline" ]]; then
        t_first_edit=$(now_ms)
    fi
}

# swebench_grade applies final.diff in a fresh container from the instance
# image, runs the row's eval script there, and hands the log to swegrade. It
# writes eval.log, check.txt (the JSON verdict) and check.exit.
swebench_grade() {
    local out=$1 image=$2 eval_exit=0 check_exit
    docker run --rm --platform "$(swebench_platform)" -v "$out:/work" -v "$SWEBENCH_ROOT:/src:ro" "$image" \
        timeout -s TERM "$SWEBENCH_EVAL_TIMEOUT_S" \
        bash /src/benchmarks/agent/swebench/grade.sh /work/final.diff >"$out/eval.log" 2>&1 || eval_exit=$?
    if ((eval_exit == 124)); then
        printf '\n>>>>> Tests Timed Out\n' >>"$out/eval.log"
    fi
    set +e
    "$SWEBENCH_GRADER" --instance "$out/row.json" <"$out/eval.log" >"$out/check.txt" 2>>"$out/setup.log"
    check_exit=$?
    set -e
    printf '%s\n' "$check_exit" >"$out/check.exit"
}
