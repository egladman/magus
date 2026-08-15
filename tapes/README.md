# Terminal recordings

The animated SVGs in the README and the docs are rendered from the `.capture`
files here by `cmd/magus-termcast`. A capture is the raw byte stream a real magus
wrote to a real pseudo-terminal; the SVG under `assets/gen/` is generated output.

```sh
magus run termcast-record .     # re-record tapes/core-loop.capture
magus run termcast-showcase .   # re-record tapes/showcase.capture (interactive)
magus run termcast-generate .   # render both captures into assets/gen/*.svg
```

Recording is opt-in; rendering is not. A session is not byte-reproducible, so
the two record targets carry `skip_cache` and are deliberately not named
`*-generate` - `generate` globs that suffix and drift-gates what it finds, and a
gate over a live recording would be red every run. Rendering a committed capture
IS reproducible, so `termcast-generate` is gated: a recording nothing checks goes
stale in silence.

## Why a committed capture rather than a screen grab

A capture is a checked-in artifact with a script behind it, so a recording can be
reproduced by anyone at any commit without a human retyping it. A screen recorder
produces a one-off binary with no source: when the output drifts, nobody can
regenerate it and the recording quietly starts lying.

## The two recordings

`core-loop.session.sh` drives the README's transcript: what magus PRINTS. It runs
non-interactively, one frame per command.

`cmd/magus-termcast/showcase.go` drives the interactive one: what magus DRAWS -
the pinned run band, the failure tree beside its captured output, the picker
searching the knowledge graph. None of that appears in a piped log, and it is
recorded by real keystrokes with the frames marked as they are taken.

Both stage their workspace with `demo-init.sh`, which builds a throwaway
multi-project Go workspace in a temp dir and puts this checkout's `magus` first
on `PATH`. Two things that buys, both load-bearing:

- **The workspace is generated, not checked in.** magus discovers a project from
  any `magusfile.buzz` below the workspace root and has no ignore mechanism, so a
  demo magusfile committed here would be picked up as a real project of this repo
  (the same breakage a stale `.claude/worktrees` copy causes, MGS1002).
- **The cache is genuinely cold.** magus keeps its cache in `.magus/` under the
  workspace root, so a fresh temp dir means the first run really does the work and
  the second really replays it. No recording clears the developer's own cache.

## Keeping them useful

These earn their place by showing something a code block cannot: output arriving
in stages, a cache hit landing, a surface being driven. A recording that only
shows text should be a fenced code block instead - cheaper, copyable, greppable,
and readable to someone on a screen reader.

Give every embed real alt text describing what happens, not "terminal demo".
