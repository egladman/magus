### Changed

- **Breaking: a figure draws at one level, and a mixed-level draw is refused.**
  `figure\of(id, level:)` declares a `figure\Level`: process, project, package, file or
  symbol; left out, the boxes decide it. A box of another level is refused with the figure
  it belongs in. `Level.file` has no unit yet and is refused.
- **Breaking: an actor is built by what it stands for.** `figure\person()`,
  `figure\external()`, `figure\step()` and `figure\stage(dir, refs:)` set `Actor.kind`, and
  an `Actor` with no kind is refused. A stage is a symbol defined directly in `dir`. A host's
  actor with no kind is a step.
- **Breaking: a figure's description is at most 60 words.** It is the alt text.
- **A figure past the box or edge budget names the narrower scope to draw apart.**
- **`figure\atlas(figures, column:)` reads a registry's figures together.** Its
  `diagram(f)` links each box to the figure one level down that draws it and refuses a
  figure wider than `column`. The docs registry draws at a 1200-unit column and lists the
  groups no figure draws under `undrilled` in each receipt.
- **A figure receipt counts its claims.** Every receipt reported zero imports, calls and
  flows, since the count compared the `Claim` enum to a string.
- **The 11 mixed-level docs figures draw at one level**, 14 topic titles state a claim, and
  `buzz-packages` is a package overview of gopherbuzz that drills into `buzz-compile` and
  `buzz-run`.
