#!/usr/bin/env bash
# setup.sh — materialize the large-monorepo benchmark workspace.
#
# Clones vsavkin/large-monorepo at the pinned SHA into ./gen/repo, lays down the
# magus config (the committed spells/*.buzz plus a magus.yaml root marker and one
# generated magusfile.buzz per project), and installs node deps. Idempotent:
# re-running checks out the pinned SHA again and refreshes the magus config. To
# start completely clean, `rm -rf gen/` first.
#
# Everything lives under gen/, which magus's discovery walk ignores (like the
# fixtures' gen/ dirs), so the cloned repo's generated magusfiles are never
# picked up by the surrounding magus workspace, and turbo/nx/lage build the clean
# checkout as-is (their configs already live at the repo root). magus support is
# purely additive (per-project magusfiles + the two workspace-local spells);
# nothing upstream is patched. When upstream moves, bump upstream_sha in
# versions.lock.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
GEN="$DIR/gen"
REPO="$GEN/repo"
UPSTREAM="https://github.com/vsavkin/large-monorepo.git"

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
git -C "$REPO" checkout -q "$SHA"

echo "==> writing magus config"
mkdir -p "$REPO/spells"
cp "$DIR/spells/nextjs.buzz" "$REPO/spells/nextjs.buzz"
cp "$DIR/spells/tslib.buzz"  "$REPO/spells/tslib.buzz"

# Workspace root marker (the repo root, not itself a project).
cat > "$REPO/magus.yaml" <<'YAML'
telemetry:
  enabled: false
YAML

# Generate one magusfile.buzz per project so magus traverses the same app ->
# feature-lib graph turbo/nx/lage derive from package.json. Apps and feature libs
# are discovered from the checkout, so this tracks upstream when the SHA moves.

# Leaf magusfile for a non-building package (feature libs + shared packages).
write_leaf_magusfile() {
    cat > "$1/magusfile.buzz" <<'LEAFMF'
import "magus";
import "spells/tslib" as tslib;
magus.project.register(fun(p, cb) > bool { cb({"spells": [tslib]}); return true; });
export fun build(args: [str]) > void { tslib["noop"](); }
LEAFMF
}

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
            write_leaf_magusfile "$libdir"
            libs+=("packages/$app/$(basename "$libdir")")
        done
    fi

    # App magusfile: edge declared twice (depends_on for affected, magus.needs for
    # ordering/caching).
    {
        echo 'import "magus";'
        echo 'import "spells/nextjs" as nextjs;'
        idx=0
        for lib in "${libs[@]}"; do
            echo "import \"project/$lib\" as f$idx;"
            idx=$(( idx + 1 ))
        done
        echo ''
        printf 'magus.project.register(fun(p, cb) > bool { cb({"spells": [nextjs], "depends_on": ['
        for k in "${!libs[@]}"; do
            printf '"%s"' "${libs[$k]}"
            [[ $k -lt $(( ${#libs[@]} - 1 )) ]] && printf ', '
        done
        echo ']}); return true; });'
        echo ''
        printf 'export fun build(args: [str]) > void { magus.needs('
        for k in "${!libs[@]}"; do
            printf 'f%d.build' "$k"
            [[ $k -lt $(( ${#libs[@]} - 1 )) ]] && printf ', '
        done
        echo '); nextjs["next-build"](); }'
    } > "$appdir/magusfile.buzz"
done

echo "==> npm install"
( cd "$REPO" && npm install )

echo "==> setup complete: $REPO"
echo "    next: ./bench.sh        (see README.md for tool/scenario selection)"
