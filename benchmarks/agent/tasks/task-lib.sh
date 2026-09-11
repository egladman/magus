#!/usr/bin/env bash
# Helpers shared by the task seed/check/solution scripts. Sourced, never executed.
#
# Everything here is stdlib node plus git: a task worktree is a bare `git worktree
# add` of the fixture clone with no node_modules, and a check must run identically
# under every arm.

# tl_seed <worktree> <task-id>
# Detached so several runs of one task can hold worktrees of the same branch at once.
tl_seed() {
    local worktree="$1" id="$2"
    git -C "$worktree" checkout -qf --detach "task/$id"
    git -C "$worktree" clean -qfd
    rm -f "$worktree/ANSWER.md"
}

# tl_edit <file> <literal-from> <literal-to>
# Fails loudly when the pattern has moved, so an oracle can never silently no-op.
# shellcheck disable=SC2016  # the node program is a literal
tl_edit() {
    node -e '
      const fs = require("fs");
      const [file, from, to] = process.argv.slice(1);
      const source = fs.readFileSync(file, "utf8");
      if (!source.includes(from)) {
        console.error(`edit: pattern not found in ${file}: ${from}`);
        process.exit(1);
      }
      fs.writeFileSync(file, source.split(from).join(to));
    ' "$1" "$2" "$3"
}

# tl_grade_answer <answer-file> <section-heading> <expected...>
# The bullet list under the named heading must be exactly the expected set: an answer
# that lists everything scores no better than one that lists nothing.
# shellcheck disable=SC2016  # the node program is a literal
tl_grade_answer() {
    local answer="$1" heading="$2"
    shift 2
    node -e '
      const fs = require("fs");
      const [answer, heading, ...expected] = process.argv.slice(1);
      if (!fs.existsSync(answer)) {
        console.error(`grade: ${answer} was never written`);
        process.exit(1);
      }
      const lines = fs.readFileSync(answer, "utf8").split("\n");
      const start = lines.findIndex((l) => l.trim().toLowerCase() === heading.toLowerCase());
      if (start < 0) {
        console.error(`grade: no "${heading}" section in ${answer}`);
        process.exit(1);
      }
      const listed = new Set();
      for (const line of lines.slice(start + 1)) {
        if (line.startsWith("#")) break;
        const m = line.match(/^\s*[-*]\s+`?([^`\s]+)`?\s*$/);
        if (m) listed.add(m[1].replace(/\/$/, ""));
      }
      const want = new Set(expected);
      const missing = [...want].filter((p) => !listed.has(p)).sort();
      const extra = [...listed].filter((p) => !want.has(p)).sort();
      if (missing.length) console.error(`grade: missing ${missing.join(", ")}`);
      if (extra.length) console.error(`grade: not in the answer key ${extra.join(", ")}`);
      process.exit(missing.length || extra.length ? 1 : 0);
    ' "$answer" "$heading" "$@"
}

# tl_platform_tests <worktree>
# The fixture's own tests for the four platform packages.
tl_platform_tests() {
    local worktree="$1" pkg
    for pkg in logging config http metrics; do
        ( cd "$worktree/packages/platform/$pkg" && node --test )
    done
}
