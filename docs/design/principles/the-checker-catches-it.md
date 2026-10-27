---
title: The checker catches it; the runtime is the backstop
order: 32
description: A mistake the Buzz checker, the spell contract, a generated decoder or a Go analyzer can find is refused at check time; a runtime refusal is for input the checker cannot see, and it names the typed fix.
tags: [design, principle, harness, checker, types]
---

# The checker catches it; the runtime is the backstop

A mistake that type-checks and fails only when the code runs costs the most
to find. The person who wrote it has moved on, the failure arrives far from
the line that caused it, and sometimes nothing fails at all. The code just does
the wrong thing, quietly. Eli's complaint about TypeScript is this one: too
many gotchas that show up at run time and never at the type checker. magus
spends its diligence so that a mistake is refused where it is written.

The rule has two halves:

- A mistake the Buzz checker, the spell contract, a generated typed decoder or
  a Go analyzer can find is refused at check time. If the checker lets it
  through, the checker has a gap, and the gap is the bug.
- A runtime refusal exists only for input the checker cannot see: a file, the
  network, a value read at run time. It names the typed fix, and it never
  falls back silently. See [No silent defaults](../judgment/no-silent-defaults.md)
  and [A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md).

A runtime check on something the checker could have seen is a missing rule.
Write the rule.

## Enforced by

- Every runtime refusal either names the check-time rule that should have
  caught it, or says why the checker cannot see that input (a file read, a
  network answer, a value that exists only at run time). A refusal that does
  neither is a sign the rule is missing.
- The Buzz checker rejects a comparison that cannot be true, such as an enum
  against a string (BZZ1005); `TestCommandRuleComparingAnEnumFieldToAStringFailsToLoad`
  pins it for guard rules.
- Provider ops are held to their typed signature at load
  ([typed provider calls](../../decisions/0011-typed-provider-calls.md)).
- Generated decoders are strict: an unknown or mistyped field is an error at
  the boundary, never a zero value.
- The lint that grades the runtime-refusal rule is not written yet. It would
  flag a refusal message that names neither a check-time rule nor the reason
  the checker is blind. Until it exists this is a debt in
  [Where this is strained](../strained.md).

## Good

A guard rule that compares an enum to a string literal does not load:

```text
BZZ1005: == between an enum and `str` is always false
```

The author sees it while writing the rule, on the line that caused it.

The spell contract is meant to work the same way. Environment variables go
through the typed env field, never through argv as a `NAME=value` token, an
`env` wrapper or `sh -c`. The intended refusal happens when the spell loads,
before any target runs. That refusal, in the spell contract and in `proc\exec`,
is planned and not built.

## Bad

```buzz
if (decision == "deny") {
    return fail();
}
```

`decision` was an enum. The comparison type-checked, compared false at run time
on every call, and the workspace guard rules built on it enforced nothing until
the fix on 10-27. No error, no log line. The checker could see the mismatch and
did not look, so a rule that was meant to refuse things refused nothing.

## See also

- [Enforcement, not pinky promises](enforcement-not-pinky-promises.md)
- [A harness is not an agent thing](a-harness-is-not-an-agent-thing.md)
- [No silent defaults](../judgment/no-silent-defaults.md)
- [A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md)
