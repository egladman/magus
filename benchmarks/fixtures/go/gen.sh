#!/usr/bin/env bash
# gen.sh N — generate a synthetic Go workspace with N independent services.
#
# Output: ./gen/ (deterministic; same N → byte-identical tree)
# Writes .bench-leaf-file and .bench-upstream-file alongside gen/.
set -euo pipefail

N="${1:-8}"

if ! [[ "$N" =~ ^[0-9]+$ ]] || [[ "$N" -lt 1 ]]; then
    echo "usage: gen.sh <N>" >&2; exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
GEN="$SCRIPT_DIR/gen"

rm -rf "$GEN"
mkdir -p "$GEN"

# ── go.work ──────────────────────────────────────────────────────────────────
{
    echo "go 1.23"
    echo ""
    echo "use ("
    for i in $(seq 0 $(( N - 1 ))); do
        echo "    ./svc-$i"
    done
    echo ")"
} > "$GEN/go.work"

# ── magus.yaml (workspace root marker) ────────────────────────────────────────
# Marks gen/ as the workspace root without making it a project.
cat > "$GEN/magus.yaml" <<'YAML'
telemetry:
  enabled: false
YAML

# ── Makefile ─────────────────────────────────────────────────────────────────
{
    printf 'SERVICES :='
    for i in $(seq 0 $(( N - 1 ))); do printf ' svc-%d' "$i"; done
    printf '\nBINS     := $(addprefix out/,$(SERVICES))\n'
    printf '\n.PHONY: all clean\n'
    printf 'all: $(BINS)\n'
    printf '\n$(BINS): out/%%: %%/main.go | out/\n'
    printf '\tgo build -o $@ ./$*\n'
    printf '\nout/:\n'
    printf '\tmkdir -p out\n'
    printf '\nclean:\n'
    printf '\trm -rf out\n'
} > "$GEN/Makefile"

# ── per-service ───────────────────────────────────────────────────────────────
for i in $(seq 0 $(( N - 1 ))); do
    svc="$GEN/svc-$i"
    mkdir -p "$svc"
    cat > "$svc/go.mod" <<GOMOD
module bench/svc-$i

go 1.23
GOMOD
    cat > "$svc/main.go" <<GOMAIN
package main

import "fmt"

func main() {
	fmt.Println("svc-$i")
}
GOMAIN

    # Per-service magusfile: go spell, build = go build. Services are independent.
    cat > "$svc/magusfile.buzz" <<'MAGUSFILE'
import "magus";
import "magus/spell/go";

magus.project.register(fun(p, cb) > bool { cb({"spells": [go]}); return true; });

export fun build(args: [str]) > void { go["go-build"](); }
MAGUSFILE
done

# ── bench marker files ────────────────────────────────────────────────────────
# Leaf: first service (no downstream dependents)
echo "svc-0/main.go" > "$SCRIPT_DIR/.bench-leaf-file"
# Upstream: same as leaf for Go fixture (services are independent; no graph)
# S7 is n/a for Go fixture; this file is written for bench.sh uniformity.
echo "svc-0/main.go" > "$SCRIPT_DIR/.bench-upstream-file"

echo "generated $N Go services → $GEN" >&2
