#!/usr/bin/env bash
# gen.sh N — generate a synthetic TypeScript monorepo with N packages.
#
# Layout: N/5 shared libs + N/5 apps (each app depends on all libs).
# Requires: N ≥ 10 and N divisible by 5.
#
# Output  : ./gen/
# Writes  : .bench-leaf-file  (app src file — no downstream consumers)
#           .bench-upstream-file (lib src file — all apps depend on it)
set -euo pipefail

N="${1:-25}"
K="${BENCH_COMPONENTS_PER_LIB:-5}"  # source files per lib

if ! [[ "$N" =~ ^[0-9]+$ ]] || [[ "$N" -lt 10 ]] || [[ $(( N % 5 )) -ne 0 ]]; then
    echo "usage: gen.sh <N>  (N ≥ 10, divisible by 5)" >&2; exit 1
fi

M=$(( N / 5 ))   # number of libs = number of apps

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TMPL="$SCRIPT_DIR/templates"
GEN="$SCRIPT_DIR/gen"

rm -rf "$GEN"
mkdir -p "$GEN/libs" "$GEN/apps"

# ── root config files (copied from templates) ─────────────────────────────────
cp "$TMPL/pnpm-workspace.yaml" "$GEN/"
cp "$TMPL/turbo.json"          "$GEN/"
cp "$TMPL/nx.json"             "$GEN/"
cp "$TMPL/lage.config.js"      "$GEN/"
cp "$TMPL/MODULE.bazel"        "$GEN/"
cp "$TMPL/WORKSPACE"           "$GEN/" 2>/dev/null || true

mkdir -p "$GEN/.moon"
cp "$TMPL/.moon/workspace.yml"  "$GEN/.moon/"
cp "$TMPL/.moon/toolchain.yml"  "$GEN/.moon/"

# ── root package.json ─────────────────────────────────────────────────────────
cat > "$GEN/package.json" <<JSON
{
  "name": "bench-root",
  "version": "0.0.0",
  "private": true,
  "packageManager": "pnpm@$(pnpm --version 2>/dev/null || echo '9.0.0')",
  "scripts": {
    "build": "tsc -b"
  },
  "devDependencies": {
    "typescript": "~5.8.0"
  }
}
JSON

# ── root tsconfig.base.json ───────────────────────────────────────────────────
cat > "$GEN/tsconfig.base.json" <<'JSON'
{
  "compilerOptions": {
    "target": "ES2020",
    "module": "commonjs",
    "strict": true,
    "declaration": true,
    "declarationMap": true,
    "sourceMap": true,
    "esModuleInterop": true,
    "skipLibCheck": true
  }
}
JSON

# ── libs ──────────────────────────────────────────────────────────────────────
for i in $(seq 0 $(( M - 1 ))); do
    lib="$GEN/libs/lib-$i"
    mkdir -p "$lib/src"

    # package.json
    cat > "$lib/package.json" <<JSON
{
  "name": "@bench/lib-$i",
  "version": "0.0.0",
  "main": "dist/index.js",
  "types": "dist/index.d.ts",
  "scripts": {
    "build": "tsc -b"
  }
}
JSON

    # tsconfig.json
    cat > "$lib/tsconfig.json" <<'JSON'
{
  "extends": "../../tsconfig.base.json",
  "compilerOptions": {
    "outDir": "dist",
    "rootDir": "src",
    "composite": true
  },
  "include": ["src/**/*"]
}
JSON

    # moon.yml
    cat > "$lib/moon.yml" <<YAML
language: typescript
type: library
tasks:
  build:
    command: "pnpm tsc -b"
    outputs:
      - "dist"
YAML

    # BUILD.bazel
    cat > "$lib/BUILD.bazel" <<BAZEL
load("@aspect_rules_ts//ts:defs.bzl", "ts_project")

ts_project(
    name = "lib_$i",
    srcs = glob(["src/**/*.ts"]),
    declaration = True,
    visibility = ["//visibility:public"],
)
BAZEL

    # src/Component-X.ts (K components)
    for j in $(seq 0 $(( K - 1 ))); do
        cat > "$lib/src/Component-${j}.ts" <<TS
export const Component_${i}_${j} = (): string => {
    return "lib-$i-component-$j";
};
TS
    done

    # src/index.ts — re-exports all components
    {
        for j in $(seq 0 $(( K - 1 ))); do
            echo "export { Component_${i}_${j} } from \"./Component-${j}\";"
        done
    } > "$lib/src/index.ts"
done

# ── apps ──────────────────────────────────────────────────────────────────────
for i in $(seq 0 $(( M - 1 ))); do
    app="$GEN/apps/app-$i"
    mkdir -p "$app/src"

    # Dependency list for package.json
    local_deps=""
    for j in $(seq 0 $(( M - 1 ))); do
        local_deps+="    \"@bench/lib-$j\": \"workspace:*\""
        [[ $j -lt $(( M - 1 )) ]] && local_deps+=","
        local_deps+=$'\n'
    done

    cat > "$app/package.json" <<JSON
{
  "name": "@bench/app-$i",
  "version": "0.0.0",
  "private": true,
  "scripts": {
    "build": "tsc -b"
  },
  "dependencies": {
$local_deps  }
}
JSON

    # tsconfig.json — references each lib
    {
        echo '{'
        echo '  "extends": "../../tsconfig.base.json",'
        echo '  "compilerOptions": {'
        echo '    "outDir": "dist",'
        echo '    "rootDir": "src",'
        echo '    "composite": true'
        echo '  },'
        echo '  "references": ['
        for j in $(seq 0 $(( M - 1 ))); do
            comma=","
            [[ $j -eq $(( M - 1 )) ]] && comma=""
            echo "    { \"path\": \"../../libs/lib-$j\" }$comma"
        done
        echo '  ],'
        echo '  "include": ["src/**/*"]'
        echo '}'
    } > "$app/tsconfig.json"

    # moon.yml
    {
        echo "language: typescript"
        echo "type: application"
        echo "dependsOn:"
        for j in $(seq 0 $(( M - 1 ))); do
            echo "  - lib-$j"
        done
        echo "tasks:"
        echo "  build:"
        echo "    command: \"pnpm tsc -b\""
        echo "    outputs:"
        echo "      - \"dist\""
        echo "    deps:"
        echo "      - \"^:build\""
    } > "$app/moon.yml"

    # BUILD.bazel
    {
        echo 'load("@aspect_rules_ts//ts:defs.bzl", "ts_project")'
        echo ""
        echo "ts_project("
        echo "    name = \"app_$i\","
        echo "    srcs = glob([\"src/**/*.ts\"]),"
        echo "    declaration = True,"
        printf "    deps = [\n"
        for j in $(seq 0 $(( M - 1 ))); do
            echo "        \"//libs/lib-$j:lib_$j\","
        done
        echo "    ],"
        echo "    visibility = [\"//visibility:public\"],"
        echo ")"
    } > "$app/BUILD.bazel"

    # src/index.ts — imports one symbol from each lib
    {
        for j in $(seq 0 $(( M - 1 ))); do
            echo "import { Component_${j}_0 as L${j} } from \"@bench/lib-$j\";"
        done
        echo ""
        printf 'console.log('
        for j in $(seq 0 $(( M - 1 ))); do
            printf 'L%d()' "$j"
            [[ $j -lt $(( M - 1 )) ]] && printf ', '
        done
        printf ');\n'
    } > "$app/src/index.ts"
done

# ── magusfile.tl ──────────────────────────────────────────────────────────────
{
    echo "-- magusfile.tl (generated by gen.sh $N)"
    for i in $(seq 0 $(( M - 1 ))); do
        echo "magus.register(\"libs/lib-$i\", { spell = { name = \"ts\" } })"
    done
    for i in $(seq 0 $(( M - 1 ))); do
        printf 'magus.register("apps/app-%d", { spell = { name = "ts" }, depends_on = {' "$i"
        for j in $(seq 0 $(( M - 1 ))); do
            printf '"libs/lib-%d"' "$j"
            [[ $j -lt $(( M - 1 )) ]] && printf ', '
        done
        echo '} })'
    done
} > "$GEN/magusfile.tl"

# ── bench marker files ────────────────────────────────────────────────────────
echo "apps/app-0/src/index.ts" > "$SCRIPT_DIR/.bench-leaf-file"
echo "libs/lib-0/src/Component-0.ts" > "$SCRIPT_DIR/.bench-upstream-file"

echo "generated $N TS packages ($M libs + $M apps, $K components/lib) → $GEN" >&2
