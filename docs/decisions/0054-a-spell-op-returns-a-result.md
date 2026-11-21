---
title: "ADR 0054: a spell op returns a Result"
order: 54
description: "A provider op that can fail returns result\\Result::<T>, and bindings.CallOp reads its error as the failure, worded and coded as the Diagnostic says. An op that cannot fail returns T. An op that declares !> is refused at load with MGS1051. Every shipped op that raised now returns a Result, and their no-throw rows are gone."
tags: [adr, decision, spells, providers, buzz, boundary, errors]
status: accepted
date: 2026-11-19
---

# ADR 0054: a spell op returns a `Result`

## Context

[ADR 0011](0011-typed-provider-calls.md) gave every provider op one typed signature,
`fun op(target: Target, req: Req) > Resp`, called through `bindings.CallOp`. An op that
failed raised: `CallOp` read the error `sess.CallValue` returned and wrapped it as
`spell "<name>": <op>: <err>`. Each caller then read that error for its family: the remote
cache counts a failed store call, the lifecycle provider reports the source unreached, the
secret provider reports an unresolved secret.

Below the entry, magus returns errors as values (`types.Result`, `result\Result::<T>` in
Buzz), and `no-throw` in `hack/lint` counts every `!>` signature that remains. At a827d46cc
the spell ops were the largest group of rows left, and each carried a
`//nolint:no-throw // magus reads a raise as ...` explaining that the host contract made it
raise: `aws/s3-cache` (4), `endoflife-date` (1), `experimental/nx` (1), `github/actions` (5),
`github/review` (5), `onepassword`, `python`, `system-keychain` and `typescript` (1 each).
The op bodies already built `result\Result` values internally and threw `err.message` at the
last step, only because the host read nothing else.

Eli ruled on 2026-11-19: an op returns a Result and the host reads it.

## Decision

### An op that can fail returns `result\Result::<T>`

`CallOp` reads what the op returns. A `result\Result` whose `error` is set is the failure:
`spell "<name>": <op>: <message>`, the same wrapping a raise got. A Diagnostic with a code
becomes a coded `diagnostics.Error` carrying its url and fix, so it renders
`[CODE] message` with its `see:` line and `errors.Is` matches the code, exactly as the
caught host error did. A Result with neither side, or an error that is not a Diagnostic or
carries no message, is a malformed answer (`errMalformedAnswer`), never a success.

Every caller is unchanged: a failed Result reaches it as the error a raise did, so the
cache, lifecycle, review, secret, CI and workspace families read it the way they read a
raise, and exit codes do not move.

### An op that cannot fail returns `T`

`enabled`, `group_start`, a pure `discover_projects` parser: none of them has a failure to
return. The contract check accepts `> T` or `> result\Result::<T>` for each op, and nothing
else. Both are typed and checked; a Result there would declare a failure path nothing
takes.

### A raising op is refused

`checkContract` refuses an op that declares `!>` with MGS1051, naming the Result signature
to declare. The Buzz checker already refuses a call that may raise in a function without
`!>` (BZZ1006), so a loaded op cannot raise from a host call. A raise the VM still returns
(a `throw` in a source the contract check never saw, such as a test fixture handed to
`CallOp` directly) is the error as before: loud, never a value.

### What this converts

All 21 shipped ops that raised: `get_artifact`, `has_artifact`, `put_artifact` and `prune`
(`aws/s3-cache`); `list_lifecycles` (`endoflife-date`); `list_projects`
(`experimental/nx`); `get_artifact`, `has_artifact`, `put_artifact`, `last_green_run` and
`resolve_secret` (`github/actions`); `find_review`, `publish_review`, `review_threads`,
`reply_review` and `list_pulls` (`github/review`); `resolve_secret` (`onepassword`,
`system-keychain`); `discover_projects` (`python`, `typescript`). Their nine no-throw rows
are gone from `analyzers.yaml`.

Out of scope: the merge queue's provider script (`internal/queue/provider`) is not a spell,
calls its ops through `provider.call` with no Target, and keeps its rows; `git/hooks`,
`golang/golangci`, `golang/gomod` and the OpenCode runtime raise from functions a target or
hook calls, not from ops.

### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): a Result with neither a
  value nor an error, or an error without a message, is a malformed answer naming itself,
  not an empty success.
- [Provider I/O is Buzz](../design/judgment/provider-io-is-buzz.md): the spell still owns
  the HTTP and the vendor's words; Go only reads the shape of what failed.
- [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  the break is recorded in `changes/unreleased/spell-op-results.md`, and every shipped op
  converts in the same change.

## Alternatives

- **Keep accepting a raising op beside a Result one.** It lost: two failure channels for
  one op is the dual reader the conventions forbid, and the no-throw rows could never fall.
- **Require a Result from every op, failing or not.** It lost: `enabled` and the pure
  discovery parsers have no failure, and the Result would be ceremony. Reopen if an op
  family gains a failure every member must report.
- **Keep the VM's `buzz: uncaught error:` prefix on a failed Result.** It lost: nothing was
  uncaught, and the prefix named the VM rather than the op.

## Consequences

- An outside spell whose op declares `!>` fails MGS1051 at load. The fix is to return
  `result\Result::<T>`: `result\fail` where it threw a string, `result\caught(err)` where a
  host call raised. magus is pre-1.0; the changelog says so.
- An op that threw a string printed `buzz: uncaught error: <text>`; the same failure as a
  Result prints `<text>`. A host error an op caught and returned prints as it did when it
  raised through, code and url included.
