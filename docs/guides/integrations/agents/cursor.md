---
title: Cursor
description: Wiring magus into Cursor - AGENTS.md for guidance, one self-contained hook script for both of its events, and the two places Cursor carries less of a verdict than other hosts.
tags: [agents, cursor, AGENTS.md, guard, hooks]
---

# Cursor

Cursor does not read Agent Skills directories. It reads an `AGENTS.md` at the
repository root, and it runs hooks as programs rather than inline shell strings.
One self-contained script covers both of its relevant events, so installing the
guard is a single download.

| what            | where                                                    |
| --------------- | -------------------------------------------------------- |
| always-on rules | `AGENTS.md` (you paste the block; magus never writes it) |
| guard wiring    | `.cursor/hooks.json`                                     |
| command surface | deny reaches the model; advise is not delivered          |
| file surface    | reported after the write, to the person only             |
| MCP             | [MCP](../mcp.md)                                         |

## Skills

There is no skills directory to install into. Run `magus agent install` anyway:
it prints the managed magus block when your `AGENTS.md` is missing it or
carrying a stale one, and you paste it in. [Skills](skills.md) covers the block,
its stamp, and the drift check that grades it.

The nudges an `advise` verdict would carry live in that guidance, because Cursor
cannot deliver them at the moment the command runs.

## MCP

```sh
magus server start
```

See [MCP](../mcp.md) for the client configuration and token.

## Guard hook

Save the script below as `.cursor/hooks/cursor-guard.sh`, make it executable,
and point both events at it:

```json
{
  "version": 1,
  "hooks": {
    "beforeShellExecution": [{ "command": "./.cursor/hooks/cursor-guard.sh" }],
    "afterFileEdit": [{ "command": "./.cursor/hooks/cursor-guard.sh" }]
  }
}
```

It is deliberately self-contained rather than delegating to the shared
templates: needing three files to install a guard is how a guard ends up not
installed.

```sh
#!/usr/bin/env sh
# magus guard for Cursor. ONE file, both hooks - download only this.
#
# Cursor runs a hook as a PROGRAM rather than an inline shell string, and its two
# relevant events carry different payloads, so this reads the event once and
# branches on what it finds instead of needing a wrapper per event:
#
#   beforeShellExecution  {"command": "...", "cwd": "...", "sandbox": false}
#   afterFileEdit         {"file_path": "<absolute>", "edits": [...]}
#
# Save to .cursor/hooks/cursor-guard.sh, chmod +x, and point both events at it:
#
#   {"version": 1, "hooks": {
#     "beforeShellExecution": [{"command": "./.cursor/hooks/cursor-guard.sh"}],
#     "afterFileEdit":        [{"command": "./.cursor/hooks/cursor-guard.sh"}]}}
#
# Self-contained on purpose. The other hosts' templates delegate to
# magus-guard-command.sh, but Cursor would then need three files downloaded to
# work, and a guard nobody finishes installing guards nothing.
#
# Two Cursor facts shape the behavior:
#
#   - A denial carries BOTH user_message (shown to you) and agent_message (sent
#     to the model); neither is delivered on an allow, so `advise` collapses to a
#     plain allow here. Those nudges live in the installed skills instead.
#   - There is NO pre-write file hook. For an advise that costs nothing: magus
#     advises on generated files rather than blocking them, so reporting after
#     the write is the intended behavior everywhere, and what Cursor shaped is
#     only the CHANNEL - stderr prose here, injected context elsewhere. For a
#     DENY it costs the block itself: afterFileEdit fires once the write has
#     landed, so the verdict arrives as a warning to the person and the file is
#     already changed. That is a real coverage gap, recorded as deny=human
#     below rather than papered over.
#
# Both calls pass --agent-name cursor so the observation magus records says which host
# produced it. Neither Cursor event carries a session id, so none is sent; that
# is attribution missing, not a verdict changing.
#
# Coverage declarations, machine-read by the host-parity gate - see the longer
# note in magus-guard-command.sh. Cursor is the host that loses the most, and
# the two lines say exactly where: an advise on a shell command is delivered
# nowhere (Cursor sends nothing on an allow), and an advise on a file write
# reaches the person via stderr but never the model.
# magus-guard-template: 5
# magus-guard-coverage: schema=1 host=cursor surface=command deny=model advise=none pass=none
# magus-guard-coverage: schema=1 host=cursor surface=path deny=human advise=human pass=none

# Prefer the workspace's own ./magus over PATH. A repository that builds magus, or pins a
# newer one than is installed, keeps its RULES in that binary - and an older PATH copy does
# not fail loudly when it lacks them. It does not recognize the config key that ARMS a rule,
# warns about an unknown field, and returns pass: silent non-enforcement at exit 0. Measured
# 2026-08-13, when a write into a declared notes store was allowed by a binary that predated
# the knowledge.notes key while `magus doctor` reported the guard as fine.
[ -n "$GUARD_MAGUS_BIN" ] || { [ -x ./magus ] && GUARD_MAGUS_BIN=./magus; }
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)

event=$(cat)

case "$event" in
*'"file_path"'*)
    # afterFileEdit: cannot block, so a missing magus costs a warning, not safety.
    if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
        exit 0
    fi
    # Ask for the MESSAGE, not the bare decision word. Several rules judge this
    # surface now, so a hardcoded explanation here would report the wrong one -
    # it used to say "that file is a DECLARED OUTPUT" whatever had actually
    # fired. The template renders the deny reason or the advise context, and
    # nothing at all for a pass. magus re-roots the absolute path Cursor sends
    # onto the workspace itself.
    verdict=$(printf '%s' "$event" | jq -r '.file_path' | "$GUARD_MAGUS_BIN" hook --path --agent-name cursor \
        -o 'template={{if eq .decision "deny"}}{{.reason}}{{else if eq .decision "advise"}}{{.context}}{{end}}' 2>/dev/null)
    [ -n "$verdict" ] || exit 0
    # Cursor surfaces a non-blocking hook's stderr, so the message goes there as
    # prose rather than as a verdict it would not read. A deny cannot block here
    # - afterFileEdit fires after the write - so it is reported as what it is.
    printf '%s\n' "magus: $verdict" \
        "Cursor has no pre-write hook, so this could only be reported after the fact." >&2
    exit 0
    ;;
esac

# beforeShellExecution. Allow on a missing magus: Cursor already fails open on a
# hook crash or malformed JSON unless the hook sets failClosed, so pretending
# otherwise would give false assurance. For strict behavior, set failClosed on
# the hook and change this to a deny.
if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
    printf '%s' '{"permission":"allow"}'
    exit 0
fi

# Captured and printed rather than piped straight through, because `magus hook` exits
# non-zero on a deny and Cursor reads a non-zero hook as a CRASH - which it fails open on,
# unless failClosed is set. Letting that status escape would turn every block into an
# allow, silently, which is the one outcome worse than not installing the guard. Cursor's
# channel is the JSON on stdout; this exits 0 so that JSON is what it acts on.
verdict=$(printf '%s' "$event" | jq -r '.command' | "$GUARD_MAGUS_BIN" hook --agent-name cursor \
    -o 'template={{if eq .decision "deny"}}{"permission":"deny","user_message":{{toJson .reason}},"agent_message":{{toJson .reason}}}{{else}}{"permission":"allow"}{{end}}' 2>/dev/null)
[ -n "$verdict" ] || verdict='{"permission":"allow"}'
printf '%s' "$verdict"
exit 0
```

## Notifications

Cursor can run a command on its agent hook surface. Shape the event into the
canonical envelope and pipe it to `magus notify`; see [Attention hooks](notifications.md).

## Coverage and limits

Two gaps, and they are different in kind.

**`advise` on a shell command is not delivered at all.** Cursor sends
`user_message` and `agent_message` only on a denial, so an advisory collapses to
a plain allow. This is why the same guidance ships in the installed skills.

**A `deny` on the file surface cannot block.** Cursor's documented events are
`beforeReadFile`, which blocks, and `afterFileEdit`, which fires once the write
has landed. The script reports the verdict to the person on stderr and records
its coverage as `deny=human` rather than claiming a parity Cursor does not have.

Reporting a declared-output edit after the fact is not a concession: that rule
only ever explains, on every host. What differs is the channel - injected
context where a host has one, stderr prose where it does not.

**Delegation capture is not available.** Cursor's documented hooks are
`beforeShellExecution`, `beforeReadFile` and `afterFileEdit` - all of them
tool-specific, and none of them carrying a prompt handed to a sub-agent. There is
no generic pre-tool event to attach to, so there is nothing to pipe. Even if one
arrived, neither Cursor event carries a session id, so the parent side of the
handoff would be unattributable.

Cursor fails open on a hook crash or malformed JSON unless the hook sets
`failClosed`. The script above matches that stance instead of pretending to be
stricter than the surrounding contract. For strict behavior, set `failClosed` on
the hook and change its missing-binary branch to a deny.

The magus half is verified: the verdict shape this script parses is checked
against this repository's binary. The Cursor half is written against the
product's published hook documentation and has not been executed here, so
confirm it against [Cursor hooks](https://docs.cursor.com/agent/hooks).

## Verify

```sh
magus doctor
```

**guard binary** names the binary a hook would resolve; **guard wiring** runs a
canary command through it and checks that a host config invokes a template whose
version marker is current.
