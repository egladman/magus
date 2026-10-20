#!/usr/bin/env sh
unset BENCH_SKILLS_DIR
ARM_NAME=skills-short BENCH_SKILL_FORM=short exec sh "$(dirname "$0")/../full/probe.sh" "$@"
