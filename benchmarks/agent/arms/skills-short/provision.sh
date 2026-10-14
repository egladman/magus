#!/usr/bin/env sh
# Arm "full" with one short-form body per skill.
unset BENCH_SKILLS_DIR
ARM_NAME=skills-short BENCH_SKILL_FORM=short exec sh "$(dirname "$0")/../full/provision.sh" "$@"
