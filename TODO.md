# TODO

A running list, not a commitment. Nothing here is dated and nothing here is
promised. Items graduate out of it by getting done or by getting deleted once
they stop mattering.

Ordering is by what the next release needs, not by how interesting the work is.
The three buckets mean different things:

- **Release gate** blocks cutting a tag. Fix these first.
- **Cheaper before the tag** does not block, but lands in the binary or in
  user-facing vocabulary, so doing it after a release makes it a change users
  have to absorb rather than a thing that was simply always true.
- **After the tag** is anything that ships on its own cadence. The docs site and
  the console deploy from `pages.yaml`, independent of the release, so a broken
  page is worth fixing but never worth holding a tag for.

## Release gate

- [ ] **CI preflight is red and no release fixes itself.** `ci.yaml` pins
      `SETUP_MAGUS_VERSION: 'v0.1.0'` (2026-07-06). HEAD's `magusfile.buzz`
      imports `badge.buzz`, which imports the `xml` host module added 2026-07-22
      in `1edcaec5`. No published release contains `xml`, so the pinned binary
      cannot load the workspace and `magus doctor` fails before the shard matrix
      is ever computed. This is the documented release-first signal from
      CLAUDE.md, working exactly as intended: the magusfile needs an unreleased
      feature.
      Sequence, in order: cut a tag from HEAD, then bump the pin to it. CD does
      not consume the pin, so the release path itself is not blocked.
- [ ] **Make version skew an obvious, coded error instead of a confusing
      downstream one.** Today an old binary against a newer workspace surfaces as
      `import "xml": module not found`, which reads as a typo. The fix has one
      hard constraint that rules out the obvious approach: **the binary producing
      the error is the OLD one, so it cannot possibly know that `xml` was added
      later.** A generated "module introduced in version X" table cannot help in
      the failing direction. Anything that works has to be something an old
      binary can evaluate against a version it has never heard of, which means a
      declared minimum, compared as semver.
      Recommended shape, two halves that close the loop:
      - **The workspace declares its floor.** `magus.yaml` already exists, so add
        a required-version key there and check it in workspace preload, BEFORE
        the magusfile is evaluated, since the magusfile is the thing that
        explodes. Prior art to copy rather than invent: Terraform's
        `required_version`, Go's `go` directive, npm's `engines`. Give it its own
        code on the MGS rail so it is greppable and linkable, and make the
        remedy name both fixes: upgrade the binary, or raise the pin in
        `setup-magus`.
      - **A ward keeps the declaration honest.** A floor only helps if somebody
        remembers to raise it, and nobody will. But the NEW binary does know
        which version introduced each host module it ships, so `doctor` (or a
        ward) can flag "this workspace imports `xml`, added in 0.3.0, but
        `magus.yaml` only requires >= 0.2.0" and fail the drift gate. The old
        binary reads the floor; the new binary proves the floor is accurate.
      Note the clock: this cannot retroactively improve v0.1.0's message. It only
      starts paying off for skew between the release that ships it and everything
      after, so shipping it in this tag is worth more than shipping it in the
      next one.
- [ ] **Finish the stale-generator guard: `serve` and `agent install` are done,
      the rest of the surface is not.** Two shipped this session. `serve` stats
      `os.executable()` each watch tick and refuses to keep regenerating when the
      binary changed underneath it. `agent install` now stamps a fingerprint of the
      embedded skill sources, so `graph verify` catches content drift instead of
      trusting a hand-bumped counter - that one was found the hard way, by an install
      from a stale binary writing old skills while verify reported up to date.
      What remains is every OTHER long-lived or generated-output path with the same
      shape: the daemon (its adoption gate covers IPC skew but not "my own tree moved
      on"), and any target that writes committed output from a process that outlives a
      rebuild. Audit rather than guess which ones qualify.
      Keep the framing from the version-floor item above: an old binary across a
      RELEASE boundary and an old binary across a PROCESS LIFETIME are one concept,
      "the magus doing this work is not the magus this tree expects." The digest
      approach generalizes - derive the check from bytes, never from a counter a human
      has to remember to bump.

- [ ] **`setup-magus`'s source-build mode is both unintuitive and wrong by
      default for its stated audience.** Two separate problems:
      - `version: ''` meaning "build from source" overloads the empty string.
        GitHub Actions inputs are all strings and sentinel values are the
        established idiom there (`setup-go` takes `stable`/`oldstable`,
        `setup-node` takes `lts/*`), so a named sentinel like `version: source`
        says it out loud in the caller's YAML. Prefer that over a second boolean
        input, which would create four states of which two contradict each other
        and need validating.
      - **The default is backwards for external repos.** The source build runs
        `go build ./cmd/magus`, which only exists if the magus source tree is
        checked out. The action's own description acknowledges it "can't assume
        the magus source tree ... is checked out", yet it now defaults to exactly
        that mode. For an outside repo the correct default is the newest release.
        Suggested arrangement: default `latest`, CD passes `source` explicitly
        (it is bootstrapping the release, which is the real reason it needs
        HEAD), CI keeps passing an explicit pinned tag (which is what makes it
        the compatibility gate). Every caller then states its intent, and the
        default is right for the audience the action is written for.
      - While in there: the `prebuilt` step is `continue-on-error: true` and
        falls back to a source build, so a genuinely broken download (bad tag,
        network, signature mismatch) is indistinguishable from "no release
        requested" and fails silently into a different mode. Once `source` is
        explicit, a failed prebuilt fetch should be fatal.
- [ ] **`describe.Extract` hard-codes the identifier `ctx`, but the contract
      enforces only the annotation.** MGS1008 checks `ParamAnnots[0]`
      (`internal/interp/runtime.go:53`); the graph extractor additionally
      requires `id.Name == "ctx"` (`internal/describe/extract.go:294-301`,
      `:318-321`, `:272`, `:384`). So
      `export fun build(c: magus\Context, args: [str]) > void { c.needs(format); }`
      loads clean, runs correctly, and contributes ZERO dependency edges and zero
      inputs/outputs to the static graph. `magus affected` and the cache key go
      silently wrong with no diagnostic.
      Pre-tag because it is a contract question, not a bug fix: either the
      annotation is the contract and the extractor must read the declared
      first-parameter name from the AST it already parses, or the name `ctx` is
      also part of the contract and wants a ward. Silent wrongness in the
      affected graph is the worst failure this tool can have.
- [ ] **Decide whether `magus.Context` should be per-invocation.**
      `plans/context-threaded-targets.md` specifies constructing and injecting a
      Context per target invocation; the implementation stashes ONE shared
      stateless instance per session (`internal/interp/bindings/buzz.go:118`,
      `internal/interp/runtime.go:432`, and the doc comment at
      `bindings/target.go:310-311` says so plainly). Harmless today, because all
      per-invocation state rides the Go ctx. It matters because a shared instance
      can never carry per-target identity, which is exactly what the planned
      `ctx.skip_cache()` / `ctx.exclusive()` / `ctx.slots(n)` need. Those are
      still unbuilt, and the stringly name-keyed policy side-map they were meant
      to replace is still load-bearing at `magusfile.buzz:45-58` and
      `docs/magusfile.buzz:76-89`. Settle the shape before the tag; build after.

RESOLVED, no action: spell ops do NOT need the threaded ctx. A fork op handler is
a declaration called once at resolve time with a null Target
(`internal/spell/resolve.go:150-162`), and no built-in spell reads its `Target`
at all, so a Buzz-level ctx argument would reach nothing. Everything it would
carry already arrives per-invocation on the Go ctx via `vm.ctx`
(`libs/gopherbuzz/vm/vm.go:940`), freshly built per call by `NewVM(ctx)`
(`libs/gopherbuzz/session.go:1108`): cancellation, cwd, charms, sandbox policy,
limiter, journal, and the telemetry provider are all verified reaching the op
body, and there is no `context.Background()`/`TODO()` on the path. Threading it
would also be UNENFORCEABLE - synthetic-module members get no arity or type
check, so migrated and un-migrated call sites would both silently "work" across
143 in-repo sites plus every user magusfile. The one genuine observability gap,
a per-op span, is a pure Go change at
`internal/interp/bindings/command.go:41`, no Buzz surface change.

## Cheaper before the tag

- [ ] **`ci.yaml` reads as more complex than magus is.** Failed its real test:
      it was going to be shown to someone and got pulled because it looked far
      more advanced than the tool actually is. That is a shop-window problem, and
      the shop window matters more at a release than almost anything else here.
      The pitch is "`magus affected ci` is basically all you run in CI." The file
      undercuts it two ways, worth separating before touching anything:
      - **Bulk is mostly commentary.** Much of the length is explanatory prose
        about cache immutability, remote-cache signing, shard capping, and the
        history cache. Each comment is individually justified and collectively
        they bury the four lines that do the work. Consider moving the rationale
        into `docs/` and leaving pointers, so the workflow reads like
        configuration instead of an essay.
      - **The shape genuinely is more than one command now.** preflight,
        a matrix fan-out, a shard-merge, and postflight. Some of that is real
        (sharding needs a matrix, and GitHub needs it computed in a prior job),
        but decide honestly which parts are essential and which accreted. If the
        answer is that sharding requires this much scaffolding, that is a signal
        magus should absorb the scaffolding, not that the YAML should keep it.
      Do NOT delete platform coverage to make it shorter. The suggestion to push
      shared steps into `setup-magus` is a reasonable direction, but note the
      tension with the item below: that action is also the thing outside repos
      consume, so it should stay a thin install primitive rather than becoming a
      grab-bag of this repo's CI conventions. Prefer a second, clearly
      magus-repo-internal composite action for repo-specific reuse.

- [ ] **Rename "surface".** The term is not discernable to anyone who has not
      read the source. It appears in CLI output and in the embedded agent
      skills, both of which ship inside the binary, so renaming after a tag is
      user-visible churn. Pick the replacement against the naming rules already
      in place: no noun-plus-preposition, reuse a prior-art term rather than
      inventing one.
- [ ] **Teach the `magus-docs` skill the search endpoint.** So an agent can
      query raw/undocumented JSON results instead of guessing a URL. The skills
      are embedded in the binary, so this rides along with the tag or waits for
      the next one.

## After the tag

Broken in public first, polish second.

- [ ] **`/magus/guides/` 404s, and so do seven other sections.** `render.buzz`'s
      `walk()` only ever calls `renderFile()` on `.md` files, so a directory
      never produces output of its own. Every section without a same-named
      sibling `.md` is a dead URL: `guides/`, `concepts/`, `migrating/`,
      `reference/`, `reference/codes/`, `reference/codes/auth/`,
      `reference/manpage/`, `console/activity/`. Only `reference/buzz/` resolves,
      because it has an authored `index.md`.
      Prefer the generator over eight hand-written stubs: `renderAutoindex()` in
      `docs/site/public.buzz:102-183` is the working precedent, and
      `renderSectionNav()` in `docs/engine/types.buzz:529-580` already computes
      direct children by clean-URL prefix. A new writer wants both, plus
      `site.addRecord()` for search/sitemap and `site.pageUrls` so
      `updateUrlLock` folds the new URLs into `docs/active.urls.lock`.
      Note why this went unnoticed: `renderBreadcrumb()`
      (`docs/engine/types.buzz:399-412`) emits an UNLINKED `<li>` when
      `lookupTitle()` finds no page, so nothing on the site ever links to
      `/guides/` and the asset-integrity gate has no broken link to catch. Worth
      making that case loud rather than silent.
- [ ] **Subcommands are inconsistently cross-linked.** The token set is built
      from manpage filenames in `docs/engine/types.buzz:667-675` as
      `"magus " + sub`, so it is one subcommand deep and the literal `magus `
      prefix is mandatory. Matching is byte-level substring, not regex
      (`docs/lib/glossary.buzz:173-181`, `:188-215`). Three distinct problems,
      worth deciding separately:
      - Flags and trailing words fall outside the anchor: `README.md:311`
        `magus describe graph -o markdown` links only `magus describe`.
      - Only the FIRST occurrence of a token per page is linked
        (`glossary.buzz:203-206`), so the same string at `README.md:371` gets
        nothing.
      - A verb with no manpage cannot link at all: `README.md:72`
        `magus explain <node>` is bare, as are `magus query` and `magus refs`.
        This one is arguably a missing-manpage bug, not a linker bug.
      Also note the stale comment at `types.buzz:645` claiming
      `"magus affected ci"` outranks `"magus affected"`; no such token can exist.
- [ ] **Style the runnable-snippet output pane distinctly from the editor.** No
      purple line, no gap. All in `docs/src/styles/site.css`: the violet spine on
      the output is `:1925`, the matching spine on the editor is `:1809`, and the
      gap is the `0.4rem` top margin at `:1922`. The pane currently borrows
      `--pico-card-sectioning-background-color` (`:1921-1928`), the same fill as
      the action bars at `:1823`, which is why output reads as more chrome; it
      wants its own background/color pair. Once the spine goes, revisit the
      squared corners at `:1827` and `:1833` that only exist to meet it.
      `docs/playground.html:180` already does the intended thing with a plain
      `border-top` and no accent - reuse that treatment.
- [ ] **Sample data for the target graph visualizer.** It is hard to evaluate a
      graph view against an empty graph, and a first-time visitor sees nothing.
- [ ] **CSS linting for the console.** Inconsistent input and button sizes have
      survived several passes because nothing enforces them. Worth checking
      whether a linter can assert the size scale, or whether this wants design
      tokens instead so the sizes cannot diverge in the first place.

## Backlog

- [ ] **`magus-report`: a skill that drafts a bug report, feature request, or
      piece of feedback, and probes for the X/Y problem while doing it.** Dry,
      concise, articulate output; keeps asking why until it reaches the actual
      problem rather than the solution the reporter already imagined.
      The value is demonstrated, not hypothetical. The 2026-07-25 session opened
      with "don't we need to thread ctx to the spells?" - a Y. The honest answer
      was no, and probing it is what surfaced the X: six `libs/diag` targets
      broken by a duplicated parameter, and `describe.Extract` silently dropping
      graph edges when the ctx parameter is named anything else. A skill that
      did that probing reliably would have arrived faster.
      Decisions already made, so they do not need relitigating:
      - **Hand-authored, NOT installed. Do not ship it in the binary yet.**
        `magus-skill-authoring` is the precedent: committed, deliberately outside
        the installed set. The unsettled question is audience - a skill for the
        maintainer drafting issues on this repo is not the same artifact as one
        for users filing against magus, and the latter needs magus's principles
        to ship as readable data. Starting hand-authored keeps the reversible
        option; bundling is the door that does not reopen.
      - **Name which principle a request touches; do not render a verdict.** The
        original framing was "assess whether I would even entertain this." Reject
        that. Automating a maintainer's judgment discourages contributors and is
        sometimes simply wrong, which is a bad trade for a project whose thesis
        is enablement. Articulate the tension and leave the call to a human. The
        valuable cases are the ones where a good idea genuinely conflicts with a
        principle worth revisiting, and a verdict-shaped skill hides exactly
        those.
      - **Delegate the is-this-a-good-idea half to the existing `pre-mortem`
        skill** (Tiger / Paper Tiger / Elephant), which already produced the
        second-language No-Go. Do not reimplement that reasoning.
      Open question worth resolving before building: the prose is the commodity
      half - any agent writes a tidy issue. The differentiated half is evidence
      only magus can gather (version, `doctor` output, workspace shape, the
      failing output ref). That suggests a paste-ready environment block behind a
      command. Note the tension honestly: that is a new subcommand, against the
      fold-rather-than-add rule, so it likely belongs as a flag on `doctor`,
      which already collects most of it.
- [ ] **A second authoring language (Nim / NimScript?) - the No-Go still
      stands.** This was already pre-mortemed and answered on 2026-07-21
      (`~/.claude/plans/pre-mortem-from-a-robust-snail.md`): No-Go on a second
      general-purpose interpreter, Conditional Go on a cheaper path. Nothing
      since then has moved the blocking Tiger, which is upstream of code: no
      adopter has ever cited Buzz as the blocker, and there is no
      released-binary adoption loop yet to hear it from. Validating that barrier
      is the honest test, and it costs weeks, not months.
      Two things learned since that sharpen it:
      - The Buzz coupling is WORSE than recorded. The pre-mortem noted graph
        extraction parses the Buzz AST with no neutral IR; it now also
        hard-codes the parameter identifier `ctx`
        (`internal/describe/extract.go:294-301`). A second language would have
        to reproduce not just the AST shape but that convention.
      - Nim specifically raises the cost rather than lowering it. It clears the
        stated bar (mainstream-ish, statically typed) where Starlark, Lua, and
        Tengo all fail on static typing. But NimScript is a Nim subset executed
        by Nim's own VM, so embedding it in Go means reimplementing that VM, and
        Nim is a far larger language than Buzz - macros, generics, an effect
        system. Buzz was chosen precisely because it was small enough to build
        solo. Nim is not, so the parity trap is deeper, not shallower.
      If the barrier does validate, the pre-mortem's cheaper path is the one to
      take first: a declarative escape hatch over the genuinely small core
      (`project`/`needs`/`outputs`/`charms`) lowering to the same
      `types/describe.go` graph through `project/shadow.go`. That captures the
      easy 80% without doubling the language surface.
      Keep the named elephant visible: writing an interpreter is intrinsically
      rewarding, which makes "it lowers the adoption barrier" the most likely
      thing to be a post-hoc rationalization.
- [ ] **FlatBuffers instead of JSON for graph storage.** Deliberately not
      pre-tag. It is a stored-format change, so it wants a release boundary
      behind it, and the optimization rules here require a checked-in benchmark
      plus benchstat evidence before any of it counts. Measure the JSON decode
      on a cold graph build first; if it is not on the hot path, this stays a
      backlog item forever, which is a fine outcome.

## Loose ends noticed in passing

- [ ] **No count affordance, which is what makes agents reach for `grep -c`.**
      `magus graph stats` reports god nodes, connectivity, orphans, and doc
      coverage, but not population per kind, so the only way to get "how many
      targets are there" is to count lines of `-o name`. The magus-architecture
      skill was doing exactly that, and its own bad example is how the habit
      spreads. Now uses `-o json | jq length`, which is at least consuming a
      contract, but the real fix is for magus to answer the question directly -
      per-kind counts in `graph stats`, or a documented count in `query`'s JSON.
      Every text filter an agent writes is a missing output format; this is the
      clearest instance.
- [ ] **`magus run <target>` has large fixed overhead versus the underlying
      command.** `magus run bindings_generate` was slow enough to look hung and
      get killed twice, while the `go generate ./std/...` it wraps returns
      instantly. Prime suspect is preload: every invocation in this workspace
      attempts ~20 docs SSG `.buzz` files as spells and fails each one (the
      BZZ2001 noise above), so the same wasted work is repeated on every command.
      Worth measuring before assuming - but if preload really is the cost, it is
      paid by every magus command a user ever runs, which makes it the highest
      leverage perf item on this list, well ahead of graph storage format.

- [ ] `setup-magus/action.yml` references `steps.magus-cache.outputs.cache-hit`
      in two `if:` conditions, but no step declares `id: magus-cache`. The guard
      is dead, so the install always runs, and the action description promises a
      tool cache that was never wired up. Either add the cache step or drop the
      claim and the dead conditions.
- [ ] `.github/actions/setup-magus/action.yml:6` has an em-dash in the top-level
      `description:`, which GitHub renders in its UI, so it falls under the
      plain-ASCII rule for user-facing strings. (The one at `:57` is a code
      comment and is exempt.)
- [ ] `.gitignore` has no pattern for editor autosaves. A tracked `#action.yml#`
      had to be removed by hand; `#*#` would stop the next one.
- [ ] `magus doctor` prints about twenty `load local spell` BZZ2001 errors on
      this repo's own tree: the docs SSG modules under `docs/lib/`,
      `docs/engine/`, and `docs/site/` import each other by path (`lib/text`,
      `engine/types`, `lib/url`), which only resolves with `docs/` as the working
      directory. They are non-fatal - no check fails on them - but doctor is the
      first command a new user runs, and on the flagship repo it currently looks
      broken. Either scope spell discovery so SSG modules are not loaded as
      standalone spells, or make the errors a single quiet note.
