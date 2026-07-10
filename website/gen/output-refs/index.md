---
title: Target output references
description: Every target that runs gets a short reference id (ref1a2b3c) for its captured output. Retrieve any target's exact output later with magus query, pipe it anywhere, or open it in the browser log viewer - no copy-pasting a wall of text.
tags: [output, ref, logs, query, failure, debugging, clipboard, mcp, agent]
---

# Target output references

A pretty `magus run` interleaves magus's own status lines with each target's real
stdout and stderr. Two things are then hard: telling magus chrome from a target's
output, and pulling out ONE target's full output - especially a failure - to share
with a teammate, an agent, or another tool.

Output references fix this. Every target that runs is given a short **reference id**
for its captured output, printed on its own line:

```
[pass] website test (1.2s)
ref1a2b3c
```

Retrieve that exact output at any time with `magus query`:

```sh
magus query ref1a2b3c
```

It writes the raw bytes to stdout and nothing else, so it pipes cleanly anywhere.

## The ref line is always there

The ref is a first-class attribute on every target's result event, so it appears in
every output format (`pretty`, `text`, `json`) and can never be omitted. In pretty
mode it sits on its own bare, unlabeled line beneath the pass/fail line, so a
triple-click selects exactly the ref.

A failing target adds two hints, the exact commands ready to copy:

```
[fail] website test (1.2s): tsc exit 2
refcc49db1f
  full output: magus query refcc49db1f
  open in browser: magus query refcc49db1f --open
```

## Retrieval: `magus query ref<hex>`

`magus query` is also the retrieval verb. A single argument shaped like a ref -
strictly `ref` followed by lowercase hex digits - prints that execution's captured
output instead of searching the [knowledge graph](knowledge.md). A real free-text
query like `magus query refactor` (a non-hex tail) is unaffected and searches the
graph as before.

- `magus query ref1a2b3c` - print the exact output to stdout.
- `magus query ref1a2b3c --meta` - prepend a header (ref, project, target, status,
  duration) before the output.
- `magus query ref1a2b3c --open` - open the output in the browser [log viewer](#the-log-viewer).

Refs prefix-match like a git short hash: type as few characters as are unique, and
an ambiguous prefix lists the candidates.

For the LATEST log of a project or target (rather than a specific past execution),
[`magus tail`](debugging.md) is a convenience, with `-f` to follow a running build.

## Tips and tricks

Copy-paste-ready one-liners:

```sh
# To the clipboard (macOS)
magus query ref1a2b3c | pbcopy
# Linux
magus query ref1a2b3c | wl-copy            # Wayland
magus query ref1a2b3c | xclip -selection clipboard

# Just the failing lines
magus query ref1a2b3c | grep -iE "error|fail"

# Straight into Claude Code (reads piped stdin in print mode)
magus query ref1a2b3c | claude -p "why did this fail and how do I fix it?"

# Into a PR or issue comment
magus query ref1a2b3c | gh pr comment 42 --body-file -

# With a context header
magus query ref1a2b3c --meta
```

## The log viewer

`magus query ref1a2b3c --open` opens the [log viewer](https://eli.gladman.cc/magus/logs/) -
a standalone browser page that renders the captured output with collapsible sections,
status badges, in-page search, ANSI color, and copy. A "Copy command" button hands back
a `magus query` one-liner (per section too), so you can pass an exact slice to an agent,
and a pretty/raw toggle shows the exact captured bytes. It is the log analog of
[`magus graph open`](knowledge.md): the ref and the output both ride the link fragment
(`#ref=...&data=...`, gzipped then base64url-encoded), decoded in your browser. The
fragment is never sent to any server, so nothing about the run - not even its ref - ever
leaves your machine.

For a very large log, print it instead (`magus query ref1a2b3c`) and pipe it - a URL
fragment is bounded by the browser's address-bar length.

`--open` follows the `BROWSER` environment variable (the freedesktop convention) to
choose which browser to launch, so you can override your desktop default per command:

```sh
BROWSER=firefox magus query ref1a2b3c --open
```

`BROWSER` may be a colon-separated list of commands, each optionally containing `%s`
where the URL is substituted (otherwise the URL is appended). With `BROWSER` unset,
magus uses your desktop's default handler (`open`, `xdg-open`, or the Windows
equivalent).

## For agents and MCP

The [MCP](mcp.md) `magus_query` tool takes the same ref: pass `ref1a2b3c` as the
query and it returns that execution's exact bytes and metadata. An agent that saw a
ref in a run fetches the full output directly, instead of re-reading a wall of text
or asking you to paste it.

## How refs are stored

- The ref is derived from the step's cache key plus a per-execution nonce, so it is
  cache-key-flavored but unique to each run.
- Output is persisted to a store under the cache directory (`outputs/`), alongside a
  small metadata sidecar (project, target, status, timestamp, duration, cache key),
  on success and on failure.
- Retention keeps the last few executions per cache key, so a nondeterministic
  target's recent failures stay independently addressable, and is garbage-collected
  along with the rest of the [cache](cache.md). Refs are run artifacts, not
  [knowledge-graph](knowledge.md) nodes; the graph schema is untouched.

## Diagnostics

When a ref cannot be resolved, `magus query` reports a coded
[diagnostic](codes/outputref/README.md) so the error points at the fix:

- [MGS8001](codes/outputref/MGS8001.md): the ref is well-formed but no stored output
  exists - it aged out of the cache, or the ref is mistyped.
- [MGS8002](codes/outputref/MGS8002.md): a shortened ref prefix matches more than one
  stored output, so the lookup is ambiguous.
- [MGS8003](codes/outputref/MGS8003.md): a ref-only flag (`--open`, `--meta`,
  `--print`) was given without a valid `ref<hex>` argument.
