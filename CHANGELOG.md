# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

See the unreleased changes at
https://github.com/egladman/magus/compare/v0.2.1...main

### Breaking

- **`vcs\diff` is now `vcs\changedFiles`.** It returns the file paths changed against a
  base ref, not a diff, and the name said otherwise. The confusion became concrete when
  `vcs\dirtyDiff` arrived and read like a variant of it rather than a different question.
  The old name is retired rather than repurposed on purpose: host-module members are typed
  to the checker, so a magusfile still calling `vcs\diff` fails at load with a clear
  error, where reusing the name for the new meaning would have silently passed a base ref
  where a path list is expected.

### Added

- **`vcs\dirtyDiff([paths])` returns the working tree's uncommitted changes as text**, on
  every backend. Drift gates previously branched on `vcs\name() == "git"` and shelled out
  through `vcs\cmd`, so an hg or jj user got filenames with no diff. git, hg, and jj now
  each implement it and every one of those branches is gone.
- **Markdown is formatted by dprint.** The workspace had no markdown formatter after
  prettier was dropped. Each project carries its own `dprint.json` extending a shared base,
  the way `biome.json` already does, and the markdown spell exposes a `dprint` op.

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
- **`opts.quiet` on `os\exec`, `os\exec_sh`, and `vcs\cmd`.** Captures output without
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
  (`magus-local` by convention) was already safe by construction - install writes only
  the names it ships and verify grades only those - so this release makes the
  convention discoverable rather than building a mechanism: a new `magus-adapt` skill
  carrying the method and the per-rule stamp format (evidence, and the condition that
  retires the rule), the name reserved against a future shipped skill, and a `magus
  hook --path` advisory that fires when an agent is about to edit a stamped install.
  Skill contract v24.
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
  or AWS Secrets Manager are an `os\exec` away and magus grows no per-provider code; with no
  provider declared, the built-in one treats a reference as an environment variable name.
  A value is a secret because it was read through the resolver, never because its name
  looked credential-shaped, so magus can keep it out of what it persists: the captured
  output, the raw log, the output store, the journal, and every log format are redacted at
  their write boundary. See [docs/concepts/secrets.md](docs/concepts/secrets.md), which is
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
- [MGS1020](docs/reference/codes/magusfile/MGS1020.md) reports a generated file claimed as
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
  reports [MGS1021](docs/reference/codes/magusfile/MGS1021.md) naming both fixes (upgrade
  the binary, or raise the pinned version in CI). It has to be declared rather than derived
  because the binary that hits the problem is the OLD one: it cannot look up which release
  added the module it is missing, having never heard of that release. Without it, a too-old
  magus fails from wherever the magusfile first touched something it lacks -
  `import "xml": module not found`, which reads like a typo. See
  [docs/concepts/compatibility.md](docs/concepts/compatibility.md), which states what magus
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
- `magus doctor` reports a bespoke phase-fragment target name ([MGS1003](docs/reference/codes/magusfile/MGS1003.md))
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
- The `-static` release archives and the `latest` container image are now actually static.
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

- Breaking: the release archives now name the static build WITHOUT a suffix, and the cgo
  build with `-cgo`. `magus_<version>_<os>_<arch>.tar.gz` used to be the cgo build, and the
  static build carried `-static`; the container tags said the opposite (`latest` static,
  `latest-cgo` cgo), so one release described the same default two opposite ways. Both now
  follow the image convention.

  This renames what people already download rather than changing which build they get: the
  install script has always defaulted to `VARIANT=static` and the download guides have
  always linked the static asset. It does break a pinned URL. A pin to
  `..._<os>_<arch>-static.tar.gz` no longer resolves - drop the suffix - and a pin to the
  bare name now yields the static build instead of the cgo one. `VARIANT=cgo` selects the
  glibc build from the install script.

- Breaking: Buzz FFI (`zdef()`) is unavailable in the `-static` release archives and in the
  `ghcr.io/egladman/magus:latest` container image. FFI opens a shared library at runtime,
  and that capability is what made those builds non-static (see Fixed); a build carrying it
  cannot also be loader-free. In those two artifacts `zdef()` now reports FFI as
  unsupported, the same graceful degradation an unsupported OS/arch already got, rather
  than failing at the call.

  Nothing else changes. Default builds, `go build`, `go install`, the cgo release archives,
  and the `latest-cgo` image all keep FFI. If a magusfile calls `zdef()`, use the cgo image
  or a non-`-static` archive. In the static image the capability was unusable regardless:
  it ships no shared libraries for `dlopen` to open.

### Removed

- Breaking: `magusfile` is no longer a spell. `import "magus/spell/magusfile"` and a
  `magusfile` entry in a project's `"spells"` list now fail with
  [MGS1017](docs/reference/codes/magusfile/MGS1017.md) and the one-line fix: delete
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
  The reasoning is written down in [CONTRIBUTING.md](CONTRIBUTING.md#naming).
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
  both forms with a size comparison (`reference/skills/`), generated from the embedded
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
  definition's `file:line`. Query recipes: [the knowledge graph](docs/concepts/knowledge.md).
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
  branch, composable into a `lint` target. See [Breaking changes](docs/migrating/breaking-changes.md).
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
