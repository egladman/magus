#!/usr/bin/env sh
# Arm "full" with the skills copied from BENCH_C1_DIR, a compressed short-form
# set the caller supplies, in place of the installed ones.
ARM_NAME=skills-c1 BENCH_SKILL_FORM=short BENCH_SKILLS_DIR=${BENCH_C1_DIR:?set BENCH_C1_DIR to the compressed skills dir} \
    exec sh "$(dirname "$0")/../full/provision.sh" "$@"
