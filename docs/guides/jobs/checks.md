---
title: Test selection in job checks
description: How job checks resolve a spell's test-selection dialect, and when narrowed test evidence is accepted.
tags: [jobs, tests, spells, test selection]
---

# Test selection in job checks

A job check names a target and a project. The target's name does not identify its
language: a JavaScript project's `test` does not acquire Go's `-run` or `-skip`
semantics because a Go project uses the same name.

The Go spell sets `testSelection = "go"` on the command its `go-test` op returns.
The field names the selector dialect that the check forwards. An empty field names
none; magus refuses to load a spell that sets an unsupported nonempty dialect.

Magus reads that field through the spells bound to the requested project. It
recognizes a raw op, an explicit op such as `go::go-test`, and a target whose body
only calls spell ops with its own context. For example, a target named
`check_cases` that calls `go["go-test"](ctx)` inherits the same selector dialect.
Charm suffixes do not change the field. Script checks keep their own argv.

For Go checks that resolve this way, job validation reads the tests in the packages
of the write paths and reports tests that `-run` or `-skip` exclude. A check scoped
to exactly those packages may narrow its tests and leave the full run to
integration. Verification can accept a recorded run whose Go selection patterns
differ when all other arguments match; its result names the scope it used.

A complex target may call a Go op while it replaces the arguments or forwards none
of them. A `ctx.needs` dependency also clears forwarded arguments. Magus therefore
does not infer a selector from an op that merely appears in such a body. It reports
the test-selection check as unchecked, and selector-specific evidence must match the
check's arguments when the run records them. Run records from before argument
metadata existed keep their existing treatment. This includes this repository's
complex root `test` target, even though its source explicitly forwards Go selectors.

Use a qualified spell op, or a target that only forwards its own context, when you
need selector-aware validation and evidence. When workspace metadata is missing,
magus leaves selection unchecked and does not relax argument matching.
