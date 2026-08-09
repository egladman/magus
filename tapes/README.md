# Terminal recordings

The GIFs in the README and the docs are recorded from the `.tape` files here by
[VHS](https://github.com/charmbracelet/vhs). A tape is the source; the GIF under
`assets/gen/` is generated output.

```sh
brew install vhs        # pulls in ttyd and ffmpeg
magus run tapes         # records every tapes/*.tape into assets/gen/
```

`tapes` is opt-in. It is not part of `generate` or `ci`, and it is not named
`*-generate`, because `generate` globs that suffix and drift-gates what it
finds - and a screen recording is not byte-reproducible, so it would fail every
CI run by construction. Re-record when the CLI's output actually changes, then
commit the GIF alongside the change that moved it.

## Why scripted rather than screen-captured

A tape is a checked-in script, so a recording can be reproduced by anyone, at
any commit, without a human retyping it. Screen recorders (t-rec, or hitting
record in a terminal) produce a one-off binary with no source: when the output
drifts, nobody can regenerate it and the GIF quietly starts lying. Same reason
everything else generated in this repo has a committed source of truth.

## Writing a tape

Each tape opens by sourcing `demo-init.sh`, which builds a throwaway four-project
Go workspace in a temp dir, puts this checkout's `magus` binary first on `PATH`,
and leaves the shell inside it:

```tape
Hide
Type "source tapes/demo-init.sh"
Enter
Type "clear"
Enter
Show
```

Two things that setup buys, both load-bearing:

- **The workspace is generated, not checked in.** magus discovers a project from
  any `magusfile.buzz` below the workspace root and has no ignore mechanism, so
  a demo magusfile committed here would be picked up as a real project of this
  repo - the same breakage a stale `.claude/worktrees` copy causes (MGS1002).
- **The cache is genuinely cold.** magus keeps its cache in `.magus/` under the
  workspace root, so a fresh temp dir means the first run really does the work
  and the second really replays it. No tape clears the developer's own cache.

Then keep it to a few beats with a `Sleep` after each - the pauses are where a
reader's eye actually lands. `Set Height` is set by the _last_ frame: a GIF
loops, so whatever the final frame shows is what someone who glances at it sees.

## Keeping them useful

These earn their place by showing something a code block cannot: elapsed time,
output arriving in stages, a cache hit landing. A tape that only shows text
should be a fenced code block instead - it will be cheaper, copyable, greppable,
and readable to someone on a screen reader.

Give every embed real alt text describing what happens, not "terminal demo".
