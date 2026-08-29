---
title: Any other host
description: The host-neutral contract - install the guidance where your host reads it, pipe the event to magus session hook, render the verdict with -o template - for an agent host magus does not document by name.
tags: [agents, guard, hooks, integration, template]
---

# Any other host

The four documented hosts are examples, not a fixed list. magus owns the guard
rules and the verdict, not integration code per host, so the host-specific part
is a template or a few lines of config you control, and adding a host is your
edit rather than a new magus release.

That is a [standing decision](../../../doctrine.md#the-host-wiring-is-yours)
rather than a gap waiting to be filled. A codec per host would cost us upkeep
as the products change, and it would cost you more than it costs us: wiring you
did not write is wiring you cannot repair on the afternoon your host changes
its event shape, and this guard fails OPEN, so a hook that quietly stopped
judging looks exactly like a session with nothing to deny. Read the template
once and it is yours.

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
event, hand it to `magus session hook`, and render the verdict into the host's reply.

```sh
printf '%s' "$command" | magus session hook -o 'template=<your host reply>'
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
  instead. Set `GUARD_NO_ADVISE` when your host does worse than ignore one -
  Codex treats the `additionalContext` key as an error and then fails OPEN, so
  sending an advisory it cannot take disarms the guard for that call. Suppressed,
  an advise renders nothing at all; `HOST_ADVISE_BRANCH` reshapes it instead if
  your host has some other channel.
- **What happens when magus cannot be found or cannot judge.** Failing open
  keeps the session usable and is what every shipped template does. Say so
  visibly rather than exiting quietly, because an unguarded session you know
  about beats one you do not.

If you contribute the result back, add a `magus-guard-coverage:` line declaring
what your glue carries per surface and decision. A parity gate reads those lines
and fails the build when a host was never asked about a decision the contract
grew.

## Lease capture

Separate from the guard, and the same shape: pipe the host's pre-tool event to
`magus session hook` when the tool being called is the one that hands work to a
sub-agent.

```sh
printf '%s' "$event" | magus session hook --agent-name <your host> >/dev/null 2>&1; exit 0
```

magus recognizes a lease by the FIELD it carries, never by a tool name it
would have to enumerate per host: a `tool_input` with a `prompt` is a spawn. It
reads the prompt as the handed context, takes the callee's label from
`subagent_type`, then `description`, then `tool_name`, and takes the parent's
session from `session_id`. If your host names those fields differently, reshape
the payload before piping it - `jq` is enough.

The ordering is deliberate: a payload carrying `command` or `file_path` is judged
as a command or a write exactly as before, and only one carrying neither is read
as a spawn. Adding this wiring cannot change a verdict you already had.

Nothing judges a lease. A prompt is prose, not a command line, so the
verdict is always a pass and a prompt that mentions a denied command is recorded
rather than blocked. Discard the output and exit 0.

### The event

One `agent_spawn` event per lease, in the same activity trail and the same
listing as every other kind - no new endpoint, and the console's activity filters
already select it.

| field             | what it carries                                                     |
| ----------------- | ------------------------------------------------------------------- |
| `time`            | when the handoff was observed                                       |
| `host`, `session` | the PARENT: the host you named, and its own session id              |
| `actor`           | `agent`                                                             |
| `action`          | the child's label, or `agent.spawn` when the payload named none     |
| `lease`           | the ledger lease, when the context declared one (see below)         |
| `request_ref`     | the handed context, fetched with `GetPayload`                       |
| `request_bytes`   | how much context was handed over                                    |
| `outcome`         | `ok` means the handoff was OBSERVED, never that the child succeeded |

The context is a blob, not a field. A lease prompt runs to kilobytes and can
carry anything the orchestrator pasted into it, so the event line holds a
reference and the body is fetched deliberately, one row at a time. It is redacted
through the same resolver as every other trail write, but it is still durable
prose on disk: treat the activity trail as readable by anyone who can read the
workspace cache.

### Correlating a spawn to a lease

Correlation is COOPERATIVE. No host event names a magus lease and magus will not
guess one from prose, so an orchestrator that wants the join writes ONE marker:

```text
lease: <id>
```

It must be the FIRST non-blank line of the handed context, and its trimmed text
must be exactly that. The id is a bare token of letters, digits and the
separators `-` `_` `.` `/` `:`, at most 128 characters, with nothing after it on
the line. Leading blank lines are skipped; the head of the context is capped at
4096 bytes, so a marker cannot hide behind a pathological first line.

Leading the prompt is the contract, not a convention. A lease prompt
routinely quotes a ledger listing, a file, or another agent's transcript, and a
`lease:` line lifted from any of them would stamp the event with a lease this
handoff has nothing to do with. Position is what separates a marker you wrote
from one you pasted.

Anything else leaves `lease` empty: no marker, an id with prose after it, an id
carrying other punctuation, a marker below the first line. That is a missing
join, not an error, and it is the designed outcome for an orchestrator that
never opted in.

## Notifications

Wire any event that means a human is needed to `magus session notify`; see [Attention hooks](notifications.md).

## Coverage and limits

Whatever your host's hook surface can carry. One binary produces the rules, so
they are identical everywhere, and what differs is only how much of a verdict
survives the trip to the model. [Parity across hosts](../agents.md#parity-across-hosts)
records that for the documented four.

## Verify

```sh
printf '%s' 'git stash' | magus session hook -o name
magus doctor
```

The first proves magus judges at all from your shell. **guard wiring** in
`doctor` inventories the host config locations it knows and reports when none of
them invokes a current template.
