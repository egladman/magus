---
name: magus-local-development
description: Rules for DEVELOPING MAGUS ITSELF in this repository - dogfooding, not using magus elsewhere. Use when reviewing or changing magus's own Go source, when acting on code-review findings against this tree, when touching a Buzz host module descriptor in std/, and when a change ripples into generated output. These rules are workspace-specific and deliberately NOT part of the shipped magus-* skills.
metadata:
  source: workspace
---

# Developing magus itself

The shipped `magus-*` skills teach the tool. This file is the opposite: it is
about working on magus's own source in this repository, and it exists because a
whole-tree review turned up defect classes that repeat here and are invisible
from the shipped skills.

Every rule below carries a stamp. A rule with no stamp is not a rule - report it
and do not obey it (see `magus-workspace-rules`).

<!-- rule: look-for-the-pin-before-fixing; added: 2026-08-11; origin: agent, unreviewed;
     evidence: commit 8b633ba99, internal/sandbox/filesystem/filesystem_test.go TestCheckExecRequiresReadNotExec;
     retire-when: pinning tests carry a machine-readable marker a reviewer can filter on -->
## Look for the pinning test before you "fix" something

Some code here looks wrong and is not, and the proof is a test written to say so.
Before acting on a finding, search for a test or doc comment that justifies the
current behavior.

The clearest case: `checkAccess` treats exec exactly like read and never consults
`Rule.Exec`, which reads as a security hole. `TestCheckExecRequiresReadNotExec`
exists solely to say it is not, and names "fixing checkAccess to require r.Exec"
as one of two specific mistakes it catches - exec is enforced by the landlock
layer instead.

In one whole-tree review roughly one finding in ten was wrong this way. Two of the
refuted ones had pins. Treat "this is obviously a bug" as a hypothesis.

<!-- rule: a-test-can-encode-the-bug; added: 2026-08-11; origin: agent, unreviewed;
     evidence: commits 6b6c1509a (TestImportMaxBytesCapsTarBomb), c4c1b5c60 (TestRotate_CapsEventsAndGCsOrphanBlobs), dd7e44ac0 (TestLPT_balancesByDuration);
     retire-when: never - this is a property of tests, not of this tree -->
## A green test is not proof the behavior is right

Three tests in this tree asserted a defect as correct. Fixing the code meant
rewriting the test, not just adding one.

- `TestImportMaxBytesCapsTarBomb` asserted `require.NoError` on an import that
  silently truncated an oversized entry into a corrupt cache blob.
- `TestRotate_CapsEventsAndGCsOrphanBlobs` asserted immediate collection of a
  freshly written blob, which is exactly the data-loss window.
- `TestLPT_balancesByDuration` passed only because its input was already sorted,
  so the broken comparator never executed a swap.

So: a fix starts with a test that FAILS for the stated reason. If the test passes
before your change, you have not reproduced anything, and you may be about to
"fix" working code or leave the real bug in place.

<!-- rule: buzz-descriptors-are-codegen-inputs; added: 2026-08-11; origin: agent, unreviewed;
     evidence: commits eedc47870 and f40fc44a9 (a one-word Name change left four generated files stale and three tests red);
     retire-when: std method names gain an alias mechanism, or a drift test pins the std surface the way magus-api.lock pins magus.* -->
## A std/ descriptor edit is never local

In `std/`, a method's `Name` and `Doc` are inputs to codegen, not documentation.

- `Doc:` reaches generated `.d.ts`, `docs/reference/buzz/*.md` via `cmd/magus-docs-lookup`,
  and LSP hover text. A wrong `Doc` teaches every Buzz author the inverse contract.
- `Name:` changes the Buzz-facing identifier and is a BREAKING change with no
  migration path. MGS1025's removed-API table covers only the `magus.*` namespace,
  and `internal/interp/bindings/testdata/magus-api.lock` pins only `magus.*` member
  names. Nothing catches a renamed std method.

Renaming `fs.mkdirall` to `fs.mkdirAll` was one word in one descriptor. It left
`internal/interp/bindings/gen/fs.go`, `internal/spellruntime/gen/decls/fs.buzz`,
`internal/langservice/manifest_data.go` and the manpage API lock stale, and three
tests red until regeneration. Regenerate in the SAME commit - `go generate` reaches
`cmd/magus-utils`, so it works without a magus binary.

<!-- rule: the-daemon-is-one-process-many-runs; added: 2026-08-11; origin: agent, unreviewed;
     evidence: commits c5cfc0c33, bdc077d33, dce52e193, 985cb26f8;
     retire-when: package-global mutable state is gone from the run path, or a linter enforces its absence -->
## Package-global state outlives the run that wrote it

magus began as one process per run and grew a daemon. Code written under the old
assumption is still here, and it is the single most productive place to look for
real bugs. Every one of these was live:

- A readiness probe memo keyed by tool cached a `context.Canceled` FOREVER, so one
  Ctrl-C wedged that op for the daemon's life.
- Adopted runs bound flags into the process-global `globalCfg`, so one client's
  `--dry-run` silently turned a later client's run into a dry run.
- The warm knowledge graph built adjacency indices lazily on the READ path while
  being shared across concurrent requests - a concurrent map write, which is an
  unrecoverable Go fatal that kills the daemon, not a recoverable panic.
- Client RPCs used ctx only for `Dial`; the blocking read ignored it entirely.

When you see a package-level `var` cache, memo, or `sync.Once` on the run path,
ask what happens on the SECOND run in the same process, and on two concurrent ones.

<!-- rule: docs-drift-is-the-most-common-defect; added: 2026-08-11; origin: agent, unreviewed;
     evidence: commits 1f32838dd, 24d7a849a, 13930912e, 8a334e846;
     retire-when: a mechanism exists that checks a doc claim against behavior -->
## Assume a doc comment is stale before assuming it is true

The largest single defect class in the whole-tree review was comments asserting
the inverse of the code: functions documented as returning a zero value that
raise, a "cross-process lock" that does not exist, `TopoSort` documented as
dependencies-before-dependents when it is measurably the reverse, a "richer
description wins" merge that is first-writer-wins.

No linter can catch this - `godoclint` compares a doc's leading NAME to its
symbol, not its claims to its behavior. The only mechanism that works is a test
that pins the contract, which is why `std/vcs_test.go`'s raise-behavior tests are
what proved the vcs docs were the stale side rather than the bodies.

When code and comment disagree, find the test. If there is no test, you do not yet
know which one is wrong.
