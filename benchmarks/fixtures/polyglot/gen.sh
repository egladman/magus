#!/usr/bin/env bash
# gen.sh — generate a fixed-shape polyglot workspace:
#   schema/   proto definition (cross-language dependency)
#   go-svc/   Go service (depends on schema)
#   ts-lib/   TypeScript library
#   ts-app/   TypeScript app (depends on ts-lib)
#   py-tool/  Python package
#
# Applicable tools: magus, make, moon
# turbo/nx/lage are n/a (JS-only tools).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
GEN="$SCRIPT_DIR/gen"

rm -rf "$GEN"
mkdir -p \
    "$GEN/schema" \
    "$GEN/go-svc" \
    "$GEN/ts-lib/src" \
    "$GEN/ts-app/src" \
    "$GEN/py-tool/src"

# ── go.work ───────────────────────────────────────────────────────────────────
cat > "$GEN/go.work" <<'GOWORK'
go 1.23

use (
    ./go-svc
)
GOWORK

# ── magus config (root marker + per-project magusfiles) ──────────────────────
# ts-app depends on ts-lib, declared twice (depends_on for affected, magus.needs
# for ordering/caching). schema/ is not a project: nothing actually imports it,
# matching the Makefile.
cat > "$GEN/magus.yaml" <<'YAML'
telemetry:
  enabled: false
YAML

cat > "$GEN/go-svc/magusfile.buzz" <<'MF'
import "magus";
import "magus/spell/go";
magus.project.register(fun(p, cb) > bool { cb({"spells": [go]}); return true; });
export fun build(args: [str]) > void { go["go-build"](); }
MF

cat > "$GEN/ts-lib/magusfile.buzz" <<'MF'
import "magus";
import "magus/spell/ts";
magus.project.register(fun(p, cb) > bool { cb({"spells": [ts]}); return true; });
export fun build(args: [str]) > void { ts["tsc"]({"args": ["-b"]}); }
MF

cat > "$GEN/ts-app/magusfile.buzz" <<'MF'
import "magus";
import "magus/spell/ts";
import "project/ts-lib" as tslib;
magus.project.register(fun(p, cb) > bool { cb({"spells": [ts], "depends_on": ["ts-lib"]}); return true; });
export fun build(args: [str]) > void { magus.needs(tslib.build); ts["tsc"]({"args": ["-b"]}); }
MF

cat > "$GEN/py-tool/magusfile.buzz" <<'MF'
import "magus";
import "magus/spell/py";
import "os";
magus.project.register(fun(p, cb) > bool { cb({"spells": [py]}); return true; });
export fun build(args: [str]) > void { os.exec("python", ["-m", "py_compile", "src/tool.py"]); }
MF

# ── Makefile ──────────────────────────────────────────────────────────────────
cat > "$GEN/Makefile" <<'MAKE'
.PHONY: all clean

all:
	go build -o out/go-svc ./go-svc
	cd ts-lib && pnpm tsc -b
	cd ts-app && pnpm tsc -b
	cd py-tool && python -m py_compile src/tool.py

clean:
	rm -rf out ts-lib/dist ts-app/dist
MAKE

# ── schema ────────────────────────────────────────────────────────────────────
cat > "$GEN/schema/api.proto" <<'PROTO'
syntax = "proto3";

package bench.api;
option go_package = "bench/svc/api";

message Request {
  string id = 1;
}

message Response {
  string result = 1;
}
PROTO

cat > "$GEN/schema/README.md" <<'MD'
# schema

Cross-language contract. Both go-svc and ts-app consume this definition.
Changing api.proto triggers downstream rebuilds in both Go and TypeScript.
MD

# ── go-svc ────────────────────────────────────────────────────────────────────
cat > "$GEN/go-svc/go.mod" <<'GOMOD'
module bench/svc

go 1.23
GOMOD

cat > "$GEN/go-svc/main.go" <<'GO'
package main

import "fmt"

// NOTE: in a real project this would import generated proto code from schema/.
// For the benchmark we keep it dependency-free to avoid protoc toolchain setup.
const protoVersion = "api.v1"

func main() {
	fmt.Printf("go-svc using %s\n", protoVersion)
}
GO

# ── ts-lib ────────────────────────────────────────────────────────────────────
cat > "$GEN/ts-lib/package.json" <<'JSON'
{
  "name": "@bench/ts-lib",
  "version": "0.0.0",
  "main": "dist/index.js",
  "types": "dist/index.d.ts",
  "scripts": { "build": "tsc -b" }
}
JSON

cat > "$GEN/ts-lib/tsconfig.json" <<'JSON'
{
  "compilerOptions": {
    "target": "ES2020",
    "module": "commonjs",
    "outDir": "dist",
    "rootDir": "src",
    "composite": true,
    "declaration": true,
    "strict": true
  },
  "include": ["src/**/*"]
}
JSON

cat > "$GEN/ts-lib/src/index.ts" <<'TS'
// Simulates a library that wraps the schema API.
export const apiVersion = "api.v1";
export const describe = (): string => `ts-lib using ${apiVersion}`;
TS

cat > "$GEN/ts-lib/moon.yml" <<'YAML'
language: typescript
type: library
tasks:
  build:
    command: "pnpm tsc -b"
    outputs: ["dist"]
YAML

# ── ts-app ────────────────────────────────────────────────────────────────────
cat > "$GEN/ts-app/package.json" <<'JSON'
{
  "name": "@bench/ts-app",
  "version": "0.0.0",
  "private": true,
  "scripts": { "build": "tsc -b" },
  "dependencies": {
    "@bench/ts-lib": "workspace:*"
  }
}
JSON

cat > "$GEN/ts-app/tsconfig.json" <<'JSON'
{
  "compilerOptions": {
    "target": "ES2020",
    "module": "commonjs",
    "outDir": "dist",
    "rootDir": "src",
    "composite": true,
    "strict": true
  },
  "references": [
    { "path": "../ts-lib" }
  ],
  "include": ["src/**/*"]
}
JSON

cat > "$GEN/ts-app/src/index.ts" <<'TS'
import { describe } from "@bench/ts-lib";
console.log(describe());
TS

cat > "$GEN/ts-app/moon.yml" <<'YAML'
language: typescript
type: application
dependsOn:
  - ts-lib
tasks:
  build:
    command: "pnpm tsc -b"
    outputs: ["dist"]
    deps: ["^:build"]
YAML

# ── py-tool ───────────────────────────────────────────────────────────────────
cat > "$GEN/py-tool/pyproject.toml" <<'TOML'
[project]
name = "py-tool"
version = "0.0.0"
requires-python = ">=3.9"
TOML

cat > "$GEN/py-tool/src/tool.py" <<'PY'
"""Simple Python tool for the polyglot benchmark fixture."""


def run() -> str:
    return "py-tool"


if __name__ == "__main__":
    print(run())
PY

# ── pnpm workspace (ts-lib + ts-app only) ────────────────────────────────────
cat > "$GEN/pnpm-workspace.yaml" <<'YAML'
packages:
  - "ts-lib"
  - "ts-app"
YAML

# ── moon workspace ────────────────────────────────────────────────────────────
mkdir -p "$GEN/.moon"
cat > "$GEN/.moon/workspace.yml" <<'YAML'
$schema: "https://moonrepo.dev/schemas/workspace.json"
projects:
  - "ts-lib"
  - "ts-app"
YAML
cat > "$GEN/.moon/toolchain.yml" <<'YAML'
$schema: "https://moonrepo.dev/schemas/toolchain.json"
node:
  version: "22.22.2"
  packageManager: "pnpm"
YAML

# ── bench marker files ────────────────────────────────────────────────────────
# S6 leaf: ts-app (no downstream dependents)
echo "ts-app/src/index.ts" > "$SCRIPT_DIR/.bench-leaf-file"
# S7 upstream: ts-lib (ts-app depends on it)
echo "ts-lib/src/index.ts" > "$SCRIPT_DIR/.bench-upstream-file"

echo "generated polyglot fixture → $GEN" >&2
