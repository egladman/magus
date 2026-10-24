---
title: The host wiring is yours
order: 2
description: magus owns the guard rules and the verdict; the part that knows your agent host is a template you copy and edit.
tags: [design, judgment, agents, hooks]
---

# The host wiring is yours

magus owns the guard rules and the verdict. It does not own an integration with
your agent host. The rules come from one binary and are identical everywhere;
the part that knows your host (which event field carries the command, which
reply channel reaches the model, what happens when the binary cannot be found)
is a template you copy and edit. Adding a host is your change, not a magus
release.

The contract is small enough to hold in your head. `magus shell` takes
the thing to judge on stdin, either plain text or a payload keyed by FIELD
(`tool_input.command`, `file_path`, `prompt`), never by a host name or a tool
name. `-o template=` renders the verdict into whatever shape the host's reply
takes.

A layer that made every host work with no friction is what is being traded away
here, and it is worth being plain about the trade. Agent hosts change their
event shapes and hook names on their own schedule. A workspace whose
integration lives inside magus waits for a magus release to get unblocked; a
workspace that owns fifteen lines of shell edits them that afternoon. The cost
is charged up front and on purpose: you read your host's hook documentation
once, and there is no zero-configuration path.

The second cost of such a layer is the one nobody sees until it matters. A
guard you did not wire is a guard whose failure mode you do not know, and this
one fails OPEN by design, so a hook that quietly stopped judging looks exactly
like a session with nothing to deny. So a shipped template announces its
fail-open arms rather than exiting quietly. One template is exempt, and the test
declares it rather than leaving it to be discovered: the path template stays
silent. An empty response from a file-write rule already means allow, a notice
on every file edit was judged the worse noise, and hearing about it there is
opt-in. Whoever runs the guard should be able to read what it does and repair
it without us.

The same holds for credentials: the guard is a seatbelt, not a boundary against
a process running as you, which can read any file magus can. So the guard
refuses what an agent should not do, and magus separately enforces every layer
it can prove, a planted token file that cannot grant more than a mint, a token
that never mints a wider one, rather than claiming a sandbox it does not have
([Tokens and grants](../../concepts/tokens.md#trust-model)).

## Enforced by

- The `hostagnostic` linter fails the build when a host's NAME appears in code
  anywhere but a path on disk.
- The `hostvocab` linter fails the build when a host's word for a tool appears
  as a string literal in guard code at all, so a lookup table is no cheaper
  than a switch. The activity-event tool labels are magus's own vocabulary for
  that reason, chosen by which flag the wrapper passed.
- The `magus-guard-coverage:` line each template carries feeds a parity gate
  that fails when a host was never asked about a decision the contract grew.
- `TestFailOpenArmsAnnounceThemselves` fails the build when a template stops
  announcing a fail-open arm.

## Good

The verdict is keyed by field, and the template owns the host's reply shape:

```sh
printf '%s' "$payload" | magus shell -o template='{{.decision}}: {{.reason}}'
```

## Bad

```go
switch tool {
case "Read", "Bash":
    return judgeShell(input)
}
```

A branch on one host's tool names is a per-host branch in everything but
spelling. The name check cannot see it, and the next host that calls the tool
something else is silently unjudged.

## See also

- [The outer harness stays outside](the-outer-harness-stays-outside.md)
- [Agents guide](../../guides/integrations/agents/session-load.md)
