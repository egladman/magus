---
title: Changelog
generated_from: CHANGELOG.md
description: Every released change to magus, newest first, in Keep a Changelog format. Generated from releases/*.yaml.
tags: [changelog, releases, versions, upgrade, breaking-changes]
---

# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

See the unreleased changes at
https://github.com/egladman/magus/compare/v0.3.0...v0.4.0

### Breaking

- **`magus query` exits 1 when it could not answer.** A search that matched nothing
  and reported the `unknown` verdict - a stale symbol index, or a lazily-loaded layer
  the lookup never consulted - now exits 1 where it exited 0. `found` and `absent`
  still exit 0, which is the documented rule this keeps rather than the one it
  changes: an empty result set is a legitimate answer to a search. What changes is
  the case that was never an answer at all, where exit 0 with a caveat buried in the
  text reads as "not in the graph" and sends the caller back to a text search - and
  it is the one case a command fixes, `magus graph build`. The status is the same
  under every `-o`, `query` still never exits 2, and a populated result still exits 0
  whatever its verdict, because there the caveat rides rows that are already facts.
  `magus query <terms> && next-step` against a workspace whose index is missing or
  stale now stops where it used to continue; a caller that wants the old behavior
  branches on `.answer.verdict` from `-o json` instead.

### Removed

- The four JSON run-browser routes are gone: `GET /api/v1/outputs`,
  `/api/v1/output?ref=`, `/api/v1/runs` and `/api/v1/run?inv=`. The typed
  `magus.viewer.v1alpha1.ViewerService` replaces them - `ListOutputs`,
  `GetOutput`, `ListInvocations` and `GetJournal` read the same two stores, and
  the contract they speak is generated rather than hand-marshaled. The service
  had been defined since the log viewer shipped and was never mounted; the JSON
  routes were hand-written strings with no schema behind them.
- `magus.FailOnDrift()` is gone, with no replacement alias. It named the
  response rather than the decision, so it could not carry "warn" or "off", and
  what it checked - whether the working tree was dirty after the target - was
  disarmed by any unrelated uncommitted edit. Use `magus.Drift(policy, reason)`,
  or the `drift` key in a magusfile's target policy.

### Changed

- **One unreadable handoff entry no longer takes the readable ones down with it.**
  `magus memory` used to fail a whole listing when any single record failed to parse or
  validate, which disabled the surface a person would use to find and delete that record.
  Reading now skips the bad entry and returns the rest, with the problem reported by
  `magus memory verify`. An entry whose `type` this binary does not know is reported as a
  warning and skipped, so a journal written by a newer magus stays usable by an older one;
  `verify` stays green on it, since there is nothing to repair. Writing an unknown type is
  still an error: the tolerance is on the read path alone. This helps only binaries built
  from this release forward. A magus already shipped still refuses the whole listing when
  it meets an `elimination` entry, so a journal shared with an older checkout needs that
  checkout upgraded.
- **`magus query` gains operators: `kind=spell` (match), `kind!=op` (exclude),
  `id=~build$` (regex).** `=` reads as a match over a structured graph the way
  kubectl selectors and PromQL matchers do, and `!=` carries negation without the
  flag collision the dash spelling had. `kind=~"spell|op"` ORs alternatives, and
  the filters read the same in every verb they appear in. The `:` grammar
  (`kind:spell`, `-kind:op`) still parses as a compat alias, so existing
  invocations keep working; new queries should use `=`/`!=`/`=~`.
- The drift gate runs for **every** target that declares outputs, and no
  declaration turns it on. Declaring an output is already the claim that those
  bytes follow from the target's inputs, so magus checks the claim rather than
  asking each workspace to opt in. It previously applied only to `preflight` and
  `generate`, and only when a target declared `FailOnDrift`. Turn it down with
  `{"drift": "warn"}` or off with `{"drift": "off", "drift_reason": "..."}`; a
  reason is required to switch it off, as it is for `skip_cache`.
- The drift gate fails only for output **this change** made stale. Output that
  drifted with no source change behind it - a merge whose own CI never finished,
  a generator nobody re-ran - is reported and does not fail the run. Failing it
  billed whoever opened the next pull request for a decision they were not party
  to, and they could not fix it without committing bytes they did not produce.
  This is not configurable: there is no setting that restores the old behavior.
- The gate now hashes each target's declared outputs before and after the run,
  rather than asking the VCS what is dirty afterwards. Bytes that did not move
  are not drift however dirty the surrounding tree is, and detection no longer
  depends on the VCS at all - only attribution does.
- Agent guard templates are at version 9. Codex's hook commands now carry
  `GUARD_NO_ADVISE=1` in `hooks.json`, and the notice a template prints when the
  magus it found cannot judge a command now names the evidence - which binary
  path it resolved, that binary's version, and the error it actually printed -
  rather than offering "too old for `session hook`, or cannot load this
  workspace" as equal suspects. The second was never a cause: the deny rules need
  no workspace, and a current binary run from an empty directory still denies, so
  that half of the sentence sent readers to check something no evidence pointed
  at. Re-download the templates to pick both up; enforcement and the rendered
  verdict are unchanged.

### Fixed

- **`release-build` works on an untagged commit again.** When HEAD carries no root
  release tag, `version()` falls back to `git describe`, and describe picked among
  every tag in the repository - so with four v0.4.0-era tags on one commit it
  answered `libs/diagnostics/v0.1.0-1-g<sha>`, a version with a `/` in it, and
  `release-build` refused. That made the target unusable on main, which is a large
  part of why the release path went two versions without being exercised. The
  fallback now constrains describe to `v[0-9]*`, which is the same shape check
  `isReleaseTag` applies: it excludes this repository's `verified-refinements` tag by
  the digit and every `libs/<name>/v0.1.0` by the leading `v`. The magusfile's note
  claiming a namespaced base was something "no version resolution here can prevent"
  was simply wrong.
- **`vcs\tags` no longer renames a tag that shares its name with a branch.** The
  git backend asked for `%(refname:short)`, which abbreviates a ref only as far as
  it stays unambiguous - so a repository holding both a `v0.4.0` branch and a
  `v0.4.0` tag got the tag back as `tags/v0.4.0`. That name matches no `v*`
  pattern and splits into the module prefix `tags/`, so the tag was invisible to
  every caller asking whether a release exists: the `tagged` charm declared a
  tagged HEAD untagged, and `magus run release` surveyed the repository as still
  sitting on the previous version. The v0.4.0 release ran against a repository
  whose own release tag it could not see. The name is now the tag as written.
- **A release commit carrying several tags no longer names its assets after the
  wrong one.** magus's own build read its version from `git describe`, which picks
  arbitrarily among the tags on one commit; cutting v0.4.0 tagged three nested
  library modules on the same commit and describe chose one of those. Every
  archive was then named `magus_libs/diagnostics/v0.1.0_<os>_<arch>.tar.gz` - a
  path, not a filename - so the builds landed in a nested directory the release
  workflow's upload glob never looked in, and the release published no downloadable
  assets while reporting success. The version is now the root release tag on HEAD
  when there is one, and `release-build` refuses outright, before it compiles
  anything, if the version it resolved would put a directory separator in an asset
  name.
- `magus describe file` no longer classifies a path that is not in the workspace.
  It is pure glob matching, and `**/*.go` matches an absolute path from another
  checkout as happily as a relative one - so a fabricated or mistyped path came
  back `project: .`, `role: source`, `declared: source . **/*.go`, byte-identical
  to a real file beside it. A path that resolves outside the root is now
  `unclaimed` with a hint naming the workspace it is not in, and every entry
  carries `exists`, printed as `exists: false` in the text form and always
  present in `-o json`. Classification is deliberately still answered for a file
  that is not there yet - "where would this land" is a question worth asking -
  so this reports existence rather than turning a missing path into an error.
- `magus describe file` reads a path shape the way `magus query` and `magus where`
  do, through the shared `file.NormalizeWorkspacePath`. Its own partial normalizer
  handled neither backslashes nor an absolute path rooted somewhere else, so
  `cmd\magus\guard_shell.go` was taken as a literal filename.
- `magus vcs resolve` no longer gives up when the merge left conflict markers in
  a magusfile. It reads the committed magusfile through the new
  `types.RevisionFileReader` capability, settles the generated conflicts with
  those declarations, and leaves the hand-written one for you - rather than
  reporting the interpreter's verdict on a `<<<<<<<` line and settling nothing.
  It deliberately does NOT regenerate in that state: a merge that changes a
  generator would produce bytes matching neither side, so it says to run
  `magus run generate:rw` once the magusfile is resolved.
- `magus vcs resolve` no longer loses the whole resolution to one renamed path.
  It staged with a single call whose pathspecs included files the rename had
  removed, and one pathspec matching nothing aborts the call before staging
  anything - so regeneration completed and the index was left untouched. Paths
  that cannot be staged are now reported by name instead.
- `docs:site-generate` declares the bundles it needs rather than relying on
  `generate` to order them first. It was correct as a stage and broken alone, so
  `magus run site-generate docs` - and `magus vcs resolve`, which invokes the
  target owning a conflicted output directly - failed the site's asset-integrity
  check on the playground's missing wasm glue.
- Agent guard: Codex is no longer sent advisories it rejects. Its PreToolUse
  treats `additionalContext` as an error and then fails open, so each advisory
  was discarded and disarmed the guard for that call. It now declares
  `advise=none`. The two fail-open notices still carry the key.
- Agent guard: the `relock` charm is now taught in the magus-run skill, and a
  gate keeps every advisory covered there. An advise injects on one host of
  four, so guidance carried only by a verdict never reached the rest.

### Added

- **`magus run release` preflights the release before it creates a tag.** Every
  named module's would-be tag now goes through checks that used to fire hours later
  in CI, on a tag that a pushed version number makes unreusable: a branch already
  using the tag's name, which leaves the tag ambiguous to every tool that resolves a
  short ref even though magus itself now reads tags as written; the version
  `release-build` would stamp once those tags sit on HEAD, and whether every asset
  name it produces matches the glob `release.yaml` uploads with; the two things
  `magus-utils cut` refuses on in the publish job, an existing
  `releases/v<version>.yaml` and an empty `[Unreleased]` section; and that the root
  tag matches the workflow's trigger while no module tag does. The dry run prints
  every verdict and refuses nothing, so a rehearsal shows all the problems at once;
  `release:cd` refuses on any failure and tags nothing. The dirty-tree refusal moved
  after the transcript for the same reason - it used to hide every check behind it.
  This is the v0.4.0 release stated as a gate: `git describe` chose
  `libs/diagnostics/v0.1.0` from among four tags on one commit, every asset was named
  `magus_libs/diagnostics/v0.1.0_<os>_<arch>.tar.gz`, and a glob star does not cross a
  `/`, so `dist/magus_*.tar.gz` matched nothing and the release published zero assets
  while every build job passed.
- **`release-build` writes `dist/ASSETS` and every build job asserts it before
  uploading.** One line per archive the build named, checked against the same glob the
  upload step is about to expand, through the new
  `.github/actions/assert-release-assets`. A naming regression now fails the build job
  with the computed asset named, rather than the uploader finding nothing - and a glob
  that selects nothing is not an error in any shell, which is how the failing upload
  reported success. `release-build`'s own guard now asks the upload glob instead of
  `fs\basename`, so it also catches a rename of the `magus_` prefix on one side of the
  handoff alone.
- **`audit.yaml` builds and checks one real release asset weekly.** The release path
  went from v0.3.0 to v0.4.0 with no `release.yaml` runs between them, so the combined
  library-plus-root flow had never run end to end when it was first asked to publish.
  Its own job rather than a fold into `determinism`, whose charter is generator
  byte-stability, and on the weekly audit rather than on every pull request because it
  guards against staleness rather than against any particular diff.
- `buzz-test` now runs `releaser.buzz`. Its test blocks had never been executed by any
  target - the root list named `badge.buzz` and `coverage.buzz` and nothing else - so
  the rules deciding what a release may be versioned as were covered by tests nobody
  ran.
- **The handoff journal records what an investigation ruled OUT as well as what it
  concluded.** A fourth entry type, `elimination`, names the dead hypothesis, carries the
  reason in `--body`, and requires `--excerpt`: the captured evidence that killed it,
  copied into the record. The excerpt is required because the ref beside it is not
  durable. An output blob lives under the checkout that produced it while the journal is
  keyed by repository, so a ref minted in an agent worktree stops resolving the moment
  that worktree is removed, which decays a ref-only record into a dangling pointer with a
  confident tone. The ref stays as a best-effort reopen handle, and `magus memory verify`
  now warns when one no longer resolves. A session that hits its limit used to take its
  whole elimination trail with it, leaving the next one to re-tread falsified branches.
  Available from `magus memory put`, the `magus_memory` MCP tool, and the console;
  `magus notes promote` carries the excerpt into the note. Nothing is captured
  automatically and nothing gates on it.
- **`magus agent adoption` measures whether agents actually use the knowledge graph.** It
  reads a corpus of shell commands (stdin or `--commands`) and reports how often the graph
  (`query`/`refs`/`explain`/`path`) was reached versus a raw text search, the graph-to-grep
  ratio, and the top repo-wide greps whose pattern is a real identifier - each with the graph
  command to try for it. That command is routed by the pattern's shape through the same
  translator the live guard suggests with, so a diagnostic code and a Buzz op read
  `magus query` rather than `magus refs`, which covers compiled-language symbols only and
  would miss them both. `-o json` carries it as `run` on every top-pattern entry. magus stays
  host-agnostic: it analyzes commands, never a host's session logs, and the help prints the
  recipe to extract them. It turns "query before grepping" from doctrine into a number you
  can watch move.
- **The guard's search advisory hands back a command to run, scoped to the project you
  searched.** A repo-wide content search (`grep -r`, `egrep -r`, `fgrep -r`, `rg`, `ag`) or a
  file-find (`find -name`, `fd`) now carries a concrete suggestion rather than a principle to
  weigh: content routes by the pattern's shape to `magus query` or `magus refs`, and a
  file-find becomes `magus query kind=file id=~<re>` built from the name or extension it asked
  for. When the search named a directory the knowledge manifest knows as a project, the
  suggestion carries `project=~^<proj>(/|$)`. That is an anchored regex rather than
  `project=<proj>` because a node resolves to the LONGEST project owning it, so the exact form
  would drop every node under a nested project that the grep it replaces would have matched. A
  search spanning two projects abstains from scoping instead of emitting a filter that
  silently answers half of it. Suggestions are paste-ready shell: a pattern carrying `$`, a
  backtick, a backslash, a double quote, or a `!` is single-quoted, so pasting one cannot run
  a command substitution or trip history expansion in an interactive shell.
- **The knowledge graph now indexes markdown by heading, so prose is retrievable a section
  at a time.** Every heading in a tracked markdown file becomes a `docsection` node whose
  id and source are `<path>#<anchor>` - the same fragment a link into the rendered page
  carries, computed with the site's own goldmark auto-heading-id so the two agree. A page
  `contains` its sections and a section contains the headings nested under it. An agent
  looking for where something is explained runs `magus query "kind:docsection <terms>"` and
  gets the passage, not the whole file; the guard advises the pattern when it sees a `cat`
  or `grep` of a `.md`, and the magus-query skill teaches it. Knowledge schema is now v10,
  which forces a rebuild so an on-disk v9 store picks up the section layer.

- **`magus diff` reads the working tree's uncommitted changes, annotated and ordered by
  what they can break.** A changeset is a set of consequences, not a list of files, and
  alphabetical order spends a reader's attention at random - it gives a regenerated
  lockfile the same weight as a signature twelve packages depend on. Each file carries the
  evidence behind its rank: how widely its changed symbols are referenced, whether any
  referent crosses a project or the module boundary (the question a version bump turns
  on), the coverage a prior run observed, how often the file has been changing, and which
  agent sessions wrote it. Declared target outputs are folded away by default, because
  reading a generated file is reading a machine's restatement of an edit made somewhere
  else; `--generated` shows them anyway. `--watch` re-reads on every tree change, and a
  patch can be read from a file or `-` instead of the tree. None of it is a verdict: magus
  does not claim a change is breaking, since deciding that needs signature compatibility,
  a base-side index magus does not keep, and language semantics it does not model. It
  reports who can see the thing you changed. Also `magus\diff()` in Buzz, `magus_diff`
  over MCP, and `GET /api/v1/diff` (with `/api/v1/diff/patch` for the raw patch), all
  ranking by the same definition so a terminal, an advisor, and an agent cannot disagree
  about what to read first.
- **A person and an agent can pair on one diff session.** The console's Diff surface
  (`/console/diff/`), `magus diff --tui`, and the `magus_diff` MCP tool attach to the same
  session when a daemon is reachable, so a comment written in the browser renders inline
  in the terminal and an agent cites a hunk by index rather than guessing one. The session
  carries the digest of the patch it was computed from and `op=state` recomputes when the
  tree has moved, rather than replaying whatever a browser last attached - an agent is
  never served a changeset that stopped existing, and a path or hunk the change does not
  contain is refused. `POST /api/v1/diff/session` is the human half. The surface also
  answers with no daemon at all: `/console/diff/#demo` runs the real surface over a
  fabricated changeset.
- **`magus vcs checkpoint` prints the working state's identity and writes nothing.** No
  tag, no stash, no ref, no file - it reads the head revision, the branch carrying it,
  whether the tree is dirty, and a 32-hex digest of the uncommitted patch, so a checkpoint
  nobody kept has cost nothing but the probes. The digest is deliberately the same
  algorithm the diff session uses for its patch, so a checkpoint recorded when work was
  handed out and a session opened over the same tree produce the same string; either can
  be used to check the other. `-o name` prints the one citable token for a ledger cell,
  and `magus_vcs_checkpoint` serves the same facts over MCP. magus emits the facts and
  decides nothing about what they mean.
- **A delegation ledger, for recording what work was handed to whom.** An orchestrating
  agent writes one row per delegated unit through the `magus_ledger` MCP tool (`op` of
  `list`, `put`, or `clear`), in the vocabulary the `magus-delegate-multi-agent` skill
  defines; `GET /api/v1/ledger` is the read door onto the same file and the console's Plan
  surface (`/console/plan/`, served alongside `GET /api/v1/plan`) renders the units as a
  layered graph with the live run states overlaid from the pool. It records and never
  enforces: magus does not check that a worker stayed inside its owned paths, does not
  block a write outside them, and derives no verdict from a row. A `put` merges
  field-at-a-time under one lock rather than read-then-write, because an orchestrator
  advancing a unit's state while that unit's worker records its checkpoint would otherwise
  have the second write revert the first. `clear` reports how many rows it dropped, since
  clearing is both how a fresh plan starts and how one orchestrator silently erases
  another's.
- **A Sapling backend (`sl`), the fourth VCS magus drives.** Sapling is a Mercurial fork,
  so most of the hg driver's shapes carry over, but every one was verified against a real
  `sl` rather than inferred from that lineage - the places Sapling has diverged are
  exactly the places a transliterated hg driver fails silently. `sl tags` is a deprecated
  no-op and git tags are invisible even in a git-backed clone, so tag lookup and describe
  report nothing rather than guessing; `sl debugignore` answers "not ignored", which
  CONTAINS "ignored", so hg's substring test would have reported every path in the tree as
  ignored; and `sl merge` prompts where `hg merge` does not, so the driver names a merge
  tool explicitly. A cross-backend parity suite now pins the behavior every backend has to
  share, rather than the behavior git happens to have.
- **`ctx.observes(name, value)` declares an external fact a target's answer depends on.**
  An image scan is keyed on the image and the tree, but its answer also depends on the
  scanner's vulnerability database, which moves daily and is not an input magus can see -
  so a cache hit reports yesterday's CVEs against today's image. The only control was
  `skip_cache`, which forfeits caching forever to avoid the staleness. An observation
  joins the cache key instead, so a fact that moved is a miss and a fact that held still
  replays, and it keys as its own input class: `magus describe target <t> --cache` shows
  an `obs` line, so "why did this rebuild" names the external fact rather than blaming a
  source file. Like every other footprint declaration it takes literal arguments on the
  target's own `ctx`; a computed value is rejected at load, because the key is minted
  before the target body runs and a value probed at key time cannot reach it. That
  restriction is why this does not yet convert the image-scan case it was built for.
- **Third-party dependencies are graph nodes.** `magus query kind:package` lists them;
  `magus explain package:<manager> <name>` shows a package's version, whether it is
  indirect or replaced, and which projects require it. Nodes are keyed by manager plus
  name so an npm and a Go package with one name never share a node, a version bump edits
  an attribute rather than renaming the node, and two projects pinned to different
  versions of one package share a node that flags the split. Go modules only in this
  release; other manifest readers follow.
- **[MGS1028](reference/codes/magusfile/MGS1028.md) reports a changed file that seeds
  a project it does not key.** Seeding and keying are separate mechanisms and this is the
  case where they disagree: directory containment selects a project, the root project
  catches whatever no directory claims, but only DECLARED sources enter a cache key. So
  touching an undeclared file selects the project, magus runs the target, the key has not
  moved, and the answer is the one already recorded - a config edit at the root of a
  monorepo can rebuild and retest everything and produce nothing new. The silent half is
  worse and is the same declaration missing: when that file genuinely does change what a
  target produces - a lint rule set, a toolchain pin, a formatter config - the cache does
  not know. `magus affected --impact` and `--explain` emit it, naming the seed projects
  rather than the files, because both already mark each file inline and `magus describe
file` explains any one of them in full. `magus doctor` reports the standing set.
- **`magus describe file` reports the individual declarations behind a path, and which
  ones cover more than one of them.** `claims` carries each declaration that names the
  path with the project and target that made it and the glob that matched, which is the
  unit that answers "which target rewrites this" where the existing `output_of`/`source_of`
  summary only answers "whose tree does it appear in". A cross-project write is attributed
  to the DECLARING project, since that is the only one that can regenerate it. The claim
  set is wider than `role` ranks: it also carries the in-place edits of
  `ctx.modifiesExistingFiles` as `update`, a write nobody replays or cleans. `overlaps`
  groups the declarations covering several of the requested paths, once per declaration
  with the paths it covers rather than once per pair - a hundred paths under one glob is a
  hundred rows rather than five thousand. It is a fact and not a verdict: one target
  rewriting two paths may be a collision between two authors or exactly what one author
  intends, and nothing here decides which. `magus_describe_file` carries both.
- **`magus version` reports the daemon's version beside the client's.** There can be two
  binaries - this one, and the daemon that has been serving the workspace since it was
  started - and a daemon outlives the CLI that started it, so upgrading magus leaves the
  older code running until it is restarted. That is the case this exists to show. The
  daemon line reads "not running" when nothing answers, and `--client` skips the probe
  entirely for a script that wants the build stamp with no daemon I/O.
- **A hook payload carrying a prompt is recorded as a delegation handoff and exempt from
  judgment.** The guard reads what a host hands it by field shape rather than by tool
  name - a payload with a command is a command, one with a file path is a path, and one
  with a prompt is an orchestrator handing context to a sub-agent. There is nothing to
  judge in a prompt: no command, no path, only a context transfer to note, so no rule is
  evaluated against one and the guard never denies it. It is tested last on purpose, so
  adding the branch cannot change the verdict on any payload the guard already judged. The
  event records the sub-agent type where the host supplies one (it repeats across spawns,
  so it groups a delegation feed), falling back to the per-spawn description and then to
  the tool name. Every activity event also now carries the host's own transcript path as a
  POINTER, so a session id in the console leads somewhere; magus never reads the file. The
  console gains an activity drawer that renders every activity kind.
- **`magus status` reports the concurrency a run actually gets, and every live pool.**
  `concurrency` alone could not be budgeted against, because its common value is 0, which
  means "nothing was configured" rather than "no build may run"; `concurrency_effective`
  is that resolved through the default and the machine clamp. Each pool entry adds
  `available` (free slots, floored at zero) so the reader does not subtract, and `socket`
  so entries can be told apart. Two live proc servers used to produce no pool section at
  all - a "use --socket to select one" error stood in for it, withholding exactly the
  capacity and in-use numbers the question was about - and are now enumerated under
  `pools`.
- **The docs site announces a release from its own bar, and can hold a post back.** The
  announcement strip links the blog post for the newest shipped release rather than only
  naming the version, and a post marked `draft: true` in its frontmatter renders nowhere -
  no post page, no blog index entry, no feed item, no announcement link - so unfinished
  writing stays committed and reviewable in the repository. The gate lives in the one walk
  every renderer shares, because the draft test previously sat in exactly one of three and
  the front door hid a draft the blog index went on listing.

### Changed

- **A changed file now seeds every project that DECLARES it, not only the one whose
  directory contains it.** A project declaring a source outside its own tree was invisible
  to `magus affected` until the containing directory happened to be a project too, so an
  edit to a file a target genuinely reads could select nothing. Containment still seeds,
  and the root project still catches whatever no directory claims - that last case is what
  MGS1028 above reports.
- **Churn follows a file through a rename.** A rename used to split a file's history
  across every name it ever had, so each fragment ranked as a separate, quieter file than
  the one thing actually being rewritten. Each backend now reports what a commit did to a
  path - added, modified, deleted, or renamed, with the previous name on a rename - and
  attribution follows that lineage. `FileHotspot` gains `moves`, the number of times a
  file changed address inside the history window: a file that keeps moving is churning
  architecturally rather than textually, which is a different thing to know than its edit
  count and is not derivable from its path. A backend that cannot detect a rename reports
  it as a delete and an add, which is the old behavior rather than a wrong answer.

### Fixed

- **`magus --root <path> ls targets .` no longer reports that `.` escapes the workspace
  root.** A relative project ref was measured from the caller's cwd rather than from the
  workspace `--root` names, so `filepath.Rel` answered an outside cwd with a `../`-prefixed
  anchor and every relative ref inherited the escape: `magus --root <ws> ls targets .`
  failed with `project path "." escapes workspace root`. Refs now anchor at the workspace
  when one was named explicitly. A ref that genuinely escapes is still rejected - this
  stops magus inventing an escape the caller did not write, it does not widen what
  resolves - and inside the workspace a dot-relative ref still anchors at the cwd.
- **hg and jj reported cwd-relative paths where git reports workspace-relative ones.** The
  same question answered from a subdirectory produced paths that resolved against
  different roots depending on which VCS the workspace used, so churn, hotspots, and every
  consumer of a changed-file list were quietly wrong for anyone not standing at the root.
  Found by the cross-backend parity suite, which now pins this as behavior every backend
  shares rather than something git happens to do.
- **`magus memory ls -o name` printed `unsupported format` instead of the entry ids.** It
  is the one-per-line form every other listing command answers, and the shared renderer
  does not implement it, so a command that offers it has to render it - reaching the
  default case read as a broken flag rather than a gap. `magus affected --plan` likewise
  emitted JSON whatever `-o` asked for, and now honors it.

### Breaking

- **`magus insight` is removed.** The lenses were never a daily verb - they are a
  reporting surface, reached from CI and from a magusfile - and a subcommand is the
  one place they cost every reader of `magus --help`. The survivor is
  `magus\insight()`: the typed report, computed IN-PROCESS from the workspace magus
  already has open, plus `magus_insight` over MCP for agents, which never went
  through the subcommand at all. There is no document renderer: presentation is the
  caller's job, built from the typed report.

  What this costs, in full:

  - The report can no longer be computed from a bare `magus buzz` script with no
    workspace on the context: there is no longer a nested magus to fall back to.
  - The **volatility** lens has no standalone surface any more. It is a field of the
    whole report, but `magus_insight` never carried it, so an agent cannot ask for it
    alone.
  - **Per-project scoping is gone.** The subcommand defaulted to the cwd's project and
    widened with `--workspace`; both survivors are workspace-wide only.
  - The standalone Mermaid renders (`-o mermaid` for hotspots and affinity, and the
    quadrant chart) are gone with the flags that selected them. The combined report
    now always emits the PORTABLE Mermaid subset - what is left writes INSIGHT.md into
    a repository or a CI step summary, and those are the renderers that subset targets.
  - `InsightReport.graphStats` is **removed**. The CLI populated it from the knowledge
    graph it loaded for itself; nothing else ever did, so keeping the field would have
    shipped a documented axis that is structurally always empty. `magus graph stats`
    is the structural axis and is unaffected.
  - `magus\insight` takes an options map (`{commits, since}`) rather than a list
    of CLI flag strings, which is the shape the subcommand imposed on it. An unknown
    key is now an error rather than a silent default.

- **`magus\diagnoseDrift` now returns a `DriftResult` object, not `DriftVerdict`.** A
  magusfile annotating the return type has to follow. The rename settles what the word
  means across the codebase: a VERDICT is the scalar judgment, and the thing carrying one
  is named for the question it answers. `DriftVerdict` was a record of drift, not a
  judgment value, and `StagingVerdict` (internal) was four slices classifying paths.
- **Knowledge-graph schema v9.** The shard store invalidates and rebuilds on first run.
  Two bumps landed in this window and neither breaks a parser; both break a WARM STORE,
  which is what the version is for. v8 added symbol-to-symbol `calls` edges, so a v7
  consumer would read a symbol's edge set as complete when it is not. v9 added
  `secret_refs` to a target node, and its shards were extracted before the field existed
  from a magusfile that has not changed since - so nothing but the version would ever
  invalidate them.
- **`vcs\diff` is now `vcs\changedFiles`.** It returns the file paths changed against a
  base ref, not a diff, and the name said otherwise. The confusion became concrete when
  `vcs\dirtyDiff` arrived and read like a variant of it rather than a different question.
  The old name is retired rather than repurposed on purpose: host-module members are typed
  to the checker, so a magusfile still calling `vcs\diff` fails at load with a clear
  error, where reusing the name for the new meaning would have silently passed a base ref
  where a path list is expected.
- **`magus agent install-agents-md` is removed; magus no longer writes your `AGENTS.md`
  at all.** It managed a marker-delimited block inside the file - creating it when
  absent, replacing the block in place on re-run, never touching your bytes outside the
  markers. That is the careful version of an installer appending to your `.bashrc`, and
  the care is what makes the point rather than excusing it: the file belongs to the
  developer, merge logic like that is never as careful as it looks, and a re-run leaves
  bytes nobody wrote in a file nobody can easily audit. Instruct, do not mutate. Nothing
  replaces the subcommand - `magus agent install` now PRINTS the block on stderr for you
  to paste, and only when your `AGENTS.md` is missing it or carrying a stale one, so a
  `--force` reinstall does not dump 80 lines of Markdown at you every time. `magus agent
  sample` prints the same block inside a whole starter file and is never gated. Reading
  `AGENTS.md` to grade the pasted block's stamp is untouched: `magus graph verify` still
  reports it present, absent, or stale per location, because reporting is not writing.
  Two knock-on effects: `magus agent sample` now emits its magus guidance BETWEEN the
  begin/end markers rather than unmarked, so a paste from it is gradeable exactly as a
  paste from install's offer is; and this repo's own `generate` target no longer rewrites
  `AGENTS.md`, which makes that file plain hand-authored prose instead of a hybrid of
  prose and generated block.
- **`fs\mkdirall` is now `fs\mkdirAll`.** The descriptor's underlying name was one mashed
  word instead of the snake_case every other multi-word `fs` method declares, so codegen
  produced an identifier inconsistent with `fs\readFile`, `fs\removeAll`, `fs\copyFile`,
  `fs\listDir`, and `fs\appendFile`. There is no alias: a magusfile calling
  `fs\mkdirall(...)` must be updated to `fs\mkdirAll(...)`.
- **`has_charm` is now `hasCharm`, on both receivers.** `magus\hasCharm(...)` and
  `ctx.hasCharm(...)`. It was the ONLY snake_case member on either surface, sitting
  beside camelCase neighbors (`ctx.needs`, `ctx.readsFiles`, `magus\bustCache`); the
  lock file now has no underscore in it at all. The name was pinned because the static
  charm extractor in `internal/describe` matches it literally to build the charm
  inventory - that matcher moved with it, and the existing tests for both receivers and
  both arms of a charm branch are what make the rename safe rather than silent.
- **`magus\graph` is now `magus\projectGraph`.** It returns the PROJECT dependency DAG,
  and sat beside `magus\targetGraph`, which returns a different graph entirely. Named
  `graph` and `targetGraph`, the second read as a variant of the first; they are
  siblings, so each is now named for what it contains. It also settles the surface's one
  inconsistent qualifier: every other pair suffixes (`describe`/`describeFile`,
  `affected`/`affectedImpact`) while this one prefixed.
- **`magus\modules()` and `magus\module(name)` are now one `magus\describeModule(name?)`.**
  Omit the name for every module; pass one to detail it. Either way the return is a
  `[Module]`, so detailing one reads `magus\describeModule("fs")[0]`.
  The pair was a list/get CRUD split this surface uses nowhere else. `magus describe
  <noun> [<name>]` is ONE command - its own usage says "singular and plural are
  interchangeable; pass a name to detail one entity" - and the Go API underneath
  (`hostmodules.Describe`) already took an optional name and returned a slice. The
  Buzz surface now mirrors the CLI one method per command form, the way
  `magus\describeFile` mirrors `magus describe file`.
- **Logging moved to `magus\log`, and `magus\normalize` is now `magus\canonicalName`.**
  `magus\info(...)` becomes `magus\log.info(...)`, and the same for `debug`, `warn`,
  `error` and `hint`. `magus\fatal` and `magus\raise` deliberately did NOT move.
  The line is what a member DOES, not what it looks like: everything in `log` emits a
  message and returns, while `fatal` and `raise` end the run. Grouping the two together
  would let `magus\log.fatal(...)` read as one more level, which is the confusion the
  split exists to prevent. It also settles the surface's odd asymmetry - `magus\secret`
  and `magus\cache` were grouped while five logging members sat loose beside them.
  `normalize` was renamed because it named neither its input nor its output. It
  canonicalizes a magus ENTITY NAME - a target, charm, or spell op - and the doc's own
  word for the result was already "canonical": `build2` gains a `-` you did not type,
  `HTTPServer` breaks before its last letter.
  Both fail at LOAD, not at run time, because host-module members are typed to the
  checker. See `internal/interp/bindings/testdata/magus-api.lock` for the full surface
  before and after.
- **`os\exec`, `os\shell` and `os\which` moved to a new `proc` module.** Import `proc` and
  call `proc\exec(...)`, `proc\shell(...)`, `proc\which(...)`; there is no alias in `os`.
  The three were the only members of `os` that start a CHILD PROCESS, and everything left
  behind (`env`, `platform`, `exit`, `sleep`, `hostname`, `executable`, `retry`) reads or
  affects the CURRENT one. That is two different capabilities under one import, and the
  split is what lets a reader see which magusfiles spawn anything at all.
  `proc\shell` is also where the Windows branch belongs: it picks `cmd /c` over `sh -c`
  per platform, which is a fact about running a shell, not about the operating system a
  script is asking questions of.
  A magusfile still calling the old spelling fails at LOAD, because host-module members
  are typed to the checker - but note the error names the missing member rather than
  pointing at `proc`, since nothing maps retired members to their new home outside the
  `removed` table in `internal/interp/bindings/modules_test.go`. If a third such rename
  lands, that table is the thing to promote into a real migration diagnostic.

### Added

- **`magus describe file` reports a `maintained` role.** It sits between `source` and
  `unclaimed` for a path magus's own core writes outside every target's declared globs -
  `.gitattributes` is the only one today. Both halves of magus already knew this and
  disagreed out loud: `magus vcs add` reported it as a file "magus itself maintains",
  while `describe file` called it unclaimed and advised checking the ignore rules, for a
  file magus had just written and needs tracked. The advice was worse than cosmetic,
  because acting on it drops magus's own merge-driver registration. `magus\describeFile`
  and `magus_describe_file` carry the new value, and the PR advisor that lists unclaimed
  files stops naming it. It is a refinement of `unclaimed`, never a rank above `source`:
  a workspace that genuinely declares one of these paths still reports it as declared.
- **A built symbol index no longer changes the committed graph.** A SCIP index is cache
  state - gitignored, per-worktree, present only where the `scip` op has run - but two
  aggregate shards folded its paths into the DEFAULT graph: `@dirs` minted a dir node per
  symbol directory, and `@io` minted produces/consumes edges for symbol files. So
  `MAGUS.md` and `gen/knowledge-graph.json` differed between a developer who had run
  `magus graph build` and CI, which never does, and the drift gate fired on the difference.
  The `@io` half was worse than nondeterministic: those edges sat in the default graph
  while their target file nodes did not, so the committed graph carried 138 references to
  nodes it does not contain. Both now stay in the per-project `@symbols` shards, so an edge
  and its endpoint appear together or not at all.
- **The knowledge graph now has a call graph.** SCIP records an enclosing range for each
  definition, so a reference occurrence inside one was written in that definition's body
  and the enclosing symbol is the caller. `magus explain symbol:X` gains "calls" and
  "called by", and `magus query relation:calls` reaches them. Nothing new is parsed and no
  indexer re-runs: the ranges were already in the indexes magus caches and were being
  discarded.

  Two restrictions keep the relation truthful rather than merely large. The callee must be
  callable - an enclosing range spans the whole declaration, signature included, so
  measured over this repo's own index only 26.4% of the occurrences inside one are calls
  and the rest are struct fields, types, and packages. Callability comes from the moniker's
  SCIP descriptor suffix rather than the optional `SymbolInformation.Kind`, so it holds for
  any indexer: scip-typescript populates no kinds at all, and a kind-based rule would have
  produced no calls for TypeScript at all while looking like it worked. And the callee must be defined in
  this workspace, since a call into a dependency has no body to navigate to and its usage
  is already on the referencing file's `references` edge. Together those took the symbol
  shards from a projected 2.35x to a measured 1.18x.
- **Every empty graph answer says which kind of empty it is.** `magus query`,
  `magus explain`, and `magus refs` carry an `answer.verdict`: `absent` is a fact magus
  verified, `unknown` names the projects it could not search and the command that fixes
  them. Previously both printed the same thing, so a missing symbol index was
  indistinguishable from a symbol that does not exist - and an agent recorded the blind
  spot as a fact. `refs` and `explain` now exit 1 on `unknown` and keep 2 for `absent`;
  `magus query` still exits 0 either way, because an empty result set is a legitimate
  answer to a search.
- **Symbols carry a language even when the indexer does not report one.** SCIP makes
  `Document.Language` optional and scip-typescript sets it on nothing, so every TypeScript
  symbol landed unlabeled and `magus query language:typescript` returned 0 while
  `language:go` returned 22,245. magus now falls back to the language the producing spell
  declares - the same declaration that made the project symbol-capable in the first place.
  A document that names its own language still wins, since one index may span several.
- **`magus insight unreferenced`**, a sixth lens: code symbols the workspace defines and
  nothing in it names. It reads the knowledge graph rather than git, so it takes no
  history window. The output carries the coverage verdict above, which is the point - a
  project whose symbol index was never built contributes no symbols, so without it the
  lens would be most reassuring exactly where it knows least. Candidates for review, never
  a delete list: reflection, interface dispatch, build tags, and consumers outside the
  workspace are all invisible to a static index.

- **`vcs\dirtyDiff([paths])` returns the working tree's uncommitted changes as text**, on
  every backend. Drift gates previously branched on `vcs\name() == "git"` and shelled out
  through `vcs\cmd`, so an hg or jj user got filenames with no diff. git, hg, and jj now
  each implement it and every one of those branches is gone.
- **Markdown is formatted by dprint.** The workspace had no markdown formatter after
  prettier was dropped. Each project carries its own `dprint.json` extending a shared base,
  the way `biome.json` already does, and the markdown spell exposes a `dprint` op.

- **A GitHub Actions secret provider**, as a third contract on the existing
  `spells/github/actions` spell alongside the cache backend and the CI provider. Select
  it with `magus\secret.provider(github)` under Actions. It exists because an Actions
  secret is write-only - nothing inside a job can fetch one - so it does the two things
  the platform-neutral built-in provider structurally cannot. An `oidc:<audience>`
  reference mints a short-lived token from the runner's own endpoint, which is the only
  credential on a runner that is genuinely fetched rather than injected and is what lets
  a repository hold no long-lived cloud key; it requires `permissions: id-token: write`
  and says so when that is missing. A bare reference reads the injected variable,
  registers the value with the runner via `::add-mask::` so every later step's log is
  masked too, and on a miss prints the exact `env:` block to paste instead of reporting
  an unset variable. The OIDC request refuses a non-HTTPS endpoint, because an earlier
  step can rewrite a job's environment through `$GITHUB_ENV` and the request token is
  sent to whatever the endpoint names.

- **Man pages for the three knowledge-graph verbs.** `query`, `explain`, and `path` had no
  entry in the man-page registry, so none of them appeared in `docs/reference/manpage/`,
  in the "See Also" list every other page carries, or in the man pages the binary ships.
  `path` in particular - the verb that answers "how are these two things related at all",
  which neither of the others can - was effectively undiscoverable unless you already knew
  it existed.
- **`magus query invocation <id>`**, a reader for the run journal. magus recorded a
  `secret` event for every credential a run resolved - reference and provider, never the
  value - and `docs/concepts/secrets.md` offered that as the answer to "which credentials
  did this run touch". Nothing could read it: the invocation id magus prints as `inv:`
  resolved to `matches: 0` against the graph grammar, so the audit trail was a claim with
  no reader. `--secrets` narrows the stream to the credential reads; `-o json` emits the
  record. Pasting a bare invocation id into `magus query` now says which command reads it
  instead of reporting an empty search.

- **Failure advice on a command op.** A spell declares
  `hints = [Hint{contains = "...", advise = "..."}]`; when the command exits non-zero and
  `contains` appears in its output, magus prints the advice through the run's own stderr,
  so it lands in the log and the output ref beside the tool's error rather than only on a
  terminal. Each stream is matched independently and only a real non-zero exit qualifies -
  a cancelled run advises nothing. The bundled spells declare it where a tool's message
  names a symptom rather than the fix: `docker-buildx` for the registry-auth and daemon
  phrasings of BOTH docker and podman (podman commonly arrives through the `podman-docker`
  shim, and the registry messages come from the OCI registry rather than from either tool,
  so those are shared); the `go` build/run/test ops for `missing go.sum entry` and
  `updates to go.mod needed`; every JS op for a stale install, covering pnpm, npm, yarn and
  bun because each invented its own phrasing for the same fact; and `cargo-clippy` for a
  missing rustup component. Hints are refused on service ops, where they could never fire.
- **MGS1026: a cacheable target that reads a credential.** A resolved credential is
  deliberately not part of the cache key, so rotating or revoking one invalidates nothing -
  and an authentication target, whose sources almost never change, then becomes a permanent
  cache hit that never contacts the provider and still reports success. The push that
  follows fails with the registry's own 401, far from the cause. A doctor check now reports
  the combination; `skip_cache` with a reason is the fix, and magus's own `image-login`
  already declares it.

### Changed

- **The `-login` convention is demoted from "the convention" to a specific tool.** It was
  presented as the way to authenticate, and it is a target with no inputs, no output, and
  no cacheability - a mode switch rather than a unit of work, and the shape that produced
  the MGS1026 hazard above. The documented default is now to authenticate when the tool
  says to, since the re-run replays from cache and costs seconds. A `-login` target remains
  the right answer for several registries at once, and for unattended runners where there
  is no human to read a failure.

### Fixed

- **A shared cache dir no longer merges two workspaces' project locks.** An absolute
  `cache.dir` (or `MAGUS_CACHE_DIR`) resolves to the same path for every root - that is
  the point, one cache - but the lock tree hung off it directly, so every workspace's
  project `.` was the same lock file. An unrelated checkout then blocked on this one,
  and because the holder is a legitimate live process it presented as an indefinite
  wait rather than an error. Locks now live under `<cacheDir>/locks/<workspace>/`, and
  `magus status` reports only the current workspace's holders instead of prefixing every
  project path with the workspace segment.

- **A re-entrant lock in a library caller hangs instead of reporting MGS3007.** The
  diagnostic exists for exactly this - a lock held by one of your own ancestors can
  never be released - but invocation ancestry was stamped only at the CLI and daemon
  entry points. A Go test driving magus in-process had none, so the check could not
  fire and the acquire waited forever with the ancestry env var sitting unread in its
  own environment. The lock boundary now reads it when nothing upstream supplied one.

- **The root `format` target no longer writes into descendant projects.** dprint
  discovers a nested `dprint.json` and formats that subtree under its own config, and
  neither the parent's `includes` nor an explicit `--config` prunes it: a bare run from
  the workspace root reached 164 files, 143 of them under `docs/`. That is a target
  writing outside its project, which magus itself rejects as MGS3001, and it stayed
  invisible while every child project's markdown happened to already be formatted.
  Passing paths as argv scopes it (10 files, none in a child project), so the root call
  now does. `dprint.json` had recorded this as unfixable; only the config route was ever
  tested.
- **Breaking, and silent until now: Buzz's `str.replace` substituted only the FIRST
  occurrence.** Upstream Buzz replaces every one - `src/builtin/str.zig` hands the whole
  string to Zig's `std.mem.replaceOwned` - so this was a gopherbuzz conformance bug, not
  the upstream parity it was documented as. It was invisible because the callers that
  care are escapers, which produce output that is merely wrong rather than failing: the
  docs Atom feed and HTML escapers encoded only the first `&` of a document, the GitHub
  Actions workflow-command escaper encoded only the first newline and let the runner end
  the command at the second, and `slugifyProject` kept every separator after the first. A
  test comment and a project note both asserted the old behavior was correct, which is
  why it outlived review. `libs/gopherbuzz/testdata/66_str_replace_all.buzz` now pins the
  upstream shape. **Do not add a `str.replaceAll`** - upstream has no such method, and
  adding one would make gopherbuzz a superset whose scripts fail upstream silently.
  (`pat.replace` and `pat.replaceAll` remain a correct pair and are unchanged.)
- **Escaper tests now use multi-occurrence fixtures.** Every escaper in the tree was
  tested with exactly one of each character, which is the shape that hid the bug above -
  thoroughness was being measured across characters rather than across repetitions.
- **`magus run buzz-test` now runs the spell and docs-library test blocks.** 142 in-file
  `test "..." {}` blocks now execute in CI, up from 21. The rest were written, passing,
  and invoked by nothing.

### Changed

- **Generated-file drift is measured by content, not by asking whether the tree is clean.**
  Every gate hashed nothing and instead required a clean tree, which disarmed it at exactly
  the moment it was reached: the documented pre-push check runs with uncommitted work in
  the tree, so it printed "skipped" and exited 0. Gates now hash their paths before and
  after the generators run, so an unrelated edit no longer hides drift, and an
  uncommitted-but-current generated file still passes because its bytes do not move.
- **Go formatting is gated by golangci-lint's `formatters` section.** `gofmt -l` reports on
  stdout and exits 0, and magus reads an op's verdict from the exit code, so unformatted Go
  passed green. The `format` target keeps `gofmt -l` as the local reporter.
- **MGS4003 fails the run instead of warning.** Determinism is what drift gating, cache
  replay, and regenerate-to-resolve merges all rest on, so there is no useful "warned about
  it" state. `--race=replay` is now run weekly by `audit.yaml`, renamed from `nightly.yaml`
  because a workflow should be named for its purpose rather than its cadence.
- **`MAGUS.md`'s routing anchors no longer depend on local run history.** The `@runtime`
  shard records which diagnostics a machine happened to trip, and those edges fed the
  degree ranking, so a committed generated file differed between a developer's machine and
  CI.


- **`magus-delegate-ultra` can now be reached by asking for it in plain words.** It had two
  triggers: its own literal name, and "explicitly requests graph-planned parallel
  delegation" - a phrase nobody says out loud, so in practice it had one. It now also
  matches how people actually ask ("fan this out", "run these in parallel", "use several
  subagents") while keeping the opt-in sharp: wanting the work faster, sooner, or more
  thorough is explicitly NOT that request, because those are asks about the outcome and
  this skill is a choice about the method, with a real cost. The name is unchanged
  deliberately - it is the trigger string, and `-ultra` already means "expensive,
  explicitly requested" across this toolchain.
- **`--simple` now sheds enumeration and keeps judgment, having previously done the
  reverse.** It described itself as "the imperative steps without the rationale, for a
  reader that infers the why" - which is backwards for who actually installs it. The
  short permutation is for the most capable readers, and those are precisely the readers
  that can re-derive a step from `-h` or `magus describe` but cannot re-derive which
  failures are SILENT. It was handing its strongest reader the half it could have
  reconstructed and taking away the half it could not. Twelve skill bodies were
  re-cut against the new axis, so the short permutation now carries, in compressed form,
  the reasons a whole-tree revert destroys a concurrent agent's work, that a merge driver
  cannot finish a conflict alone, that a silent fallback hides the gap worth reporting,
  and that a partial inventory is a wrong answer wearing a right answer's shape. Several
  load-bearing imperatives turned out to be marked full-only and were reaching only half
  the readers; they are unconditional now. The authoring skill and the flag's own help
  carry the corrected framing. Skill contract v25. The old key named the
  absence of a behavior, so answering "can this run write?" meant parsing a double
  negative, and the documented CI snippet read inverted from its own intent:
  `MAGUS_CACHE_IMMUTABLE: ${{ github.event_name == 'pull_request' }}` becomes
  `MAGUS_CACHE_WRITE_ENABLED: ${{ github.event_name != 'pull_request' }}`. It gates the
  local snapshot and the remote push alike; restoring is still ungated, so a pull
  request replays the shared cache at full speed while publishing nothing to it.
- **The host platform now keys the cache as separate `os:` and `arch:` lines**, each
  controlled by `cache.include.os.enabled` and `cache.include.arch.enabled`. They vary
  independently - a container image built on linux/amd64 differs from linux/arm64 by
  arch alone, a shell suite differs between macOS and linux by OS alone - so one
  combined switch made a workspace that cared about one pay for both. This replaces the
  per-target `platform` policy.

### Fixed

- **A too-new tool reported MGS3005, "older than this spell supports".** The version gate
  took a single constraint string, so one error covered every way of violating it. Two
  named bounds make the direction structural: below the minimum is MGS3005, at or above
  the ceiling is MGS3006.

- **A copied hook template can now be checked for staleness.** Every shipped template
  carries a `magus-guard-template` version line, and the guide says how to compare it
  against your own copy. It fills the one gap in the agent surface where a fix could not
  reach its users: an installed skill is generated and regraded by `magus graph verify`,
  but a hook template is copied into a host's config and owned by its reader from then on,
  so the exit-code fix below would have been documented within the hour and absent from
  every installed copy indefinitely. A version rather than a checksum, because these files
  are explicitly yours to edit and a checksum would flag your own changes as drift. A bump
  is total: a test fails until every template is re-stamped, so no file is left claiming a
  version whose behavior it does not have.
- **The shipped guard templates mishandled a denied command's non-zero exit.** A deny now
  exits 2 with the verdict still on stdout, which the templates read as "this binary
  rejected the attribution flags": the two generic templates silently judged every blocked
  command a second time, unattributed and recorded twice in the activity trail, and the
  Cursor script let the blocking status escape as its own, which Cursor reads as a crashed
  hook and fails open on - turning every block into an allow. They now retry only when a
  call produced no verdict at all, and the Cursor script prints its JSON and exits 0,
  because Cursor's channel is that JSON rather than the status. Found by the new transport
  cases on the first run after the exit-code change landed; nothing had executed these
  files before.
- **The OpenCode guard plugin never obtained a verdict, so OpenCode sessions ran
  entirely unguarded.** It invoked `magus agent hook`, a subcommand that stopped
  existing when the guard moved to the top-level `magus hook`, and passed the command
  or path as a positional argument when `hook` reads its input from stdin and rejects
  positionals. Both were invisible: the plugin ignores the child's stderr, so the
  usage text went nowhere, an empty stdout failed to parse, and its fail-open arm
  logged "verdict was not JSON; allowing" once per tool call and allowed everything.
  It now calls `hook` and writes the input to the child's stdin. The parity check
  that found this one deliberately does not cover it - a glue that handles verdicts
  correctly but never receives one is a transport failure, and nothing executes the
  templates against a real event yet.
- **A defined type over a basic kind crossed into Buzz as `null`.** Now guarded by a
  test that crosses every runtime boundary type, with the list generated from the same
  registry that emits the Buzz mirrors - so a new boundary type is covered when it is
  declared rather than when someone remembers. A type switch matches
  on type identity, not underlying type, so a field typed `types.DoctorCheckStatus` or
  `types.TargetRunState` matched no case and arrived as null - `doctor().checks[0].status`
  read null rather than `"ok"`, while the SDK guidance told callers to branch on exactly
  that field instead of grepping console text. Handled reflectively now, so the next
  defined type does not reintroduce it.

### Added

- **A supported version window per tool, checked against the binary that actually ran.**
  A spell declares what its ops need with `supported = VersionBounds{min = "1.21"}`, a
  workspace declares its own policy with `magus.project({"tools": {"node": {"min": "22",
  "below": "25"}}})`, and the two intersect so neither can loosen the other. Outside the
  window fails before the run does any work, as MGS3005 (below the minimum) or MGS3006 (at
  or above the ceiling); enforcement follows the declaration, so a project whose targets
  shell out instead of dispatching a spell op is held to its window all the same. The
  version already fed the cache key, so the probe was running on every build regardless;
  this compares its result against something you declared. `min`
  is inclusive and `below` is exclusive, both plain versions rather than a constraint
  range, because a range language puts a syntax between you and the two cases that
  matter. magus never learns which versions exist upstream and never selects one.
- **`magus describe tool[s]`, and a Toolchain tile in the console.** Until now you could
  only see the window a build is held to by failing one, and the diagnostic named the tool
  that broke the rule without listing the rest. Both surfaces read the same state - the
  probed version, the spell's `supported`, the project's `tools` key - and report the
  verdict the CLI would raise for the same pair, so a page and a terminal cannot disagree.
  The spell's window and the workspace's stay separate columns: the first question about a
  failing bound is who set it, and the intersection has already thrown that away by the
  time a diagnostic exists. The console reads it over a new `magus.tool.v1` service. A
  probe forks a process, so the daemon caches each reading for a minute and every row
  carries its age instead of implying it is live. magus still never learns which versions
  exist upstream, never selects one, and carries no end-of-life data.
- **`opts.quiet` on `proc\exec`, `proc\shell`, and `vcs\cmd`.** Captures output without
  echoing it, matching what `magus\cmd` and friends already accepted. Read in the one
  path all three share, so they cannot drift into different option sets.
- **A doctor check that the declared `required_version` covers the magusfile keys in
  use.** An unknown `magus.project` key aborts workspace load, which takes down every
  command including the one that would build a binary new enough to read the file.
  `required_version` converts that into MGS1021, but only if somebody remembers to raise
  it; this asserts it instead. An unrecognized key with no near match now also suggests
  `magus self update`, for the binaries too old to evaluate a floor at all.

- **A workspace can carry its own magus rules, in a skill magus does not ship.** The
  installed skills teach the tool and are identical in every repo, so a rule that is
  true only here had nowhere to live: editing an installed copy reads as drift to
  `magus graph verify` and is erased by the next `magus agent install --force`, and
  nothing said so at the moment of the edit. A local skill beside the installed set
  (`magus-local-development` by convention) was already safe by construction - install
  writes only the names it ships and verify grades only those - so this release makes
  the convention discoverable rather than building a mechanism: a new `magus-adapt`
  skill carrying the method and the per-rule stamp format (evidence, and the condition
  that retires the rule), the name reserved against a future shipped skill, and a
  `magus hook --path` advisory that fires when an agent is about to edit a stamped
  install. Skill contract v24.
- **The reserved local-skill name is `magus-local-development`, not `magus-local`.**
  The convention had not shipped in a release yet, so there was no compatibility
  burden to carry: `LocalSkillName`, the `magus-adapt` skill body, the `.gitignore`
  exception, and this workspace's own `.claude/skills/magus-local-development/` moved
  together, outright, with no alias. Skill contract v27.
- **A `magus-buzz-review` skill**, three lenses over a magusfile, spell, or standalone
  `.buzz` script - idiom/style, skeptic/correctness, and upstream-Buzz conformance -
  fanned out in parallel and merged, the same shape go-review-ultra already uses for
  Go. magus-buzz teaches how to write Buzz; nothing taught how to review it, so a
  gopherbuzz-only behavior (namespace access accepting a dot as well as a backslash,
  a bare `as` cast coercing instead of statically checking, a compound assignment
  double-evaluating its target) had no home to be flagged from, and a strict-mode rule
  applied to a magusfile - which is always parsed embedded, unconditionally - read as a
  real finding when it was a false one. Every finding carries one of three authority
  labels (UPSTREAM, GOPHERBUZZ, PORTABILITY) naming which of those three questions it
  answers. It does not cover magusfile/target/spell contracts; magus-buzz still owns
  those. Skill contract v28.
- **`magus-buzz` no longer tells an agent that `test` is a reserved word.** The
  shipped skill's "reserved words" table conflated parse-time reservation with
  runtime shadowing, and was wrong on two entries: `test` is not in gopherbuzz's
  `reservedIdents`, deliberately, because every magus target set defines
  `export fun test(...)`, the canonical test target - the old text told an agent
  that target was illegal. `map` was never reserved either; naming a local or a
  field `map` shadows the builtin `.map()` method, which is a runtime hazard, not
  a parse error, and is why it fails later with a confusing `null is not
  callable`. The passage now states the real reserved list, states plainly that
  `test` is usable, and moves the shadowing hazard to its own line. Skill
  contract v29.
- **`magus-buzz` and `magus-buzz-review` now teach the magusfile testing
  boundary.** `docs/guides/testing.md` already said not to write tests for a
  magusfile - it is declarative configuration, and wanting one is the signal
  to move that logic into a spell or a sibling module - but the shipped skill
  never said so, so an agent following it would happily test a magusfile.
  magus-buzz now states the boundary and the `--embedded` flag a magusfile's
  own imported module needs when tested (its imports parse embedded, not
  strict); the review skill's idiom lens adds the one-line finding and points
  back rather than restating it. Skill contract v30.
- **`magus-buzz-review` now records that a declared `!>` error set is
  unenforced.** Upstream Buzz treats `!> ErrType` as a real error set;
  gopherbuzz's parser consumes the annotation and calls skipType, and no AST
  node stores it, so a function declaring a raise that throws compiles clean
  when called with no try/catch from a function declaring none. The
  upstream-conformance lens now says to read a `!>` as documentation rather than
  a checked contract, and not to take its absence as proof a call cannot raise.
  Skill contract v31.
- **Host parity is now a build gate rather than a table nobody re-reads.** Each guard
  template declares, per guard surface, how much of a verdict it can carry
  (`magus-guard-coverage`), and the guard's own vocabulary moved into an importable
  contract. Adding a decision kind or a guard surface without wiring every host now
  fails `go test`, as does a declaration that disagrees with the parity table in the
  agents guide. A declaration can also be sincere and wrong, so the templates are now
  EXECUTED as well: a testscript corpus runs the three POSIX sh templates against real
  host events with a real binary, and the OpenCode plugin's transport cases run under
  node with `Bun.spawn` supplied by the test, leaving the shipped artifact untouched.
  Both are tied back to the contract - a new decision or surface fails until an
  executed case covers it, or the file says in writing why that cell is unreachable.
  The honest remaining limit: the recorded event shapes come from each host's
  documentation, so a host renaming a field is still invisible until someone runs it.
- **Tool readiness probes.** A spell can declare `mgs_getReadinessProbes`, keyed by tool,
  and magus checks it before dispatching an op that runs that tool. `docker --version` is
  client-only and succeeds with no daemon, so a stopped daemon used to surface as a build
  failure on a project with nothing wrong with it; it now fails as MGS3004 before the op
  forks. Readiness never enters a cache key - it is a precondition, not an input.

### Changed

- **`semver\compare` now orders instead of testing a relation.** It was
  `compare(a, op, b) > bool`, answering whether a relation held. Every other library
  spells `compare` as three-way ordering returning an integer - Go's `cmp.Compare` and
  `strings.Compare`, `x/mod/semver.Compare`, Masterminds, node-semver - so the old
  signature was a trap that compiled: an author expecting an ordering got a boolean.
  It is now `compare(a, b) > int`, returning -1, 0, or 1. The relation form moves to the
  new `semver\satisfies(v, constraint)`, which also accepts ranges the operator form
  could not express, so `semver\compare(v, ">=", floor)` becomes
  `semver\satisfies(v, ">= " + floor)`.

- An output reference is now derived from the step's cache key, so the same inputs mint
  the same ref on every machine: an inspect line pasted from CI or a teammate's terminal
  resolves in your checkout. Ref equality becomes input equality, which is what makes the
  works-on-my-machine question answerable at all - if CI prints one ref and your laptop
  prints another for the same target, your inputs differ, and magus can now say which
  ones. **A ref is `out` plus 12 hex (`out9c92fef96e60`) where it was `out` plus 8**, so a
  script, fixture, or pattern that pinned the old width needs updating. Execution identity
  moved down a level to per-run ATTEMPT ids, which keep the 8-hex shape - a volatile
  target's recent failures each stay independently addressable, and an id printed by an
  older magus still resolves.

### Added

- `magus query output <ref> --attempts` lists the executions stored behind one ref, newest
  first, and `--meta` shows that run's identity rather than its output: descriptor,
  invocation lineage, cache key, and one digest per key component class.
- `magus describe target <target> --cache` computes the key a run would mint right now,
  without running anything, and `--against <ref>` diffs it against a stored run's key to
  name the exact source file, environment variable, or tool version that drifted. The
  verdict is key equality rather than the line list, and a mismatch exits non-zero so a
  script can gate on it; pass `--no-default-charms` when comparing against a CI ref, since
  CI runs that way. Env values never reach the store or the terminal - a key input's value
  is replaced by a short digest that still changes when the value does.
- `magus query output <ref> --publish` uploads a failing run's output to the remote cache
  as a signed bundle, so a teammate can resolve the same ref. Failures are never cached and
  never pushed, which is backwards from what people actually want to share, so this is an
  explicit act. A bundle carries no manifest and no artifact blobs, so a published failure
  can never replay as someone's cache hit. Passing runs still travel automatically, and
  their artifact now carries the run's descriptor and key inputs as well.
- An unresolvable `magus query output <ref>` (MGS8001) is no longer a dead end. magus
  sweeps every candidate target in the workspace, keys each exactly as a run would, and
  compares against the ref - the same prediction `describe target --cache` already does
  for one target on demand. One match prints the exact `magus run <target> [project]`
  that would reproduce it; no match says plainly that the run which printed it had
  different inputs (a different commit, an uncommitted change, or an environment), which
  is a finding, not a failed lookup. `--base` plays no part in either case - it scopes
  which targets `affected` treats as changed, not what a target hashes to. `--meta` also
  gains a `rev:` line: the VCS revision the run's inputs were read at, with a `(dirty:
  ...)` note and a `recorded at X, you are on Y.` callout when it differs from HEAD - the
  key pins a tree state, never a commit, so this is provenance, not something to check
  out. A ref minted by a run that forwarded extra arguments after `--` still cannot be
  predicted, since those arguments are part of the key and a prediction has none.

### Security

- The remote cache artifact's signature covers every member instead of the manifest alone:
  the build log and the portable-ref sidecars are authenticated, imported extras are staged
  until the signature clears so a rejected artifact leaves nothing behind, and a signature
  is bound both to the KIND of object it was made over and to the (project, cache key) it
  is served for. Without that binding a signed output bundle could be re-tarred as a cache
  artifact and replay as a successful entry, turning a published failing run into a
  teammate's cached pass. Artifacts signed by an older magus still verify; the extras their
  signature never covered are dropped rather than trusted.

### Added

- Host module calls are typechecked. Every method a host module declares now ships a Buzz
  `extern` signature alongside its implementation, so `magus\affectedImpact(base)` types as
  `Impact` at the call site instead of as an unknown, and reading a field the return does not
  carry is a load-time error rather than a runtime surprise. The declarations are generated
  from the same `std.Module` descriptors the runtime binds, so a signature cannot drift from
  what executes. Two methods stay untyped and say so in the generated output: `fs\join` and
  `fmt\sprintf` are variadic, and Buzz has no variadic parameter to declare.

- A magusfile can read a credential through a declared provider.
  `magus\secret.provider("<spell>")` selects the backend and `magus\secret.read("<ref>")`
  reads one reference. Where a secret comes from is a spell's problem, so 1Password, Vault,
  or AWS Secrets Manager are an `proc\exec` away and magus grows no per-provider code; with no
  provider declared, the built-in one treats a reference as an environment variable name.
  A value is a secret because it was read through the resolver, never because its name
  looked credential-shaped, so magus can keep it out of what it persists: the captured
  output, the raw log, the output store, the journal, and every log format are redacted at
  their write boundary. See [docs/concepts/secrets.md](concepts/secrets.md), which is
  also explicit that this reduces blast radius and dwell time versus a `.env` file and does
  not make anything "secure".
- Container images are published with an SBOM and provenance, to two registries in one
  build. Each variant now carries an SPDX SBOM and max-mode SLSA provenance as in-toto
  attestations, and a single buildx invocation per variant pushes to GHCR and Docker Hub
  together - not one build per registry, which on a cold CI runner would rebuild every
  layer. Merges to main publish a per-commit snapshot image. `image-registries` reports the
  registry table the active charms resolve to, and `image-login` authenticates against it.
- `magus.project` accepts `"no_language"`, a REASON string explaining why a project binds
  no toolchain spell. It silences doctor's language-coverage check for a project that is
  legitimately polyglot (the `evals` harness is the in-repo case) without inviting the
  check to be switched off wholesale. A bare `true` is rejected: the reason is the point.
- [MGS1020](reference/codes/magusfile/MGS1020.md) reports a generated file claimed as
  an output by more than one target, and documents the one-owner rule for generated files.
- `magus doctor` findings come at two levels, and the split is a correction. `[fail]` is a
  workspace that is wrong however you like to work: a dependency cycle, an unparsable
  magusfile, two targets claiming one output. `[advice]` is a convention magus recommends -
  target naming, language coverage, spell doc comments - which is reported and exits zero,
  because `ci` is the one target magus reserves and the rest of the layout belongs to
  whoever wrote it. Previously a convention check could only fail or not exist, so each one
  grew its own escape hatch (`no_language`, and briefly `allow_bespoke_name`): the config
  surface was accumulating one key per opinion, and taking magus's advice was mandatory
  unless you wrote a paragraph explaining yourself. There is deliberately no flag that
  promotes advice back to failure; that would be the same imposition with an opt-in label.

- A workspace can declare the oldest magus that can run it. `required_version` in
  `magus.yaml` takes a semver constraint, is checked before any magusfile is evaluated, and
  reports [MGS1021](reference/codes/magusfile/MGS1021.md) naming both fixes (upgrade
  the binary, or raise the pinned version in CI). It has to be declared rather than derived
  because the binary that hits the problem is the OLD one: it cannot look up which release
  added the module it is missing, having never heard of that release. Without it, a too-old
  magus fails from wherever the magusfile first touched something it lacks -
  `import "xml": module not found`, which reads like a typo. See
  [docs/concepts/compatibility.md](concepts/compatibility.md), which states what magus
  promises across versions and why there is no plan for a 2.0.

### Fixed

- The console's service worker stops serving a stale bundle indefinitely. Its `BUILD_ID`
  named the cache and was hand-written, though the comment beside it claimed the build
  bumped it, so a rebuilt console produced a byte-identical `sw.js`, the browser found no
  update, and every client stayed pinned to the shell it first cached. The refresh prompt
  downstream was never reached because nothing upstream ever fired. `BUILD_ID` is now a
  digest of the bytes it precaches, and a tab re-checks for a new worker on boot and every
  15 minutes, so an unattended display is not left on a build from days ago.
- A `magus doctor` finding names the file it found. Details rendered their path with
  `filepath.Rel` against the runner's root and discarded the error, but the root is empty
  on the path the daemon takes - so `Rel` failed and the detail printed an empty path,
  reporting a target name as wrong without saying which magusfile declared it. It now falls
  back to the workspace root, then to the absolute path.
- `magus doctor` reports a bespoke phase-fragment target name ([MGS1003](reference/codes/magusfile/MGS1003.md))
  once per project instead of once per name. Collapsing every project onto the first
  magusfile scanned meant a workspace with three of them showed one, and fixing that one
  surfaced the next - the check could not say how much work was left.
- The container images build again. Both Dockerfiles copied only `go.mod` and `go.sum`
  before `go mod download`, but the root `go.mod` `replace`s two in-repo modules and the
  download reads each replacement's own `go.mod` to build the module graph. It failed with
  `reading libs/<name>/go.mod: no such file or directory`, which meant no container image
  had ever been published: the failure only fires on a `v*` tag, so every release attempt
  died at the same step. The manifests are now copied before the download, which keeps that
  layer cacheable on the manifests alone.
- The `_static` release archives and the `latest` container image are now actually static.
  Buzz's FFI provider reaches `dlopen` through purego's `//go:cgo_import_dynamic`, which
  gives the binary a `PT_INTERP` and a `libc`/`libdl`/`libpthread` dependency even under
  `CGO_ENABLED=0`. The archive advertised as static therefore needed a dynamic loader and
  would not run on a musl or scratch host, and the `distroless/static` image could not exec
  its own binary at all, reporting only `exec /magus: no such file or directory`. Both now
  build with `-tags noffi` (see Changed).
- The sandbox no longer denies a write into a directory the run has yet to create.
  A non-existent write target is normalized by resolving its parent, but when that
  parent was missing too the whole path stayed lexical, so a symlink anywhere above
  it went unresolved and could never match a rule path (which IS resolved). Any
  workspace under a symlinked prefix - on macOS that is every path under `/var` or
  `/tmp` - had nested creates denied. It now walks up to the nearest ancestor that
  exists and re-attaches the missing tail.
- magus no longer panics mid-run on a target that fans out. `captureRun` puts one
  pair of output taps on the context for a whole target body, and `ctx.needs(lint,
  test)` - the shape of every `ci` target - runs its children concurrently, so
  several goroutines reached the same tap. Its line buffer was unguarded, so two
  writers tore the slice header and the process died with `slice bounds out of
  range` inside `lineTap.Write`. The panic killed the writer goroutine, after which
  the child process reported its broken output pipe as `exit -1` - surfacing as an
  unrelated-looking tool failure rather than as a crash. The shared log sink beside
  it already had the equivalent guard.

### Changed

- `magus status -o json` spells the `build_info` keys in lowercase (`version`, `commit`,
  `date`) rather than capitalized. The struct carried no tags, so it was the one object in
  an otherwise snake_case payload that echoed Go field names. A script reading
  `.build_info.Version` must read `.build_info.version`. YAML output is unchanged, and the
  console reads this over protobuf rather than JSON, so it is unaffected.
- `magus affected --impact -o json` always emits `coverage` on a changed symbol, and
  `magus insight report -o json` always emits `volatility`. Both were pointers that
  disappeared when absent; they are values now, so a magusfile reads `sym.coverage.ratio`
  and `report.volatility.targets` without a nil guard and the Buzz mirror can declare them
  non-optional. A consumer testing for key presence should test the counts instead:
  `total_stmts` of 0 means no coverage was observed, an empty `targets` means no
  run-outcome history.

- Breaking: `vcs.shortHash`, `vcs.hash`, `vcs.branch`, `vcs.commitDate` and `vcs.commit`
  now RAISE when no VCS is resolved or its metadata cannot be read. They used to swallow
  the failure and hand back `""` (or, for `commit`, an object with every field empty), and
  the module reference told you to test `c.date == ""` to find out.

  That is not how a Buzz function reports a problem - upstream declares the error in the
  signature and the caller writes try/catch - and the sentinel could not even be trusted:
  `""` is a value a branch name or a subject line can legitimately hold, so the check could
  not distinguish "no answer" from "the answer is empty". It also made the check optional,
  and a magusfile that forgot it interpolated an empty commit into a version string or an
  image tag with nothing to surface the mistake.

  Migration, where a missing VCS is a real case (building from a release tarball or a
  container context):

  ```buzz
  // before
  final c = vcs\shortHash();
  if (c == "") { return "unknown"; }
  return c;

  // after
  try { return vcs\shortHash(); } catch (e) { return "unknown"; }
  ```

  `vcs.name()` still returns `""` when nothing is resolved, and remains the way to TEST for
  a VCS before asking it anything - the same split as `os.env` and `os.lookupEnv`.

- Breaking: container images are signed with cosign v3, so **verifying one needs a v3
  client**. A v3 client reads both formats; a v2 client cannot read a v3 signature and
  reports the image as unverified, which is indistinguishable from a bad signature. Run
  `cosign version` before treating a failure as a compromised image.

  Taken now, deliberately, rather than announced later: no release has been published yet,
  so nobody is verifying these images with a pinned v2 client. Doing it after a release
  would have flipped `latest` under readers who never opted in, and the guide tells them a
  verification failure means "not an official build - do not run it".

  It also unblocks the toolchain. cosign's own 2.x releases do not publish the
  `cosign_checksums.txt.sigstore.json` that aqua verifies against, so no 2.x version could
  be installed through the pinned toolchain at all - `mise install` failed outright, in
  every CI job that runs it rather than only the signing one.

- Breaking: `skip_cache` now requires a REASON string; the bare `true` form no longer
  loads. `"skip_cache": true` was a flag that recorded a decision and threw away why it was
  made, so a target opted out of caching in 2025 looked identical to one opted out by
  accident, and six stale opt-outs survived in this repo alone because nobody could tell
  which were still load-bearing. Write the reason instead:

  ```buzz
  "targets": {
      "release-sign": {"skip_cache": "signs the manifest per invocation; a replayed signature would cover different bytes"},
  },
  ```

  A magusfile with the old form fails to load and names the target. This is a per-target
  policy about a target that must never replay; it is NOT the way to skip the cache for one
  run - `--no-cache` on the command line is a session-level judgment and stays where it is.
  `docs/concepts/cache.md` covers the distinction and the mapping from Nx's `cache: false`.

- Breaking: the release archives now name the static build with a `_static` suffix and the
  dynamic build with the bare name, and the dynamic archives are no longer published by
  default - a release carries them only when the workflow is dispatched with
  `include_dynamic_builds`. v0.3.0 shipped `magus_<version>_<os>_<arch>-static.tar.gz`
  beside a bare-named dynamic build; the hyphen made `<arch>-static` parse as an
  architecture, so the suffix is now an underscore field, and it marks the exception the
  way `busybox-static` does.

  This breaks a pinned URL twice over. A pin to `..._<os>_<arch>-static.tar.gz` no longer
  resolves - use `..._<os>_<arch>_static.tar.gz` - and a pin to the bare name, which used
  to fetch the dynamic build, now fetches nothing on a default release. Everything that
  fetches an archive by name asks for `_static` explicitly: the install script, the
  download guides, and `magus self update`.

- Breaking: Buzz FFI (`zdef()`) is unavailable in the `_static` release archives and in the
  `ghcr.io/egladman/magus:latest` container image. FFI opens a shared library at runtime,
  and that capability is what made those builds non-static (see Fixed); a build carrying it
  cannot also be loader-free. In those two artifacts `zdef()` now reports FFI as
  unsupported, the same graceful degradation an unsupported OS/arch already got, rather
  than failing at the call.

  Nothing else changes. Default builds, `go build`, `go install`, the dynamic release
  archives, and the `-dynamic` images all keep FFI. If a magusfile calls `zdef()`, use a
  `-dynamic` image or a dynamic archive. In the static image the capability was unusable regardless:
  it ships no shared libraries for `dlopen` to open.

### Removed

- Breaking: `magusfile` is no longer a spell. `import "magus/spell/magusfile"` and a
  `magusfile` entry in a project's `"spells"` list now fail with
  [MGS1017](reference/codes/magusfile/MGS1017.md) and the one-line fix: delete
  both. Neither did anything already - magus binds that driver to every project it
  discovers, because it is what makes a magusfile's own targets runnable rather than
  a toolchain an author opts into. Leaving the declarations accepted kept teaching
  readers that `magusfile` was a spell like `go` or `buf`, which the spell reference
  has never listed it as. Consequences: `magus describe spells` no longer lists it,
  and `magus ls` reports the toolchain a project actually binds (or none) instead of
  answering `magusfile` for almost every project - a fact true by construction, since
  having a magusfile is how a project is discovered at all.
- Breaking: `magus memory list` and `magus config mcp connector list` are now
  `... ls`, matching `magus ls` and `magus run ls`. The old spelling errors with a
  message naming the new one.
- Breaking: the three vendor spells register canonical, vendor-qualified names -
  `actions` is now `github-actions`, `s3-cache` is now `aws-s3`, and the GitLab CI
  provider's `ci` is now `gitlab-ci`. A registered name is what identifies a spell
  in every listing and diagnostic, with no directory around it to supply context,
  so it has to stand alone: `actions` named no product, and `ci` collided outright
  with the `ci` TARGET that `magus affected ci` anchors on. Source paths are
  unchanged (`spells/github/actions`, `spells/aws/s3-cache`, `spells/gitlab/ci`),
  so the path imports in magusfiles keep working; only the registered name moved.
  The reasoning is written down in [CONTRIBUTING.md](development/contributing/#naming).
- Breaking: `magus tail` is gone. It streamed the most recent cached log for the
  project in the current directory - a view `magus query output <ref>` already gives
  from the reference every run prints. A whole subcommand, flag surface, and man page
  for a narrower path to the same bytes. Its retired URL is listed in
  `docs/retired.urls.lock`; no successor page, because the capability did not move.
- Breaking (library callers): `magus.WithTargetNameNormalizer` and the
  `types.TargetNameNormalizer` interface are gone, along with
  `types.DefaultTargetNameNormalizer` and `types.NormalizeCharmName`. The interface had
  exactly one implementation and the option had zero callers anywhere in the tree,
  including tests, so `run.Normalizer` was always nil and the seam only ever installed
  the same kebab-casing sixteen other call sites reached for directly. Use
  `types.Normalize` for every entity name - target, charm, or spell op.
- The bundled PGO profile (`libs/gopherbuzz/default.pgo`, the `cmd/magus/default.pgo`
  symlink, and the `pgo-generate` target) is gone. A profile that has to be regenerated
  by hand after hot-path changes is stale more often than not, and it made `go build`
  and `go test` disagree about how the same package was compiled.
- The `assume_interactive` config key (`MAGUS_ASSUME_INTERACTIVE`,
  `--assume-interactive`) is gone. It existed to lift the TTY gate on `magus tail` and
  `magus x`, and did not earn its place on either. For `x` it never reached a working
  state: past the outer gate the picker hit its own TTY check and failed anyway, so the
  escape hatch only moved the error later. For `tail` it was a workaround for a gate that was
  too broad, and `magus tail` has since been removed outright (see above). Nothing
  replaces it; if you set it in `magus.yaml` it is now inert.

### Changed

- Breaking: built-in spells are named for what they adapt. `ts` is now `typescript`,
  `rs` is `rust`, `py` is `python`, `md` is `markdown`. `go` is unchanged - Go's name is
  Go. Update `import "magus/spell/<name>"` and the `spells:` list in `magus\project`;
  the handle an import binds changes with it, so `ts["tsc"]` becomes
  `typescript["tsc"]`. Op names are untouched (`cargo-build`, `pytest`, `markdownlint`
  already named their real tool). An unknown import still suggests the right spell, and
  the alias table now holds only genuine synonyms - `javascript`, `js`, `node`,
  `nodejs`, `cargo`, `python3` - rather than apologizing for abbreviations.
- Breaking: spell op names are normalized when the spell is decoded. Op keys are
  validated against a charset that admits `_` and uppercase, but every request arriving
  at dispatch has already been kebab-normalized, and dispatch is a map lookup - so an op
  authored `go_build` was stored under `go_build`, looked up as `go-build`, missed, and
  swallowed as a fan-out skip at debug level. Declared, and reachable by nothing. Every
  built-in already used kebab keys, so bundled spells are unaffected; a workspace-local
  spell with a `camelCase` or `snake_case` op now works instead of silently never
  running.
- Breaking (library callers): the `types.Describer` methods return slices instead of a
  `{definition, count, items}` envelope. `DescribeSpells`, `DescribeCharms`,
  `DescribeTargets`, `DescribeFiles`, `DescribeWorkspaces` and `DescribeTarget` now hand
  back `[]SpellEntry`, `[]CharmEntry`, `[]TargetEntry`, `[]FileEntry`,
  `[]WorkspaceEntry` and `[]EvaluatedTargetEntry`. `Definition` was a package constant
  and `Count` was `len()`, so every call site that filtered had to reassign `Count` by
  hand - a denormalization one forgotten line shipped as a wrong count. The JSON shape
  of `magus describe ... -o json` is unchanged; the envelope is rebuilt at the render
  edge. `DescribeProjects` and `DescribeEvaluatedProjects` keep a struct, because both
  carry a real `Workspace` field. `host.ModulesOutput` is now `host.Modules`.
- `magus describe spell` reports how to reach a spell and what it adapts: an `import`
  line you can paste (`import "magus/spell/go";`) and the source language it adapts.
  `SpellEntry` carries the import path as `buzz_import` in `-o json`. It is a path, not
  a handle: spell imports are read statically to build the target graph, so a spell
  reached any other way would lose its edge without failing.
- `magus describe` over MCP serves every noun the CLI does. `charms`, `graph` and
  `modules` were CLI-only, so an agent could not discover what charms exist, could not
  see the target graph, and could not introspect the Buzz stdlib at all.
- A non-canonical target or charm spelling now prints a one-time hint naming the
  canonical form (`magus run goBuild` -> `target "goBuild" is canonically "go-build"`).
  Silent when you already wrote the canonical form.
- Suggestions are case-insensitive. `magus run build API` missed project `api` and got
  no suggestion at all, because `API` -> `api` scored three edits against a threshold of
  two. Project paths still resolve exactly - they are filesystem paths.
- The sandbox passes every `GO*` variable through (`sandbox.env.passthrough: ["GO*"]` in
  this repo's `magus.yaml`). A variable that shapes compilation but does not reach the
  compiler does not get ignored, it splits the build cache: `GOEXPERIMENT` reaching one
  `go` invocation and not another produced a linker `fingerprint mismatch` that looked
  unrelated to anything.
- Knowledge-graph schema v7. No node or edge shape changed: the bump is because shard
  fingerprints are now computed by streaming fields into SHA256 rather than by hashing
  marshaled JSON, so every fingerprint VALUE differs from a v6 store's. The manifest
  check treats a version mismatch as a full rebuild, which is the whole migration. The
  old approach marshaled each shard purely to hash the bytes, putting an encode on the
  hot path of every magus command (fingerprinting all shards costs 757 ms at 50k
  projects) and coupling the fingerprint to the storage format, so a future format
  change would have silently invalidated every cached shard. Measured: -44% sec/op,
  -23% B/op, -41% allocs/op.
- Host methods that return a record now say so in their signature: `magus\cmd(args,
  [opts]) -> ExecResult` where it previously read `map[string]any`. Nineteen methods
  across nine modules were affected. Annotating the named type (`final r: ExecResult =
  magus\cmd(...)`) makes the checker verify field access, turning a typo from a runtime
  nil into a load error; that already worked and was simply undiscoverable.

### Added

- Agent skill version 22. Skills now render their full and simple permutations
  with standard-library `text/template` branches (`{{if .Full}}`, `{{else}}`,
  `{{if .Simple}}`) instead of private HTML-comment markers. Installation now
  fails loudly for malformed template syntax, while a parse-tree guard keeps
  bodies limited to deterministic wording branches.
- `magus\normalize(name)` canonicalizes any entity name from a magusfile - the same
  function targets, charms and spell ops resolve through. It is also live in the browser
  playground, so [the name-normalization docs](https://eli.gladman.cc/magus/concepts/targets/)
  run their examples rather than asserting the rule.
- The playground says when a `test "..." {}` block was not run. Buzz test bodies execute
  only under `magus buzz -t`, so evaluating one in the playground was a silent no-op: a
  deliberately failing assertion looked exactly like a passing one.
- Agent skill version 19. `magus-architecture` now surveys for what is too THIN to
  justify a boundary, not only what is too big. Every existing lens (god nodes,
  hotspots, affinity, ownership) detects something central, hot, or heavily coupled;
  none detect over-abstraction, which is the more common failure early on. The skill
  names the cost no metric records: in Go every package boundary forces an export, so
  splitting files into packages to organize them widens the public surface you were
  trying to keep small. It flags three shapes - imported only from inside its own
  subtree, one importer with nothing encapsulated, single file with a single exported
  symbol - and says explicitly that SIZE is not one of them, because a small package
  that hides four helpers behind one function is earning its keep.

  Known gap this does not close: the graph has no `package` kind, so it cannot answer
  "who imports this Go package" directly. Its finest structural rung is `project`
  (a magusfile-bearing directory) and the next is `file`; Go's unit of encapsulation
  sits between them and is unmodelled. Minting `package` with `imports` edges would
  make the first shape above a one-line query instead of a manual read.

- `ctx.updates(...)`, a third per-target footprint declaration beside `ctx.inputs` and
  `ctx.outputs`, for a file a target EDITS rather than produces: a hand-written page with
  a generated region between markers, a manifest a tool rewrites in place. magus never
  deletes an update (`magus clean` skips it) and never replays one from a cache snapshot,
  because the bytes it produced are only part of the file. It folds into the cache key
  like an input, so editing the prose around a generated region invalidates the target
  that maintains that region - which declaring the file an output could not do, since an
  output is excluded from its own source hash. It infers no ordering edge in either
  direction; declare `ctx.needs` if you need one.

  This closes a real data-loss path. `docs/concepts/spells.md` and
  `docs/concepts/knowledge.md` are 355- and 570-line hand-written pages carrying a small
  generated region, and both were declared in `ctx.outputs`: `magus clean docs` deleted
  them whole, and the next `content-generate` died with `inject spell list: open
  concepts/spells.md: no such file or directory`. Only git made that recoverable. Both
  are now declared with `ctx.updates`. `magus clean`'s help no longer describes what it
  removes as "regenerable build artifacts" either - that was the declaration's claim, not
  something clean verified.
- `magus agent install --simple` installs a shorter permutation of every agent skill: the
  imperative steps with the rationale withheld, for a capable model that infers the why and
  would rather spend the context on the task. Both permutations are hand-authored from ONE
  source body (an author brackets the withheld spans), so they cannot describe different
  behavior and they share one content digest - `magus graph verify` reports staleness the
  same way whichever is installed, and the file's stamp records `skill-variant`. Across the
  eight skills the short form is 14% smaller. The docs site now reproduces every skill in
  both forms with a size comparison (`docs/reference/skills/`), generated from the embedded
  bodies so it cannot drift from what install writes.
- The `magus-changes` skill now serves three outputs rather than one: the evidence-backed
  brief it already wrote, a `CHANGELOG.md` entry in this file's existing Keep a Changelog
  shape, and per-question granular diff commands - all answered through magus surfaces
  (`graph diff`, `describe file`, `affected --impact/--explain`) rather than a raw diff.
- Shell completion now offers the target names this workspace actually declares, read from
  `magus describe targets`, instead of eight names baked into each script; zsh and fish also
  show each target's kind (canonical, or the spell providing it). Falls back to the built-in
  set outside a workspace, where `describe` cannot answer.
- `magus buzz --workspace` gained a line editor: arrow-key history, line editing, and Tab completion
  drawn from magus's own surfaces - meta commands, the session's user globals, host modules
  and their methods (`fs.writeF<TAB>`), and the workspace's targets and projects. A piped
  session is unchanged. It also pins a one-row status footer showing the active language,
  the working directory, and the parser's continuation depth.

- File authorship is now first-class in the graph (schema v6): an `author` node per git
  contributor with `authored` edges to the files they touched, so `explain author:<name>`
  shows what someone maintains and it can be set against a file's declared CODEOWNERS owner
  (the emergent maintainer vs the owner of record). The edges are uncapped - bounded only by
  the `knowledge.vcs.max_commits` history window, not an arbitrary per-author limit - so a
  solo maintainer's full authorship is a fact the graph teaches, not a summary it hides.
  Extracted from the same git-history scan (author facts in the graph; aggregate analytics
  stay in insight). Set `knowledge.vcs.authorship: false` (env `MAGUS_KNOWLEDGE_VCS_AUTHORSHIP`)
  to keep only the per-file `vcs_*` attrs and omit the author node/edge layer; on by default.
- File nodes now carry `vcs_last_author` (the last commit's author) alongside the existing
  `vcs_last_commit`/`vcs_last_modified`/`vcs_commits`, so a file's EMERGENT maintainer (who
  actually edits it) can be set against its DECLARED CODEOWNERS owner - a gap a pure
  code-graph cannot see. Captured from the commit history magus already scans.
- Knowledge graph indexes the build I/O layer and authored markdown (schema v5). Each
  target's declared `magus.outputs` / `magus.inputs` becomes a `produces` / `consumes`
  edge to the file and doc nodes it matches, so a generated file is self-labeled by its
  producing target (`explain doc:docs/spells/go.md` shows "produced by content-generate")
  and you can walk a target to exactly what it writes; a per-glob fan-out cap keeps a
  broad declaration from turning a target into a god node. Separately, every authored
  markdown file workspace-wide (README, AGENTS.md/CLAUDE.md, CHANGELOG, SKILL.md, ...) is
  now a `doc` node carrying a `role` attr from a universal filename convention and a
  `contains` edge from its project, so `query "kind:doc role:agent"` finds the
  agent-instruction files in any repo.
- Knowledge graph gains build and runtime dimensions: each spell op now carries the
  base argv it runs (an `argv` attr) and `use`s a `tool` node for the program it runs,
  so `explain tool:go` lists every op that runs go and `kind:tool` is the workspace's
  toolchain inventory - a target reaches its tool via its existing `target --uses--> op`
  edge. Plus test `coverage` with a `test_refs` count folded onto file and symbol nodes
  from the coverage profile magus already produces, and `magus refs` now returns the
  definition's `file:line`. Query recipes: [the knowledge graph](concepts/knowledge.md).
- `daemon.enabled` (flag `--daemon-enabled`, env `MAGUS_DAEMON_ENABLED`, default true):
  set false to run each invocation self-contained in its own per-process pool instead
  of discovering and adopting the shared `magus server start` daemon - handy for a
  worktree that should not touch a shared daemon. Recursive `magus` calls still forward
  over a per-process socket to share the concurrency budget; only the shared daemon is
  opted out of.
- Self-documenting output templates: bare `-o template` (no body) lists the
  command's output fields - the json keys usable in `-o json` and `-o template`,
  with each field's type, drilling into nested types. Works for every structured
  command (the field list is reflected from the output value, no per-type
  registration). Previously an empty template was an error. No new command or
  format: it rides the existing `-o template` surface.
- Spell authoring kit: `magus init spell` scaffolds a spell, `magus buzz -t` runs a
  spell's in-file test blocks, and `magus buzz lsp` serves diagnostics and
  completion to an editor over stdio.
- `buf-breaking` op in the buf spell: gates a proto schema against a baseline
  branch, composable into a `lint` target. See [Breaking changes](migrating/breaking-changes.md).
- `describe target --explain` prints the charm trace behind a target's resolved
  command, so a stacked argv patch is inspectable before a run.
- Silent-failure diagnostics: an invalid charm patch (MGS6001), a `has_charm` typo,
  a spell that binds zero ops, and an unknown project name now report a coded,
  actionable error instead of failing quietly.
- Interspersed global flags: `magus <command> --verbose` and `magus --verbose
<command>` now parse the same way.
- `magus describe charm[s]` inverts the charm index: it lists every target that
  declares a charm and the argv edit it makes, marking the reserved built-ins and
  workspace defaults.
- Charm conflict detection: when two active charms edit the same argument, one
  silently overrides the other (the winner decided by name order), so magus warns
  that the losing charm has no effect at run time and flags it in `magus describe
target ...:a,b` before a run. Disjoint edits never trip it.
- `magus describe target` describes a service op before it runs: its readiness
  probe, stop command, idle window, whether it is shared, and its dedup fingerprint.
- `magus graph` is the home of the workspace's graphs as objects: `graph deps`
  emits the project dependency DAG (the standalone form of `run --graph` /
  `affected --graph`, which remain), `graph export` emits the merged knowledge
  graph (`-o json` node-link, or the new `-o graphml` for external graph
  viewers), and `graph stats` reports its shape (god nodes, orphans, doc
  coverage; `--kind` to scope). The `query`/`explain`/`path` retrieval verbs
  are unchanged.

### Fixed

- `magus.Open(ctx, root)` works again for library callers. The literal-argument rule
  over `ctx.inputs`/`outputs`/`updates` was scoped on a per-target `skip_cache` policy,
  but policies are only populated once the interpreter has evaluated `magus\project()` -
  which a bare library caller never does. So every target read as cacheable and a
  magusfile the CLI loads fine was rejected. The rule now splits by declaration kind:
  footprint declarations stay a hard error, execution overrides (`ctx.withEnv`,
  `ctx.withCwd`) do not.
- `magus run --dry-run <target>:<charm>` takes the same charm branches as the real run.
  The tracer normalized the target half of a `target:charm` reference but not the
  charms, and compared `has_charm` raw - so `lint:no_cache` traced un-charmed while the
  real `lint:no_cache` ran charmed. The tracer's whole premise is fidelity to the run it
  predicts.
- The docs site no longer walks into a nested `node_modules`. Generated directories were
  skipped only as exact children of the docs root, so once a sub-project under `docs/`
  had its dependencies installed, the render began emitting every dependency's
  `README.md` as a page - an unbounded render that also wrote into a descendant project
  (MGS3001).
- Forwarding to a daemon of a different build no longer warns. A version/protocol
  mismatch means the daemon is alive but will not adopt a mismatched client, so the
  command now falls back to local execution quietly (a debug line, not a `[warn]
proc forward failed` line). This is routine when multiple worktrees run different
  builds against one shared per-user daemon.
- A workspace-local Buzz spell could not declare a service op: the host-registered
  `magus/target` module omitted the `Service` type (present only on the dry-run
  host), so `Service{...}` failed to compile. Both hosts now register it.

### Changed

- The knowledge graph's git-history (`@vcs`) scan is now cached through the standard shard
  store - keyed by an input fingerprint (HEAD + window + schema) recorded in the manifest -
  instead of a bespoke `vcs-inputs.json` sidecar. The expensive scan runs only when HEAD or
  the window actually moves; an unchanged tree reuses the shard from disk with no extra
  serialization. The window (`knowledge.vcs.max_commits`, default 1000) bounds the scan so
  it never walks a whole monorepo's history.
- `magus explain` and `magus path` now render as compact natural-language text by
  default, for both the CLI and the MCP tools: an edge's direction is folded into a
  verb (`used by`, `depends on`, `part of`, `required by`), edges are grouped by that
  verb with a count before any multi-item list, and full node IDs are listed - so one
  rendering serves humans, agents that read, and the docs. This replaces the
  `<--uses-- op:go:go-build [op]` adjacency notation, which made the reader invert the
  arrow, and the verbose JSON the MCP tools returned (roughly 4x the size). `-o json`
  remains the structured form for agents that parse.
- Breaking: `-o template=<go-template>` now renders against the JSON-normalized
  value, so template field names are the json-tag keys (`{{range .projects}}{{.path}}{{end}}`),
  identical to what `-o json` emits, instead of the PascalCase Go struct fields
  (`{{.Projects}}`/`{{.Path}}`) it exposed before. This makes `-o json` a faithful
  reference for authoring templates. Numbers arrive as float64 (coerce with `int`
  before numeric comparison); `join` now accepts any list, not just `[]string`.
- Breaking: `magus describe knowledge` is now `magus graph export`, and
  `magus insight structure` is now `magus graph stats`; the old spellings error
  with a pointer to the new home. `insight report` still embeds the graph-stats
  section, renamed from `structure` to `graph_stats` in its `-o json`/`yaml`
  output (the `KnowledgeStats` schema itself is unchanged).
- `magus buzz lsp` replaces the top-level `magus lsp`.
- Local spell imports resolve workspace-root-first with walk-up accrual; a name
  collision between an ancestor and a descendant spell is flagged (MGS1002) and
  suppressed only with an acknowledged `spells.allow_shadow` reason.

## [v0.3.0] - 2026-07-25

See the full changelog at
https://github.com/egladman/magus/compare/v0.2.1...v0.3.0

## [v0.2.1] - 2026-07-19

See the full changelog at
https://github.com/egladman/magus/compare/v0.2.0...v0.2.1

## [v0.2.0] - 2026-07-18

See the full changelog at
https://github.com/egladman/magus/compare/v0.1.0...v0.2.0

## [v0.1.0] - 2026-07-05

### Added

- Playground: an in-browser CodeMirror editor with live diagnostics, module and
  symbol autocompletion, hover docs, and call-signature help, backed by the
  WebAssembly interpreter; a collapsible notice lists the host modules the
  browser cannot run.
- Docs site: first-class `/blog` subsystem with reverse-chronological listing,
  breadcrumb root, per-post edit links, and Blog nav item.
- Docs site: two Atom 1.0 feeds — `/public/atom/blog.atom.xml` (posts) and
  `/public/atom/releases.atom.xml` (releases, derived from this file).
- Docs site: nested Apache-`mod_autoindex`-styled `/public/` tree with an
  autoindex helper — hub at `/public/`, feeds at `/public/atom/`, release
  artifacts at `/public/release/`.

### Changed

- Docs site: extensionless URLs everywhere (`/documentation/`, `/modules/fs/`);
  the authored `docs/manpage/gen/` path segment is flattened out of public URLs.
- Docs site: nine flat client scripts collapsed into a two-file esbuild bundle
  (`theme.js` head-critical, `main.js` deferred module).
- Docs site: nav link "GitHub" moved to the footer, relabeled "Source Code".

### Fixed

- Docs site: mobile TOC becomes a slide-up bottom-sheet instead of stacking
  above the article; page toolbar reflows so search fills its row and
  "Suggest an edit" drops below.
