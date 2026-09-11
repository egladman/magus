#!/usr/bin/env bash
# tasks.sh - cut the agent-benchmark task branches off the enriched fixture.
#
# Called by setup.sh with (repo, base branch). Every task/<id> branch is created
# here and nowhere else: the seed state is generated, never hand-applied to gen/, so
# `rm -rf gen/ && ./setup.sh` reproduces the whole corpus. The task prompts, checks
# and oracle solutions live in ../agent/tasks/<id>/.
#
# Interrogation and catch-up tasks need no source change, so their branch is the base
# commit; the agent's deliverable there is ANSWER.md, which check.sh grades.
set -euo pipefail

REPO="${1:?usage: tasks.sh <repo> <base-branch>}"
BASE="${2:?usage: tasks.sh <repo> <base-branch>}"

git_bench() {
    git -C "$REPO" -c user.name=magus-bench -c user.email=bench@magus.invalid "$@"
}

cut_branch() {
    git -C "$REPO" checkout -q -B "task/$1" "$BASE"
}

commit_branch() {
    git -C "$REPO" add -A
    git_bench commit -qm "$1"
}

regenerate() {
    node "$REPO/tools/gen-api.mjs" --write
}

# Literal search-and-replace that fails loudly when the pattern has moved, so a
# fixture edit can never silently produce a branch with no seeded change in it.
# shellcheck disable=SC2016  # the node program and the JS patterns are literals
edit() {
    node -e '
      const fs = require("fs");
      const [file, from, to] = process.argv.slice(1);
      const source = fs.readFileSync(file, "utf8");
      if (!source.includes(from)) {
        console.error(`tasks: pattern not found in ${file}: ${from}`);
        process.exit(1);
      }
      fs.writeFileSync(file, source.split(from).join(to));
    ' "$REPO/$1" "$2" "$3"
}

echo "==> cutting task branches"

# Interrogation: the enriched base, unmodified. The answer key is the shape of the
# platform dependency graph, which bridges.tsv fixes.
cut_branch platform-http-consumers
cut_branch config-change-rebuild-set

# Bugfix: mergeConfig drops any falsy override, so `false`, `0` and `''` never win.
cut_branch merge-config-falsy
edit packages/platform/config/index.mjs \
    'if (value === undefined) continue;' \
    'if (!value) continue;'
commit_branch "tighten the mergeConfig override guard"

# Bugfix: buildUrl concatenates query pairs raw, so a space or an ampersand in a
# value produces a malformed url.
cut_branch url-query-encoding
# shellcheck disable=SC2016  # JS template literals, not shell expansions
edit packages/platform/http/index.mjs \
    'const pairs = keys.map((key) => `${encodeURIComponent(key)}=${encodeURIComponent(String(query[key]))}`);' \
    'const pairs = keys.map((key) => `${key}=${query[key]}`);'
commit_branch "simplify the buildUrl query encoding"

# Cross-cutting change with generated output: both start from the clean base.
cut_branch rename-logger-factory
cut_branch add-metrics-percentile

# Catch-up: a tagged baseline plus four commits whose changed-project set is the
# ground truth.
cut_branch changes-since-baseline
git -C "$REPO" tag -f -m "catch-up baseline" release-2 "$BASE" >/dev/null

edit packages/platform/metrics/index.mjs \
    '  // Zero samples average to 0 rather than NaN, so a snapshot of an idle timer stays' \
    '  // Largest recorded sample, or 0 when nothing has been recorded.
  max() {
    return this.samples.reduce((highest, ms) => (ms > highest ? ms : highest), 0);
  }

  // Zero samples average to 0 rather than NaN, so a snapshot of an idle timer stays'
regenerate
commit_branch "add Timer.max"

edit packages/platform/http/index.mjs \
    "const DEFAULTS = { method: 'GET', timeoutMs: 1000, retries: 0 };" \
    "const DEFAULTS = { method: 'GET', timeoutMs: 2500, retries: 0 };"
edit packages/platform/http/test/http.test.mjs \
    'assert.equal(req.timeoutMs, 1000);' \
    'assert.equal(req.timeoutMs, 2500);'
regenerate
commit_branch "raise the default request timeout to 2500ms"

edit packages/warp-drive-manager/important-feature-2/src/platform-bridge.mjs \
    'export function featureMetrics() {' \
    'export function resetRenders() {
  renders.reset();
}

export function featureMetrics() {'
regenerate
commit_branch "let the warp-drive-manager feature reset its render counter"

edit apps/crew/pages/index.tsx \
    'import ' \
    '// Landing page for the crew app.
import '
commit_branch "note what the crew landing page is"

# Build-command discovery: api summaries left stale or missing, with no instruction
# for how they are produced.
cut_branch regenerate-api-summaries
rm -f "$REPO/packages/platform/metrics/gen/api.md"
rm -f "$REPO/packages/crew/important-feature-3/gen/api.md"
edit packages/platform/logging/gen/api.md '- formatEntry' '- formatEntryLegacy'
edit packages/platform/http/gen/api.md '- buildUrl' '- buildURL'
edit packages/warp-drive-manager/important-feature-16/gen/api.md \
    '- featureLogger' '- makeFeatureLogger'
commit_branch "hand-edit the api summaries"

git -C "$REPO" checkout -q "$BASE"
echo "==> task branches ready"
git -C "$REPO" branch --list 'task/*'
