---
title: Any other host
description: The host-neutral contract - install the guidance where your host reads it, pipe the event to magus hook, render the verdict with -o template - for an agent host magus does not document by name.
tags: [agents, guard, hooks, integration, template]
---

# Any other host

The four documented hosts are examples, not a fixed list. magus owns the guard
rules and the verdict, not integration code per host: maintaining a codec for
each one as the products keep changing would be a lot to carry for little gain.
So the host-specific part is a template or a few lines of config you control,
and adding a host is your edit rather than a new magus release.

Any host that can run a command and read its output fits.

| what            | where                                                 |
| --------------- | ----------------------------------------------------- |
| skills          | whichever directory your host discovers `SKILL.md` in |
| always-on rules | `AGENTS.md`, if your host reads one                   |
| guard wiring    | whatever pre-tool hook or plugin your host offers     |
| MCP             | [MCP](../mcp.md)                                      |

## Skills

Not every host reads the Agent Skills format. If yours discovers `SKILL.md`
directories, install into the one it names:

```sh
magus agent install --tar | tar -xf - -C <the directory your host reads>
```

If it reads only an instruction file, run `magus agent install` for the
`AGENTS.md` block it prints and paste that instead. [Skills](skills.md) covers
both.

## MCP

```sh
magus server start
```

Any client that takes a Streamable HTTP URL plus a bearer token can connect; see
[MCP](../mcp.md).

## Guard hook

The wiring is the same shape everywhere: get the command or path out of the host
event, hand it to `magus hook`, and render the verdict into the host's reply.

```sh
printf '%s' "$command" | magus hook -o 'template=<your host reply>'
```

If your host writes its payload as JSON with `tool_input.command` or
`tool_input.file_path`, pipe the payload in unchanged: magus reads the envelope
itself, infers a write from a file path, and picks up `session_id` and
`hook_event_name` for attribution. Otherwise select the field yourself - `jq -r
'.<path>'` is what the shipped templates use - and pass `--path` when the input
is a file.

The fastest start is to copy [`magus-guard-command.sh`](guard-templates.md) and
set its override variables: `HOST_EVENT_PATH`, `HOST_RESPONSE`,
`GUARD_AGENT_NAME`, and the two unavailable-response variables. That gets you
the missing-binary and broken-binary handling without writing it again.

Three decisions are yours to make:

- **Which channel carries a deny.** Most hosts have one that reaches the model.
- **Whether an advise can reach the model at all.** Some hosts deliver a message
  only on a denial; there, the advisory nudges live in the installed skills
  instead.
- **What happens when magus cannot be found or cannot judge.** Failing open
  keeps the session usable and is what every shipped template does. Say so
  visibly rather than exiting quietly, because an unguarded session you know
  about beats one you do not.

If you contribute the result back, add a `magus-guard-coverage:` line declaring
what your glue carries per surface and decision. A parity gate reads those lines
and fails the build when a host was never asked about a decision the contract
grew.

## Notifications

Wire any event that means a human is needed to `magus notify`; see [Attention hooks](notifications.md).

## Coverage and limits

Whatever your host's hook surface can carry. One binary produces the rules, so
they are identical everywhere, and what differs is only how much of a verdict
survives the trip to the model. [Parity across hosts](../agents.md#parity-across-hosts)
records that for the documented four.

## Verify

```sh
printf '%s' 'git stash' | magus hook -o name
magus doctor
```

The first proves magus judges at all from your shell. **guard wiring** in
`doctor` inventories the host config locations it knows and reports when none of
them invokes a current template.
