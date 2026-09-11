#!/usr/bin/env bash
# setup.sh - materialize the large-monorepo benchmark workspace.
#
# Clones vsavkin/large-monorepo at the pinned SHA into ./gen/repo, lays down the
# magus config (the committed spells/*.buzz plus a magus.yaml root marker and one
# generated magusfile.buzz per project), overlays the enrichment from enrich/, and
# installs node deps. Idempotent: re-running checks out the pinned SHA again and
# refreshes everything. To start completely clean, `rm -rf gen/` first.
#
# Everything lives under gen/, which magus's discovery walk ignores (like the
# fixtures' gen/ dirs), so the cloned repo's generated magusfiles are never
# picked up by the surrounding magus workspace, and turbo/nx/lage build the clean
# checkout as-is (their configs already live at the repo root). Nothing upstream is
# patched: the overlay only ADDS files (packages/platform/*, one bridge module per
# listed feature library, tools/, and the per-project magusfiles). When upstream
# moves, bump upstream_sha in versions.lock.
#
# The overlay is committed on the branch named by ENRICHED_BRANCH, and tasks.sh
# then cuts one task/<id> branch off it per agent-benchmark task. The perf
# benchmark (bench.sh) is unaffected: it reads the working tree, not the branch.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
GEN="$DIR/gen"
REPO="$GEN/repo"
UPSTREAM="https://github.com/vsavkin/large-monorepo.git"
ENRICHED_BRANCH="bench/enriched"

SHA="$(grep '^upstream_sha=' "$DIR/versions.lock" | cut -d= -f2)"
[[ -n "$SHA" ]] || { echo "setup: upstream_sha missing from versions.lock" >&2; exit 1; }

mkdir -p "$GEN"

if [[ ! -d "$REPO/.git" ]]; then
    echo "==> cloning $UPSTREAM"
    git clone "$UPSTREAM" "$REPO"
fi

echo "==> checking out pinned SHA $SHA"
if ! git -C "$REPO" cat-file -e "$SHA^{commit}" 2>/dev/null; then
    git -C "$REPO" fetch origin
fi
git -C "$REPO" checkout -q --detach "$SHA"
git -C "$REPO" branch -f "$ENRICHED_BRANCH" "$SHA"
git -C "$REPO" checkout -q "$ENRICHED_BRANCH"
git -C "$REPO" reset -q --hard "$SHA"
git -C "$REPO" clean -qfd -e node_modules

echo "==> writing magus config"
mkdir -p "$REPO/spells"
cp "$DIR/spells/nextjs.buzz" "$REPO/spells/nextjs.buzz"
cp "$DIR/spells/tslib.buzz"  "$REPO/spells/tslib.buzz"
cp "$DIR/spells/jsmod.buzz"  "$REPO/spells/jsmod.buzz"

# Workspace root marker (the repo root, not itself a project). default_charms mirrors
# the magus repo's own setting so `magus run generate` writes its declared outputs;
# the drift gate is the separate read-only `verify` target, so the charm does not
# defeat it.
cat > "$REPO/magus.yaml" <<'YAML'
telemetry:
  enabled: false
default_charms: [rw]
YAML

echo "==> overlaying enrichment"
mkdir -p "$REPO/tools"
cp "$DIR/enrich/tools/gen-api.mjs" "$REPO/tools/gen-api.mjs"
mkdir -p "$REPO/packages/platform"
for pkg in logging config http metrics; do
    rm -rf "${REPO:?}/packages/platform/$pkg"
    cp -R "$DIR/enrich/platform/$pkg" "$REPO/packages/platform/$pkg"
done

# One bridge module per listed feature library. Each imports exactly one platform
# package, so the cross-project edges are non-uniform and worth tracing.
write_bridge() {
    local libdir="$1" lib="$2" pkg="$3"
    local out="$REPO/$libdir/src/platform-bridge.mjs"
    case "$pkg" in
    http)
        cat > "$out" <<BRIDGE
import { request } from '../../../platform/http/index.mjs';

// Describes the feature's read request; sending it is the caller's job.
export function featureRequest(path) {
  return request(\`https://api.example.test\${path}\`, { query: { feature: '$lib' } });
}
BRIDGE
        ;;
    metrics)
        cat > "$out" <<BRIDGE
import { Counter, snapshot } from '../../../platform/metrics/index.mjs';

const renders = new Counter('$lib.renders');

export function recordRender() {
  return renders.add();
}

export function featureMetrics() {
  return snapshot([renders]);
}
BRIDGE
        ;;
    config)
        cat > "$out" <<BRIDGE
import { loadConfig } from '../../../platform/config/index.mjs';

const DEFAULTS = { endpoint: 'https://api.example.test', retries: 1 };

export function featureConfig(overrides) {
  return loadConfig(DEFAULTS, overrides, ['endpoint']);
}
BRIDGE
        ;;
    logging)
        cat > "$out" <<BRIDGE
import { createLogger } from '../../../platform/logging/index.mjs';

export function featureLogger() {
  return createLogger('$lib');
}
BRIDGE
        ;;
    *) echo "setup: unknown platform package $pkg" >&2; exit 1 ;;
    esac
}

bridge_libs=()
bridge_pkgs=()
while IFS=$'\t' read -r libdir pkg; do
    if [[ -z "$libdir" || "$libdir" == \#* ]]; then continue; fi
    [[ -d "$REPO/$libdir" ]] || { echo "setup: bridge target $libdir missing" >&2; exit 1; }
    write_bridge "$libdir" "$(basename "$libdir")" "$pkg"
    bridge_libs+=("$libdir")
    bridge_pkgs+=("$pkg")
done < "$DIR/enrich/bridges.tsv"

# Empty output, not a non-zero status, for a library with no bridge: the caller reads
# it in a command substitution under `set -e`.
bridge_pkg_of() {
    local want="$1" i
    for i in "${!bridge_libs[@]}"; do
        if [[ "${bridge_libs[$i]}" == "$want" ]]; then
            echo "${bridge_pkgs[$i]}"
            return 0
        fi
    done
    return 0
}

# Generate one magusfile.buzz per project so magus traverses the same app ->
# feature-lib graph turbo/nx/lage derive from package.json, plus the platform edges
# the bridge modules introduce. Apps and feature libs are discovered from the
# checkout, so this tracks upstream when the SHA moves.

# The api targets live in the magusfile rather than in a spell op because the
# generator is one repo-root script every project shells out to; ../../../ is the
# depth of every packages/<group>/<name> project.
api_targets() {
    cat <<'API'

export fun generate(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles("gen/api.md");
    proc\exec("node", ["../../../tools/gen-api.mjs", "--write", "."]);
}

export fun verify(ctx: magus\Context, args: [str]) > void !> any {
    proc\exec("node", ["../../../tools/gen-api.mjs", "--check", "."]);
}
API
}

# Leaf magusfile for a non-building package (feature libs + shared packages).
write_leaf_magusfile() {
    cat > "$1/magusfile.buzz" <<'LEAFMF'
import "magus";
import "spells/tslib" as tslib;
magus\project({"spells": [tslib]});
export fun build(ctx: magus\Context, args: [str]) > void { tslib["noop"](ctx); }
export fun ci(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(build); }
LEAFMF
}

# A feature library carrying a bridge module: same tslib build, plus the jsmod spell
# and the api targets, plus the project edge onto the platform package it imports.
write_bridge_magusfile() {
    local libdir="$1" pkg="$2"
    {
        echo 'import "magus";'
        echo 'import "proc";'
        echo 'import "spells/tslib" as tslib;'
        echo 'import "spells/jsmod" as jsmod;'
        echo "import \"project/../../platform/$pkg\" as platform;"
        echo ''
        echo "magus\\project({\"spells\": [tslib, jsmod], \"depends_on\": [\"packages/platform/$pkg\"]});"
        echo ''
        printf '%s\n' 'export fun build(ctx: magus\Context, args: [str]) > void { tslib["noop"](ctx); }'
        api_targets
        echo ''
        printf '%s\n' 'export fun ci(ctx: magus\Context, args: [str]) > void !> any {'
        echo '    ctx.needs(platform.ci);'
        echo '    ctx.needs(build, verify);'
        echo '}'
    } > "$REPO/$libdir/magusfile.buzz"
}

# Platform packages: plain ESM, real tests, and the api targets. deps is the
# space-separated list of sibling platform packages this one imports.
write_platform_magusfile() {
    local pkg="$1" deps="$2" dep idx=0
    {
        echo 'import "magus";'
        echo 'import "proc";'
        echo 'import "spells/jsmod" as jsmod;'
        for dep in $deps; do
            echo "import \"project/../$dep\" as d$idx;"
            idx=$(( idx + 1 ))
        done
        echo ''
        printf 'magus\\project({"spells": [jsmod], "depends_on": ['
        idx=0
        for dep in $deps; do
            [[ $idx -gt 0 ]] && printf ', '
            printf '"packages/platform/%s"' "$dep"
            idx=$(( idx + 1 ))
        done
        echo ']});'
        api_targets
        echo ''
        printf '%s\n' 'export fun tests(ctx: magus\Context, args: [str]) > void !> any {'
        printf '%s\n' '    proc\exec("node", ["--test"]);'
        echo '}'
        echo ''
        printf '%s\n' 'export fun ci(ctx: magus\Context, args: [str]) > void !> any {'
        idx=0
        for dep in $deps; do
            echo "    ctx.needs(d$idx.ci);"
            idx=$(( idx + 1 ))
        done
        echo '    ctx.needs(verify, tests);'
        echo '}'
    } > "$REPO/packages/platform/$pkg/magusfile.buzz"
}

write_platform_magusfile logging ""
write_platform_magusfile config "logging"
write_platform_magusfile http "logging config"
write_platform_magusfile metrics "logging"

# Shared packages: leaf nodes, no downstream (no package.json lists them).
if [[ -d "$REPO/packages/shared" ]]; then
    for shared in "$REPO"/packages/shared/*/; do
        [[ -d "$shared" ]] && write_leaf_magusfile "$shared"
    done
fi

for appdir in "$REPO"/apps/*/; do
    app="$(basename "$appdir")"
    # Feature libs this app consumes (packages/<app>/important-feature-*).
    libs=()
    if [[ -d "$REPO/packages/$app" ]]; then
        for libdir in "$REPO"/packages/"$app"/important-feature-*/; do
            [[ -d "$libdir" ]] || continue
            rel="packages/$app/$(basename "$libdir")"
            pkg="$(bridge_pkg_of "$rel")"
            if [[ -n "$pkg" ]]; then
                write_bridge_magusfile "$rel" "$pkg"
            else
                write_leaf_magusfile "$libdir"
            fi
            libs+=("$rel")
        done
    fi

    # App magusfile: edge declared twice (depends_on for affected, ctx.needs for
    # ordering/caching).
    {
        echo 'import "magus";'
        echo 'import "spells/nextjs" as nextjs;'
        idx=0
        for lib in "${libs[@]}"; do
            # Dot-relative to this magusfile's dir (apps/<app>), not repo-relative.
            echo "import \"project/../../$lib\" as f$idx;"
            idx=$(( idx + 1 ))
        done
        echo ''
        printf 'magus\\project({"spells": [nextjs], "depends_on": ['
        for k in "${!libs[@]}"; do
            printf '"%s"' "${libs[$k]}"
            [[ $k -lt $(( ${#libs[@]} - 1 )) ]] && printf ', '
        done
        echo ']});'
        echo ''
        printf 'export fun build(ctx: magus\\Context, args: [str]) > void { ctx.needs('
        for k in "${!libs[@]}"; do
            printf 'f%d.build' "$k"
            [[ $k -lt $(( ${#libs[@]} - 1 )) ]] && printf ', '
        done
        echo '); nextjs["next-build"](ctx); }'
        echo ''
        printf '%s\n' 'export fun ci(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(build); }'
    } > "$appdir/magusfile.buzz"
done

echo "==> generating api summaries"
node "$REPO/tools/gen-api.mjs" --write

echo "==> committing the overlay on $ENRICHED_BRANCH"
git -C "$REPO" add -A
git -C "$REPO" -c user.name=magus-bench -c user.email=bench@magus.invalid \
    commit -qm "overlay the magus config, the platform packages and the api summaries" --allow-empty

"$DIR/tasks.sh" "$REPO" "$ENRICHED_BRANCH"

echo "==> npm install"
( cd "$REPO" && npm install )

echo "==> setup complete: $REPO"
echo "    next: ./bench.sh        (see README.md for tool/scenario selection)"
echo "          ../agent/tasks/   (the agent-benchmark tasks seeded on task/* branches)"
