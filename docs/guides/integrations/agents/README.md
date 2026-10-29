---
title: Hook bindings
description: Which glue each agent host runs at each hook point, and why a host that runs none says so.
tags: [agents, hooks, guard]
---

# Hook bindings

<!-- Generated from each harness spell's mgs_listHooks and mgs_listUnsupportedHooks by
`magus run generate:rw docs/guides/integrations/agents`. Edit the spells, not this page. -->

Every harness magus ships answers every hook point: a cell names the glue the host runs
there, or says the host cannot run one. An unsupported point cites the host's docs, or
says what nobody has verified yet. `magus doctor` fails `harness-hook-bindings` on a point
a harness leaves unanswered.

| hook point      | claude-code       | claude-code-mod | codex                            | cursor        | opencode          |
| --------------- | ----------------- | --------------- | -------------------------------- | ------------- | ----------------- |
| `shell`         | `magus-command`   | unsupported     | `magus-command`                  | `cursor-hook` | `opencode-plugin` |
| `edit`          | `magus-command`   | unsupported     | `magus-command`                  | `cursor-hook` | `opencode-plugin` |
| `stop`          | `magus-command`   | unsupported     | `magus-command`                  | unsupported   | `opencode-plugin` |
| `subagent-stop` | `magus-command`   | unsupported     | `magus-command`                  | unsupported   | unsupported       |
| `spawn`         | `magus-command`   | unsupported     | `magus-command, magus-rehydrate` | `cursor-hook` | `opencode-plugin` |
| `session-start` | `magus-session`   | unsupported     | `magus-rehydrate`                | `cursor-hook` | `opencode-plugin` |
| `compact`       | `magus-rehydrate` | unsupported     | `magus-rehydrate`                | `cursor-hook` | unsupported       |
| `mcp`           | `magus-command`   | unsupported     | `magus-command`                  | `cursor-hook` | `opencode-plugin` |
| `attach`        | unsupported       | unsupported     | unsupported                      | unsupported   | unsupported       |

## Unsupported points

- claude-code, `attach`: not verified: that the host shows an SVG magus writes inline; re-check once libs/figure output renders standalone
- claude-code-mod, `shell`: the mod wires settings only; spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `edit`: the mod wires settings only; spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `stop`: the mod wires settings only; spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `subagent-stop`: the mod wires settings only; spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `spawn`: the mod wires settings only; spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `session-start`: the mod wires settings only; spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `compact`: the mod wires settings only; spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `mcp`: the mod wires settings only; spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `attach`: the mod wires settings only; spells/harness/claude-code binds Claude Code's hooks
- codex, `attach`: not verified: that the host shows an SVG magus writes inline; re-check once libs/figure output renders standalone
- cursor, `stop`: not verified: that a Cursor stop hook reply returns the agent to work; testdata/hosts/cursor vendors no stop output schema
- cursor, `subagent-stop`: not verified: that Cursor's subagentStop payload names the child's transcript; testdata/hosts/cursor vendors no subagentStop schema
- cursor, `attach`: not verified: that the host shows an SVG magus writes inline; re-check once libs/figure output renders standalone
- opencode, `subagent-stop`: not verified: that a child session's idle carries the context it leaves; session.idle names only the session (opencode.ai/docs/plugins)
- opencode, `compact`: experimental.session.compacting only feeds the summarizer (opencode.ai/docs/plugins); opencode-plugin.ts records why the brief stays out of it
- opencode, `attach`: not verified: that the host shows an SVG magus writes inline; re-check once libs/figure output renders standalone