---
title: Security scanning
description: Fail a pull request on the vulnerabilities it introduced and main on every vulnerability it carries, from one scan and one manifest read at the base ref, for Go and pnpm.
tags:
  [
    security,
    govulncheck,
    pnpm-audit,
    vulnerabilities,
    base-ref,
    provenance,
    ci,
  ]
---

# Security scanning

A `security` target that fails on a scanner's exit code turns every open pull request
red the day a CVE lands against a module main has carried for a year. The authors of
those pull requests cannot fix it there, so they learn to rerun or ignore the gate. This
guide builds a target that tells the two cases apart, using facts magus already holds.

## 1. Two questions, one exit code

A scanner answers "is anything in this tree vulnerable". A pull request needs a
different answer: "did this change make it so". The first question belongs to main and
to a schedule, where someone owns the whole tree. The second belongs to the pull
request, where the author owns only what the change added. Dependency review tools draw
the same line: a new or updated dependency gates the change, an existing one raises an
alert against main.

## 2. What magus holds

The base ref is the revision `magus affected` diffs against:

- `vcs.base_ref` in `magus.yaml`, or `MAGUS_VCS_BASE_REF` for one run.
- `magus doctor` runs the `vcs-base-ref` check and fails when the ref is unreachable.
- A shallow clone may lack the base; magus deepens it for `affected`, and
  [CI checkout](integrations/ci.md) covers the clone shapes that keep it reachable.

From a target, the [vcs module](../reference/buzz/vcs.md) reads the rest:

| call                                    | answers                                              |
| --------------------------------------- | ---------------------------------------------------- |
| `vcs\base()`                            | the resolved base ref                                |
| `vcs\commit(vcs\base()).id`             | the base revision, to compare with `vcs\commit().id` |
| `vcs\cmd(["show", base + ":./go.mod"])` | the manifest as the base has it (git's spelling)     |

## 3. The `security` target

`security` is a canonical target name worth adding (see
[targets](../concepts/targets.md)). Declare it with three properties:

- `skip_cache` with a reason: the advisory database changes when your tree does not,
  so a replay would report a stale verdict.
- A `timeout`, because this target reaches the network and a stalled feed should end
  the run. Name that reach in the comment above the timeout.
- `ci` composes it, so `magus affected ci` runs it with the rest of the gate.

```buzz
magus\project({
    "targets": {
        "security": {
            "skip_cache": "reads the vulnerability database, which changes independently of this tree",
            "timeout": "15m",
        },
    },
});
```

The go spell's `govulncheck` op declares `External.reads`, which `magus doctor` reads
to flag a cacheable target composing it (MGS1033). The op captures its output and
defaults its package pattern to `./...`, so the recipe calls it with
`{"args": ["-format", "json", "./..."]}` and reads the JSON from the result's `stdout`.

## 4. Classes

Every finding carries two labels. Actionability decides whether anyone can fix it
today. Provenance decides who owns it.

| actionability | rule                                                                                                                                          |
| ------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| actionable    | Go: this code calls the vulnerable symbol. pnpm: severity high or critical, and a patch on the installed major has cleared the cooling window |
| DEFERRED      | everything else; printed on every run, never fatal, whatever its provenance                                                                   |

| provenance   | rule, for module M at version V in the finding                          |
| ------------ | ----------------------------------------------------------------------- |
| `introduced` | the base manifest lacks M, or carries M at another version              |
| `inherited`  | the base manifest carries M at V                                        |
| `unknown`    | no base to read; printed once as `no base ref; provenance not computed` |

A version bump counts as introduced. The change chose that version.

The pnpm cooling window mirrors `minimumReleaseAge`: a patch younger than it cannot be
installed, so it cannot be demanded. A patch only on the next major is deferred too,
because moving a transitive dependency across a major needs its dependent to widen a
range, and no change to your lockfile can do that.

## 5. Go and pnpm

Go:

- `govulncheck -format json ./...` streams findings at module, package and symbol
  level. Symbol level is the default scan and the only level that means your code calls
  the vulnerable function.
- `Trace[0].Module` and `Trace[0].Version` name the installed module. `stdlib` is keyed
  at its language line (`1.26`), because `go.mod` pins the line and not the patch.
- The base `go.mod` goes to a temp file and through the go spell's `go-mod-json` op
  (`go mod edit -json <file>`). No modfile parser lives in Buzz.

pnpm:

- `pnpm audit --json` lists advisories with each installed version under `findings`.
- The base `pnpm-lock.yaml` goes through `yaml\parse`; its `packages:` keys are
  `name@version` (lockfile v9), or `/name@version(peers)` (v6), normalized to
  `name@version`.

Both feed one `classify(installed, base)` function.

## 6. Where each class fails

| run                                  | base vs head | fatal                          |
| ------------------------------------ | ------------ | ------------------------------ |
| pull request branch                  | differ       | actionable and not `inherited` |
| merge queue candidate, with the flag | differ       | every actionable finding       |
| main after a merge, schedule         | equal        | every actionable finding       |
| no VCS, or the base does not resolve | none         | every actionable finding       |

The target compares `vcs\commit(vcs\base()).id` with `vcs\commit().id`. It reads no CI
variable, so the same command gives the same verdict on a laptop and in a runner. An
inherited finding on a pull request prints as `[inherited]`; the base carries it, so
main's next run and the schedule fail on it.

A merge queue candidate differs from its base like a pull request does, yet it becomes
main when it passes. Nothing in the tree tells the two apart, so the queue says so with a
target argument:

```sh
magus run security . -- --inherited=fatal
magus run ci:gha . -- --inherited=fatal
```

`ctx.needs` hands a dependency no arguments, so a `ci` that composes `security` reads
the flag itself and calls `security(ctx, args: args)` in its body when it is set. Use an
argument rather than a charm here: a charm keys every step the run reaches, so a queue
run under one would miss the cache main's runs wrote.

Every run ends with one summary line, so a week of runs shows the split:

```text
security: 1 introduced, 3 inherited, 7 deferred
```

## 7. What magus does not do

- No VEX documents, no allowlist file, no snooze.
- No severity policy of its own. The thresholds live in your Buzz.
- No second scan of the base. `fixed` (in the base's findings, gone now) would need one,
  and nothing here computes it.
- No decision. magus supplies the base ref and the manifest at that ref; your
  `security` target owns the verdict.

## 8. Prior art

GitHub's dependency-review-action and osv-scanner's pull request mode make the same
split. dependency-review-action reads GitHub's hosted dependency graph, which a private
repository gets only with GitHub Advanced Security. osv-scanner scans the base and the
head and diffs the two result sets, so every pull request pays for two scans. This
recipe runs one scan and one manifest read, needs no hosted service, and covers a
private repository and pnpm the same way.

## 9. Copy this

Copy `hack/magusfile/advisories.buzz` from the magus repository into your workspace. It exports
three functions:

| function                                                    | does                                                | needs               |
| ----------------------------------------------------------- | --------------------------------------------------- | ------------------- |
| `advisories\govulncheck(ctx, go: go, inheritedFatal: bool)` | scans the Go module in the target's directory       | the go spell handle |
| `advisories\audit(inheritedFatal: bool = false)`            | scans the pnpm project in the target's directory    | nothing             |
| `advisories\inheritedFatal(args)`                           | reads `--inherited=fatal`, raising on anything else | the target's `args` |

A Go project:

```buzz
import "magus";
import "magus/spell/go";
import "./hack/magusfile/advisories" as advisories;

export fun security(ctx: magus\Context, args: [str]) > void !> any {
    advisories\govulncheck(ctx, go: go, inheritedFatal: advisories\inheritedFatal(args));
}
```

The first argument can be `ctx.withEnv({...})` when the scan needs the build's
environment, such as a `GOEXPERIMENT` the rest of the workspace sets.

A pnpm project:

```buzz
import "magus";
import "../hack/magusfile/advisories" as advisories;

export fun security(ctx: magus\Context, args: [str]) > void !> any {
    ctx.needs(install);
    advisories\audit();
}
```

Keep the `test` blocks in the copied file and run them with `magus buzz -t --embedded
hack/magusfile/advisories.buzz`. They pin the parts you are most likely to edit:

- `classify` provenance, including a version bump counting as introduced.
- `isFatal`: inherited is fatal only where the base is the head.
- `inheritedFatal`: the one argument `security` takes, and what it refuses.
- The govulncheck stream parse and the `go.mod` and lockfile inventories.
- The npm range handling behind the cooling window.

To change who fails on what, edit `isFatal`. To change what counts as actionable, edit
`isBlockingSeverity` and `hasInstallablePatch` for pnpm, or `goFindings` for Go.
