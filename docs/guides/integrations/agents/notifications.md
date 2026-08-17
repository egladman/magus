---
title: Attention hooks
description: magus notify turns one host event into a desktop notification, so a blocked or finished agent reaches you - and why this is a hook sink rather than an MCP tool.
tags: [agents, notify, hooks, notifications, desktop]
---

# Attention hooks

An agent blocked on a permission prompt, or one that finished twenty minutes
ago, is only useful if you find out. `magus notify` normalizes one host event
and, with `--desktop`, posts a desktop notification.

It does not publish an event to the daemon or Console. Use it to bring a
person back to the host where the agent needs an answer.

```sh
printf '%s\n' "needs your approval" | magus notify --outcome Notification --desktop
printf '%s\n' "finished" | magus notify --outcome Stop -o json
```

## Why this is not an MCP tool

An MCP server only ever observes tool calls. A blocked agent makes no call at
all - the blockage IS the silence, and silence is precisely what MCP has no way
to report. The host's own hook system is the only surface that fires on it. So
this is a hook sink rather than a tool, and it stays one whether or not the
daemon is up.

## The envelope

`--outcome` takes whatever the host calls its event. It is classified by
substring, case-insensitively, into canonical outcomes: `waiting`,
`permission`, `failed`, `finished`, `diagnostic`, `update`. Anything
unrecognized becomes `other` and still notifies, because a missed alert is the
failure this exists to prevent.

When a host hands you JSON, shape it into the canonical envelope before piping
it in. The command deliberately does not know host field names:

```sh
jq -c '{schema_version: 1, outcome: .hook_event_name, source: {kind: "agent"}, message: .message}' \
  | magus notify --desktop
```

## Wiring it per host

The event names below are the ones each host documents. Check yours against its
current documentation, because these move; the magus side never changes.

| host                          | event(s) to wire                         |
| ----------------------------- | ---------------------------------------- |
| [Claude Code](claude-code.md) | `Notification`, `Stop`, `SubagentStop`   |
| [Codex](codex.md)             | its hook or notify program setting       |
| [OpenCode](opencode.md)       | its plugin surface                       |
| [Cursor](cursor.md)           | its agent hook surface                   |
| [any other host](any-host.md) | any event that means "a human is needed" |

Claude Code's `Notification` fires both on a permission prompt and on idle
waiting for input; its event JSON carries `hook_event_name`, `message` and
`session_id`. [Its page](claude-code.md) has the config.

## Fail quietly, on purpose

A notifier hook should exit 0 and swallow its own output. A notifier that can
fail is a hook that can break the session it was meant to watch - the same
reasoning as the guard's fail-open contract.

Resolve the binary the way the guard does: prefer a repo-local `./magus`, then
PATH, and do nothing if neither exists. Do not fall back to a fixed path like
`/tmp/magus`; a stale binary there runs happily and enforces months-old rules
while looking perfectly healthy. `magus doctor`'s **guard binary** check reports
which binary a hook would actually run and fails when it is older than your
working tree.
