---
title: Test selection in job checks
description: How job checks resolve a spell's test-selection dialect, and when narrowed test evidence is accepted.
tags: [jobs, tests, spells, test selection]
---

# Test selection in job checks

A job check names a target and a project. The target's name does not identify its
language: a JavaScript project's `test` does not acquire Go's `-run` or `-skip`
semantics because a Go project uses the same name.

The Go spell declares `testSelection = "go"` on the command returned by its
`go-test` op. The field describes the forwarded selector dialect. An empty field
declares none; an unsupported nonempty dialect is a spell-load error.

Magus resolves this declaration through the requested project's bound spells.
It recognizes a raw op, an explicit op such as `go::go-test`, and a target whose
body only calls spell ops with its own context. For example, a target named
`check_cases` that calls `go["go-test"](ctx)` inherits the same selector dialect.
Charm suffixes do not change that declaration. Script checks keep their own argv.

For resolved Go checks, job validation reads the tests in the packages of the
write paths and reports tests excluded by `-run` or `-skip`. Checks explicitly
scoped to exactly those packages may narrow their tests, leaving the full run to
integration. Verification can accept a recorded run with different Go selection
patterns when all other arguments match; its result names the scope used.

A complex target may invoke a Go op while replacing its arguments or forwarding
none of them. A `ctx.needs` dependency also clears forwarded arguments. Magus
therefore does not infer a selector from an op merely appearing in such a body.
It reports the test-selection check as unchecked, and selector-specific evidence
must match the check's arguments when the run records them. Historical run records
without argument metadata retain their existing treatment. This includes this repository's complex root
`test` target, even though its source explicitly forwards Go selectors.

Use a qualified spell op or a target that only forwards its own context when you
need selector-aware validation and evidence. Missing workspace metadata likewise
leaves selection unchecked and does not authorize relaxed argument matching.
