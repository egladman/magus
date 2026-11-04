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
| `stop`          | `magus-command`   | unsupported     | `magus-command`                  | `cursor-hook` | `opencode-plugin` |
| `subagent-stop` | `magus-command`   | unsupported     | `magus-command`                  | `cursor-hook` | `opencode-plugin` |
| `spawn`         | `magus-command`   | unsupported     | `magus-command, magus-rehydrate` | `cursor-hook` | `opencode-plugin` |
| `session-start` | `magus-session`   | unsupported     | `magus-rehydrate`                | `cursor-hook` | `opencode-plugin` |
| `compact`       | `magus-rehydrate` | unsupported     | `magus-rehydrate`                | `cursor-hook` | `opencode-plugin` |
| `mcp`           | `magus-command`   | unsupported     | `magus-command`                  | `cursor-hook` | `opencode-plugin` |
| `attach`        | unsupported       | unsupported     | unsupported                      | unsupported   | unsupported       |

## Unsupported points

- claude-code, `attach`: a hook answers with text only (additionalContext, systemMessage); no output field carries an image or a file (<https://code.claude.com/docs/en/hooks>)
- claude-code-mod, `shell`: the mod wires the extraKnownMarketplaces and enabledPlugins settings only (<https://code.claude.com/docs/en/settings>); spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `edit`: the mod wires the extraKnownMarketplaces and enabledPlugins settings only (<https://code.claude.com/docs/en/settings>); spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `stop`: the mod wires the extraKnownMarketplaces and enabledPlugins settings only (<https://code.claude.com/docs/en/settings>); spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `subagent-stop`: the mod wires the extraKnownMarketplaces and enabledPlugins settings only (<https://code.claude.com/docs/en/settings>); spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `spawn`: the mod wires the extraKnownMarketplaces and enabledPlugins settings only (<https://code.claude.com/docs/en/settings>); spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `session-start`: the mod wires the extraKnownMarketplaces and enabledPlugins settings only (<https://code.claude.com/docs/en/settings>); spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `compact`: the mod wires the extraKnownMarketplaces and enabledPlugins settings only (<https://code.claude.com/docs/en/settings>); spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `mcp`: the mod wires the extraKnownMarketplaces and enabledPlugins settings only (<https://code.claude.com/docs/en/settings>); spells/harness/claude-code binds Claude Code's hooks
- claude-code-mod, `attach`: the mod wires the extraKnownMarketplaces and enabledPlugins settings only (<https://code.claude.com/docs/en/settings>); spells/harness/claude-code binds Claude Code's hooks
- codex, `attach`: a hook answers with text only (systemMessage, additionalContext); no output field carries an image or a file (<https://learn.chatgpt.com/docs/hooks>)
- cursor, `attach`: no hook accepts or returns an image or a file inline; attachments arrive by path and replies carry text (<https://cursor.com/docs/agent/hooks>)
- opencode, `attach`: not verified: <https://unpkg.com/@opencode-ai/sdk@1.18.30/dist/gen/types.gen.d.ts> 2026-10-29: a completed tool part carries attachments (FilePart), but no page says an SVG one renders inline
