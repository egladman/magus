# Diff surface: what landed overnight, and what did not

Branch `feat/pwa-diff-viewer-replay-afea09`, worktree `wonderful-maxwell-80473c`. Everything
below is COMMITTED and UNPUSHED. Root suite, console tests, and console lint were green at
each commit; see "Not verified" at the end for what was not run.

The research that drove this is on a different branch:
`feat/diff-viewer-market-research-b03191`, at `plans/diff-viewer-market-research.md` plus
`plans/diff-viewer-personas/` (seven persona reports).

## Commits, oldest first

| Commit | What |
| --- | --- |
| `0fa5ae641` | The four honesty fixes: nil-able reach, UNRANKED banner, positional rejection, credential-free trail, author-edited seeds, unseen files rank up |
| `2559e6bc7` | The rename: `magus diff` everywhere |
| `7188c44fa` | stdin/patch-file input, watch mode, OSC 8 links, the UNRANKED gate fix, agent_name on comments, fold reason, hotspot cutoff |
| `4b5fc7b76` | Accessibility pass on the virtualized grid |
| `4c5f40609` | Word-level intra-line emphasis |
| `d53349922` | Drop SCIP locals from the annotated diff |
| `2da3946ad` | Doctor check: is the agent observer actually recording |

## The rename, in case something still says "review"

`magus review` -> `magus diff`. Also `magus_review` -> `magus_diff` (MCP), `types.Review*` ->
`types.Diff*`, `internal/review` -> `internal/diff`, `magus\review` -> `magus\diff` (Buzz),
and the console's command ids `review.*` -> `diff.*`.

**The endpoint split is the part to remember.** `/api/v1/diff` was already taken by the raw
patch, so:

- `GET /api/v1/diff` - the ANNOTATED changeset (was `/api/v1/review`)
- `GET /api/v1/diff/patch` - the raw unified patch (was `/api/v1/diff`)
- `POST /api/v1/diff/session` - unchanged in meaning

Handlers follow that vocabulary: `DiffHandler` is the annotated one, `PatchHandler` is the
text. The rule is **Patch is the text, Diff is the annotated object**, and it is worth keeping.

`SortForReview` became `SortForReading`. The word "review" survives where it means the
ACTIVITY, which is correct; it was only removed as a feature name.

## The lead fix, and the trap inside it

`magus diff` claimed "ordered by what they can break" while rendering path order whenever the
symbol index was cold. `Reach` is now `*int` - nil means unmeasured, which is distinct from a
measured zero the way `Coverage` and `Churn` already were - and `Diff.Ranked()` reports whether
any ranking key existed. The CLI prints an UNRANKED banner BEFORE the list; the console shows
an `unranked` chip and retitles "Read these first" to "First by path (unranked)".

**The trap, because it will catch the next person too:** gating that on `graph != nil` does not
work. A knowledge graph loads perfectly well with no symbol shards in it, so the first version
set a measured zero on every file in exactly the workspaces that have no index, and the banner
never fired. The correct predicate is `impact.GraphStore(graph).HasSymbols()`, which is what
`impact.Enrich` already gates its own overlays on. I only caught it by running the binary
against a real dirty worktree - the compile and the tests were both green.

## Everything else that landed

- **`magus diff -` and `magus diff <patch-file>`.** A positional is accepted only as `-` or a
  readable file; a ref is refused with a message naming `git diff <ref> | magus diff -`. This
  is what makes the surface composable, and it annotates a patch magus did not produce.
- **`magus diff --watch`.** Re-renders on tree change, ignoring declared outputs so a
  regenerating target cannot drive the loop. Refused in combination with stdin or a file,
  because neither can be re-read.
- **OSC 8 hyperlinks** on every printed path, gated through `tty.WantsHyperlinks`, which
  already refuses a pipe, `TERM=screen`, and SSH. Piped output still carries zero escapes.
- **Accessibility.** `aria-rowcount` is the true total and `aria-rowindex` the ABSOLUTE
  position (the classic virtualization bug renders "row 1 of 10,000" at every offset); add and
  delete are announced in words with the glyph `aria-hidden`; the suggestion rail is a polite
  atomic live region; `prefers-reduced-motion` is honoured in the stylesheet and in `scrollTo`.
- **Word-level emphasis** (`console/src/console/diff/words.ts`, with tests). Common prefix and
  suffix on token boundaries, so a rename marks the whole identifier. Unequal del/add runs are
  deliberately left alone.
- **Credentials out of the trail.** `Touch.Ran` stores the PROGRAM, never the argument list,
  reduced at ingest. `TestReplayKeepsCredentialsOutOfTheTrail` pins it.
  **The token that leaked still needs rotating - that is yours to run**, see the memory entry
  `review-touches-ran-replays-raw-commands`.
- **SCIP locals dropped** from the annotated diff (roughly two thirds of the MCP payload), with
  newly-added exports deliberately kept.
- **Doctor `agent observer` check.** Fails on commands-with-no-reads, advises when reads are
  too sparse to explain anything, quiet below 50 observations. On this machine it immediately
  reported **4 reads against 272 writes and 1709 shell commands** - which corroborates the
  persona finding that the reading trail is effectively empty in practice. Worth chasing.

## NOT DONE

Nothing here was started and then left half-built; these were simply not reached.

### Task 4 remainder

- **Session `as_of`** (tree digest + timestamp). Nothing carries snapshot identity, so two
  clients cannot detect that they disagree and "9 of 12 read" loses meaning when the
  denominator moves.
- **"What changed since you last looked."** The persisted viewed digests already hold the
  input; this is the Gerrit "Diff Against" idea done cheaply.
- **A projection parameter on the MCP ops.** Every op still returns the whole session. The
  locals filter cut the bulk, but a `fields` parameter is still the right shape.
- **Gate mode** (exit code / threshold). The vibe-coder persona's point stands: this is a
  prioritizer, not a gate, and it did not stop a changeset whose tests did not compile.
- **Knowledge-loss / bus-factor join.** The ownership lens exists and the diff never reads it.
- **A diagnostic when a target writes a path it did not declare.** The bazel veteran's second
  ask, and the thing that would make the fold's completeness checkable rather than promised.

### Task 6: Buzz in WASM - NOT STARTED

Scoped but not built. What I established:

- `cmd/buzz-playground` is `//go:build js && wasm` and already compiles the interpreter plus a
  host surface to WebAssembly. It serves `docs/playground.html` - the DOCS SITE, not the
  console. The console loads no Buzz today.
- `internal/dry` is that playground's tracing host, and `TestMagusSurfaceMatchesBindings` keeps
  it in lockstep with the real `magus` surface. I paid that tax during the rename, so the
  mechanism demonstrably works.
- **The blocker is CSP.** `internal/service/console/static.go` pins `script-src 'self'`.
  Instantiating WebAssembly needs `'wasm-unsafe-eval'` added. That grant permits compiling
  WASM and still forbids JS `eval`, which is the whole argument for doing it this way.
- `types.Diff` is already registered as a Buzz runtime object in
  `cmd/magus-utils/boundary_types.go`, so the changeset is addressable from Buzz today.

Order I would build it in: (1) run the existing `.github/actions/advice/api-surface.buzz`
against the same Diff in the browser, so you see what the bot will post before you push - one
advisor source, two audiences; (2) user-authored lenses; (3) Buzz extensions that run in
terminal, CI, and browser alike; (4) offline patch review with no daemon.

## Not verified

- **`magus affected ci --no-default-charms` was not run.** Individual targets were: root
  `test`, console `test`, console `lint`, and `generate` after every schema change.
- The console changes were verified by tests and lint, **not by loading the surface in a
  browser**. The a11y attributes and the word emphasis have unit-level cover but no DOM test.
- `--watch` compiles and is wired, but I did not sit in a watch loop and edit a file.
