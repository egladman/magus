#!/usr/bin/env sh
ARM_NAME=skills-c1 BENCH_SKILL_FORM=short BENCH_SKILLS_DIR=${BENCH_C1_DIR:?set BENCH_C1_DIR to the compressed skills dir} \
    exec sh "$(dirname "$0")/../full/probe.sh" "$@"
