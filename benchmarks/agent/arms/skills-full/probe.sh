#!/usr/bin/env sh
unset BENCH_SKILLS_DIR
ARM_NAME=skills-full BENCH_SKILL_FORM=full exec sh "$(dirname "$0")/../full/probe.sh" "$@"
