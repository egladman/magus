#!/usr/bin/env sh
# Arm "full" with one full-form body per skill.
unset BENCH_SKILLS_DIR
ARM_NAME=skills-full BENCH_SKILL_FORM=full exec sh "$(dirname "$0")/../full/provision.sh" "$@"
