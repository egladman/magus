---
title: Target output references
description: Every target that runs gets a short reference id (out1a2b3c) for its captured output. Retrieve any target's exact output later with magus query, pipe it anywhere, or open it in the browser log viewer - no copy-pasting a wall of text.
tags: [output, ref, logs, query, failure, debugging, clipboard, mcp, agent]
aliases: [concepts/output-refs]
---

# Target output references

A pretty `magus run` interleaves magus's own status lines with each target's real
stdout and stderr. Two things are then hard: telling magus chrome from a target's
output, and pulling out ONE target's full output - especially a failure - to share
with a teammate, an agent, or another tool.

Output references fix this. Every target that runs is given a short **reference id**
for its captured output, printed on its own line:

```text
[pass] docs test (1.2s)
out1a2b3c
```

Retrieve that exact output at any time with `magus query output`:

```sh
magus query output out1a2b3c
```

It writes the raw bytes to stdout and nothing else, so it pipes cleanly anywhere.

## The ref line is always there

The ref is a first-class attribute on every target's result event, so it appears in
every output format (`pretty`, `text`, `json`) and can never be omitted. In pretty
mode it sits on its own bare, unlabeled line beneath the pass/fail line, so a
triple-click selects exactly the ref.

A failing target adds two hints, the exact commands ready to copy:

```text
[fail] docs test (1.2s): tsc exit 2
outcc49db1f
  full output: magus query output outcc49db1f
  open in browser: magus query output outcc49db1f --open
```

## Retrieval: `magus query output <ref>`

`magus query` doubles as the retrieval verb through an explicit `output` subcommand.
`magus query output out1a2b3c` prints that execution's captured output instead of
searching the [knowledge graph](../knowledge.md). It is a subcommand, not a shape-routed
positional, so a free-text search term can never collide with a ref id - `magus query
refactor` always searches the graph.

- `magus query output out1a2b3c` - print the exact output to stdout.
- `magus query output out1a2b3c -o json` - the descriptor (ref, project, target,
  status, duration) plus the output as one record; `-o yaml` too.
- `magus query output out1a2b3c --open` - open the output in the browser [log viewer](#the-log-viewer).
- `magus query output out1a2b3c --attempts` - list the ref's stored executions.
- `magus query output out1a2b3c --meta` - the run's identity: descriptor, lineage,
  cache key, and per-class key digests.

Refs prefix-match like a git short hash: type as few characters as are unique, and
an ambiguous prefix lists the candidates.

## Refs are portable: same inputs, same ref

The ref is a truncation of the step's [cache key](../cache.md), which is computed
from workspace-relative paths, content hashes, and sorted components - nothing
machine-local. So the SAME inputs produce the SAME ref on every machine: an inspect
line pasted from CI or a teammate's terminal resolves in your checkout, provided
your cache holds a run of that step.

The corollary is the debugging story: ref equality is input equality. If CI prints
`outa1b2c3d4e5f6` and your laptop prints a different ref for the same target, your
inputs differ - a source file, a tool version, an env var, or a charm disagrees.

### Attempts: one ref, every execution

The ref names the step, not one execution. Retention keeps the last few executions
per step - a volatile target's recent failures each stay addressable:

```sh
magus query output outa1b2c3d4e5f6 --attempts
```

lists them newest first (attempt id, pass/fail, duration, time, invocation id). The
bare ref always answers with the newest execution; pass a full attempt id to
`magus query output` to retrieve an older one's exact bytes.

### Explaining a ref difference

Because the ref is the key, "different ref" means "different inputs" - and magus can
name the input. Each run stores the deterministic label:value lines its key was
hashed from (secret-redacted), so:

```sh
magus query output out4ef30de6abcd --meta
```

shows one digest per component class (`src`, `env`, `tool`, `charm`, `dep`, ...) -
compact enough to compare across machines to learn WHICH class disagrees - and

```sh
magus describe target build . --cache --against out4ef30de6abcd
```

computes the key a run would mint RIGHT NOW (without running anything) and diffs it
against the stored lines behind the ref, printing the exact line that drifted: the
edited source file with old and new content hash, the changed env var, the bumped
tool version. `--cache` alone shows the live key, the ref a run would print, and the
class digests. This is the works-on-my-machine debugging story: paste CI's ref into
`--against` and read off what your machine disagrees about.

The verdict is key equality, not the line list, and a mismatch exits non-zero so a
script can gate on it. CI runs with `--no-default-charms`, so pass that flag too when
comparing against a CI ref - otherwise your local `default_charms` show up as the
difference.

Env values never reach the store: a key line's value is replaced by a short digest
(`env:TOKEN=sha256:...`) before it is written or printed, on both sides of the diff.
The digest still changes when the value does, so a drifted variable is named without
its contents being shown.

For the LATEST log of a project or target (rather than a specific past execution),
[`magus tail`](../../guides/debugging.md) is a convenience, with `-f` to follow a running build.

## Sharing a ref across machines

A ref is portable arithmetic, but the OUTPUT behind it still has to exist where you
look. Two paths put it there:

- **Passing runs travel automatically.** When a [remote cache](remote.md) is
  configured, a pushed artifact now carries the run's descriptor, its key lines, and
  its build log alongside the manifest and blobs. A teammate who gets a remote cache
  hit resolves the ref the producer printed, rather than minting a local one for the
  same inputs.
- **Failing runs are never shared unless you say so.** A failure is not cached and
  not pushed, which is exactly backwards from what humans want, so publishing is an
  explicit act:

  ```sh
  magus query output out4ef30de6abcd --publish
  ```

  That uploads a signed **output bundle**: the descriptor, the key lines, and the
  captured bytes. A bundle carries no manifest and no artifact blobs, so it can never
  be replayed as a cache hit - a published failure cannot become someone's cached
  success. Publishing needs a signing key, and reading one needs the matching trust
  set, the same asymmetry the remote cache already uses.

`magus query output <ref>` consults the local store first and the published bundles
only if the ref is unknown locally. When nothing has it, the error names the stores it
checked, so a never-published ref is distinguishable from a typo.

Everything the signature covers grew with this: it now authenticates the build log
and the sidecars, not just the manifest. An artifact whose log was altered in transit
is rejected outright instead of having that log written to your cache, and imported
extras are staged until the signature clears, so a rejected artifact leaves nothing
behind. A signature is also bound to the KIND of object it was made over and to the
`(project, cache key)` it is served for, so a published output can never be re-served
as a cache entry, and an entry can never file itself under a different key. Artifacts
from an older magus still verify; magus simply ignores the extras their signature did
not cover.

## Tips and tricks

Copy-paste-ready one-liners:

```sh
# To the clipboard (macOS)
magus query output out1a2b3c | pbcopy
# Linux
magus query output out1a2b3c | wl-copy            # Wayland
magus query output out1a2b3c | xclip -selection clipboard

# Just the failing lines
magus query output out1a2b3c | grep -iE "error|fail"

# Straight into Claude Code (reads piped stdin in print mode)
magus query output out1a2b3c | claude -p "why did this fail and how do I fix it?"

# Into a PR or issue comment
magus query output out1a2b3c | gh pr comment 42 --body-file -

# The descriptor and output together as one JSON record
magus query output out1a2b3c -o json
```

## The log viewer

`magus query output out1a2b3c --open` opens the [log viewer](https://eli.gladman.cc/magus/console/) -
a standalone browser page that renders the captured output with collapsible sections,
status badges, in-page search, ANSI color, and copy. A "Copy command" button hands back
a `magus query output` one-liner (per section too), so you can pass an exact slice to an agent,
and a pretty/raw toggle shows the exact captured bytes. It is the log analog of
[`magus graph open`](../knowledge.md): the ref and the output both ride the link fragment
(`#ref=...&data=...`, gzipped then base64url-encoded), decoded in your browser. The
fragment is never sent to any server, so nothing about the run - not even its ref - ever
leaves your machine.

For a very large log, print it instead (`magus query output out1a2b3c`) and pipe it - a URL
fragment is bounded by the browser's address-bar length.

`--open` follows the `BROWSER` environment variable (the freedesktop convention) to
choose which browser to launch, so you can override your desktop default per command:

```sh
BROWSER=firefox magus query output out1a2b3c --open
```

`BROWSER` may be a colon-separated list of commands, each optionally containing `%s`
where the URL is substituted (otherwise the URL is appended). With `BROWSER` unset,
magus uses your desktop's default handler (`open`, `xdg-open`, or the Windows
equivalent).

## For agents and MCP

The [MCP](../../guides/mcp.md) `magus_output` tool is the agent analog of `magus query output`:
pass a `ref` (`out1a2b3c`, or a unique prefix) and it returns that execution's exact
bytes plus its descriptor. An agent that saw a ref in a run fetches the full output
directly, instead of re-reading a wall of text or asking you to paste it. It is a
dedicated tool, not a mode of `magus_query`, so a free-text graph query never
collides with a ref id.

## How refs are stored

- The ref is the step's cache key, truncated: same inputs, same key, same ref,
  on any machine. Each execution additionally gets a nonce-derived **attempt id**,
  so repeated runs of one step never overwrite each other.
- Output is persisted verbatim as a per-attempt blob under the cache directory
  (`outputs/<key>/`), alongside a small descriptor sidecar (ref, project, target,
  status, timestamp, duration, key, attempt, magus version), on success and on
  failure. Retrieval is a straight byte read, so `magus query output` returns
  exactly the bytes the target wrote.
- Retention keeps the last few executions per cache key, so a nondeterministic
  target's recent failures stay independently addressable, and is garbage-collected
  along with the rest of the [cache](../cache.md). Refs are run artifacts, not
  [knowledge-graph](../knowledge.md) nodes; the graph schema is untouched.
- Stores written before portable refs keep resolving: the old per-execution ids
  still address their bytes, and the same step resolves under its new portable ref.

## Diagnostics

When a ref cannot be resolved, `magus query` reports a coded
[diagnostic](../../reference/codes/outputref/README.md) so the error points at the fix:

- [MGS8001](../../reference/codes/outputref/MGS8001.md): the ref is well-formed but no stored output
  exists - it aged out of the cache, or the ref is mistyped.
- [MGS8002](../../reference/codes/outputref/MGS8002.md): a shortened ref prefix matches more than one
  stored output, so the lookup is ambiguous.
- [MGS8003](../../reference/codes/outputref/MGS8003.md): `magus query output` was given an argument
  that is not a well-formed `ref<hex>` id, so it cannot name a stored output.

## The artifact twin: history and diff

An output ref reaches a past run's captured **log**. The same store also still holds
that run's **artifacts**, because the cache is content-addressed: blobs under `cas/`
keyed by their sha256, referenced by one manifest per cache entry. Every earlier
version of a declared output is therefore still on disk and addressable, until
eviction reclaims it.

Two chain verbs expose that:

```sh
magus run build --then file dist/app history   # every cached version of that artifact
magus run build --then file dist/app diff      # compare it against the last cached version
```

`history` answers a question the VCS answers badly for generated files. Git tells you
when someone committed a regeneration; this tells you when the **bytes** changed:

```text
2026-07-30T00:35:44Z  81db67b6a570      412  build
2026-07-30T00:12:09Z  2d27fbdf4e8c      412  build
```

Runs that produced identical bytes collapse to one row, and the row kept is the
earliest of the run, because the useful question is when content first appeared
rather than when it was last re-confirmed. Add `-o json` (before `--then`) for the
blob, size, target and cache entry, so a script can compare them itself.

### diff delegates; it does not render

`diff` materializes the cached side into a temporary file and runs **your** difftool,
resolved in order from `$MAGUS_DIFFTOOL`, then `$DIFFTOOL`, then `git diff
--no-index` (git is already a hard dependency of a workspace, so it is the one
differ guaranteed to be present). The value is split on spaces, so a tool with flags
works without a shell:

```sh
MAGUS_DIFFTOOL="delta --side-by-side" magus run build --then file dist/app diff
MAGUS_DIFFTOOL="difft"                magus run build --then file dist/app diff
```

magus emits and does not render, exactly as it refuses to draw the knowledge graph.
A built-in differ would be a worse version of the tool you already chose.

Materializing clones from the store rather than copying wherever the filesystem
supports it (APFS, btrfs, XFS), so comparing a large artifact costs almost nothing.

### Two answers that are not failures

- **"matches every cached version; nothing to diff."** The working tree is the only
  content the store has for that path. Running a differ over two identical files and
  leaving you to infer that from empty output would be worse.
- **"artifact blob evicted."** The store evicts least-recently-used blobs, so a
  version can be listed from a surviving manifest and still have no bytes behind it.
  This is reported rather than shown as an empty diff, because an empty diff reads as
  "unchanged" - the most misleading answer available. Re-run the target with
  `--no-cache` to regenerate it.
