#!/bin/sh
# Regenerate kernels.wasm from kernels.c. Needs clang with the wasm32 target
# (Ubuntu: apt install clang lld). The .wasm is committed so the wazero engine
# runs with no toolchain; rerun this only after editing kernels.c.
set -eu
cd "$(dirname "$0")"
clang --target=wasm32 -O2 -nostdlib -Wl,--no-entry -Wl,--strip-all -o kernels.wasm kernels.c
echo "built kernels.wasm ($(wc -c < kernels.wasm) bytes)"
