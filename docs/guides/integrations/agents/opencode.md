---
title: OpenCode
description: Wiring magus into OpenCode - skills in .opencode/skills, the TypeScript plugin that carries both guard surfaces, and where an advise verdict stops.
tags: [agents, opencode, skills, guard, plugin]
---

# OpenCode

OpenCode discovers Agent Skills and intercepts tool calls through a plugin
rather than a hook config. Throwing from `tool.execute.before` blocks the call,
so a deny reaches the model as the tool error; an advise has nowhere to go
except the log.

| what            | where                                                   |
| --------------- | ------------------------------------------------------- |
| skills          | `.opencode/skills/` (it also reads `.claude/skills/`)   |
| guard wiring    | `~/.config/opencode/plugins/` or `.opencode/plugins/`   |
| command surface | deny reaches the model; advise is logged for the person |
| file surface    | deny reaches the model; advise is logged for the person |
| MCP             | [MCP](../mcp.md)                                        |

## Skills

```sh
magus agent install .opencode/skills
```

If you already installed into `.claude/skills` for Claude Code, OpenCode reads
those too and this step is optional. [Skills](skills.md) covers the rest of the
install surface.

## MCP

```sh
magus server start
```

See [MCP](../mcp.md) for the client configuration and token.

## Guard hook

Save the plugin below to `~/.config/opencode/plugins/` or `.opencode/plugins/`,
then confirm OpenCode loaded it:

```sh
opencode debug config
```

It encodes no magus rule. Every decision comes from `magus session hook`, which keeps it
host-only glue rather than a second rule set that drifts out of step with the
other templates.

```ts
// magus guard hook for OpenCode. OpenCode has no shell-command hook config: a
// plugin intercepts tool calls instead, and throwing from tool.execute.before
// blocks the call.
//
// This file is the source of truth. Copy it to ~/.config/opencode/plugins/ (or
// .opencode/plugins/) and adjust to taste; nothing in it is magus-internal.
//
// It encodes no magus rule. Every decision comes from `magus session hook`, so
// this stays host-only glue rather than a second rule set that drifts out of
// step with the other hosts' templates. `--agent-name opencode` only labels the
// observation magus records; it cannot change a verdict.
//
// Covers BOTH guard surfaces, so OpenCode gets the same rules Claude Code does:
//   bash          the command rules
//   edit | write  the declared-output rule
//
// One handler (`apply`) serves both, so both carry a verdict the same way: a
// deny throws and its reason reaches the model as the tool error, while an
// advise can only be logged for the human because OpenCode has no
// context-injection arm. That is what the two declarations below record, and
// they are machine-read by the host-parity gate - see the longer note in
// magus-guard-command.sh.
// magus-guard-template: 7
// magus-guard-coverage: schema=1 host=opencode surface=command deny=model advise=human pass=none
// magus-guard-coverage: schema=1 host=opencode surface=path deny=model advise=human pass=none
//
// PATH contract: this shells out to `magus` by name, inheriting PATH from the
// opencode process. If magus lives in a prefix PATH does not include (mise,
// brew, asdf, ~/.local/bin), set GUARD_MAGUS_BIN to an absolute path. That name
// deliberately avoids the MAGUS_* space, which is magus's own config surface.

import { existsSync } from "node:fs";
import type { Plugin } from "@opencode-ai/plugin";

/** The verdict schema this plugin understands; a bump means re-read the docs. */
const SUPPORTED_SCHEMA = 1;

/**
 * magus's guard verdict. A discriminated union on `decision`, so the compiler
 * enforces that `reason` is only read on a deny and `context` on an advise.
 */
type Verdict =
  | { schema_version: number; decision: "pass" }
  | { schema_version: number; decision: "advise"; context: string }
  | { schema_version: number; decision: "deny"; reason: string };

/**
 * Narrows untrusted JSON to a Verdict. A type guard rather than a cast because
 * this is another process's stdout: a cast would let a malformed payload reach
 * the branches below as though it had been checked.
 */
function isVerdict(value: unknown): value is Verdict {
  if (typeof value !== "object" || value === null) return false;
  const fields = value as Record<string, unknown>;
  if (typeof fields.schema_version !== "number") return false;
  switch (fields.decision) {
    case "pass":
      return true;
    case "advise":
      return typeof fields.context === "string";
    case "deny":
      return typeof fields.reason === "string";
    default:
      return false;
  }
}

/** First non-empty string among `keys` in a tool's untyped args, else "". */
function argString(args: unknown, keys: readonly string[]): string {
  if (typeof args !== "object" || args === null) return "";
  const fields = args as Record<string, unknown>;
  for (const key of keys) {
    const value = fields[key];
    if (typeof value === "string" && value.length > 0) return value;
  }
  return "";
}

export const MagusGuard: Plugin = async () => {
  // Prefer the workspace's own ./magus over PATH, for the reason spelled out in
  // magus-guard-command.sh: an older PATH binary does not fail when it lacks a rule, it
  // fails to recognize the config key that arms the rule and returns pass.
  const magus = process.env.GUARD_MAGUS_BIN ?? (existsSync("./magus") ? "./magus" : "magus");

  /**
   * Runs one `magus session hook` invocation and returns its raw stdout, or null when the
   * binary could not be run at all (missing, not executable). An older binary that
   * rejects a flag still runs and exits, so that case comes back as "" here, not
   * null - the caller distinguishes them.
   */
  const runOnce = async (args: readonly string[], input: string): Promise<string | null> => {
    try {
      const proc = Bun.spawn([magus, ...args], {
        stdin: new TextEncoder().encode(input),
        stdout: "pipe",
        stderr: "ignore",
      });
      const stdout = await new Response(proc.stdout).text();
      await proc.exited;
      return stdout;
    } catch {
      return null;
    }
  };

  /**
   * Runs one guard query. Returns null when no verdict could be obtained, which
   * every caller treats as allow.
   *
   * Failing OPEN is deliberate. Throwing is OpenCode's only way to stop a call,
   * so a guard that threw whenever magus was missing would block every tool
   * call and make the session unusable - worse than no guard. The failure is
   * logged rather than swallowed, so an unguarded session stays visible.
   *
   * The thing being judged goes in on STDIN, never in argv. `magus session hook` takes
   * no positional arguments at all, and that is not an incidental preference:
   * a command is arbitrary text, and a shell command passed as an argument is
   * one quoting mistake away from being re-parsed. Passing it in argv does not
   * misjudge the command, it gets NO verdict - which fails open, quietly, on
   * every call.
   *
   * `--agent-name` in `args` is ATTRIBUTION, not policy (see the header comment), and
   * postdates the current release this plugin is downloaded and run against.
   * Passing it unconditionally does not degrade the guard, it BREAKS it - an older
   * binary rejects the unknown flag and prints its usage, so `-o json` comes back
   * empty and every verdict silently disappears. So: try as called, and on an
   * empty reply - which under `-o json` only happens when the call itself failed,
   * since even a pass renders `{"decision":"pass",...}` - retry with `--agent-name` and
   * its value stripped out. Same shape as magus-guard-command.sh's `guard()`
   * fallback, fixed there after the same gap (memory:
   * agent-host-attribution-not-captured) and ported here so this plugin degrades
   * the same way.
   */
  const judge = async (args: readonly string[], input: string): Promise<Verdict | null> => {
    const unguarded = () =>
      console.warn(
        `[magus guard] could not run ${magus}; this call is UNGUARDED. ` +
          "Install magus, or set GUARD_MAGUS_BIN to its path.",
      );

    let stdout = await runOnce(args, input);
    if (stdout === null) {
      unguarded();
      return null;
    }
    if (stdout.trim() === "") {
      const nameIndex = args.indexOf("--agent-name");
      const withoutName =
        nameIndex === -1 ? args : [...args.slice(0, nameIndex), ...args.slice(nameIndex + 2)];
      stdout = await runOnce(withoutName, input);
      if (stdout === null) {
        unguarded();
        return null;
      }
    }

    let parsed: unknown;
    try {
      parsed = JSON.parse(stdout);
    } catch {
      console.warn("[magus guard] verdict was not JSON; allowing");
      return null;
    }
    if (!isVerdict(parsed)) {
      console.warn("[magus guard] unrecognized verdict shape; allowing");
      return null;
    }
    if (parsed.schema_version !== SUPPORTED_SCHEMA) {
      console.warn(
        `[magus guard] verdict schema ${parsed.schema_version} differs from the expected ` +
          `${SUPPORTED_SCHEMA}; allowing. Update this plugin from the magus docs.`,
      );
      return null;
    }
    return parsed;
  };

  /** Throws on a deny; logs an advise, which OpenCode cannot inject as context. */
  const apply = (verdict: Verdict | null): void => {
    if (verdict === null) return;
    switch (verdict.decision) {
      case "deny":
        throw new Error(`[magus guard] ${verdict.reason}`);
      case "advise":
        // OpenCode has no context-injection arm, so this cannot reach the
        // model. Logging keeps it in front of the human instead of dropping it;
        // the same guidance ships in the installed skills, which is why the
        // skills and the guard say the same things.
        console.warn(`[magus guard] ${verdict.context}`);
        return;
      case "pass":
        return;
    }
  };

  return {
    "tool.execute.before": async (input, output) => {
      if (input.tool === "bash") {
        const command = argString(output.args, ["command"]);
        if (command === "") return;
        apply(await judge(["session", "hook", "--agent-name", "opencode", "-o", "json"], command));
        return;
      }

      if (input.tool === "edit" || input.tool === "write") {
        // OpenCode names this filePath; the fallbacks cost nothing and keep the
        // plugin working if a future tool spells it differently.
        const path = argString(output.args, ["filePath", "file_path", "path"]);
        if (path === "") return;
        apply(
          await judge(
            ["session", "hook", "--path", "--agent-name", "opencode", "-o", "json"],
            path,
          ),
        );
      }
    },
  };
};

export default MagusGuard;
```

The thing being judged goes in on stdin, never in argv. `magus session hook` takes no
positional arguments, and a plugin that passes the command as one does not get a
wrong verdict - it gets no verdict, which fails open on every call.

If magus lives in a prefix the OpenCode process does not have on PATH (mise,
brew, asdf, `~/.local/bin`), set `GUARD_MAGUS_BIN` to an absolute path.

## Notifications

Invoke `magus session notify` from the plugin with the same envelope every other host
uses; see [Attention hooks](notifications.md).

## Coverage and limits

- An `advise` verdict is logged for the person. OpenCode has no
  context-injection arm, so it cannot reach the model. The same guidance ships
  in the installed skills, which is why the skills and the guard say the same
  things.
- The plugin fails open when magus cannot be run, and says so in the log.
  Throwing is OpenCode's only way to stop a call, so a guard that threw on a
  missing binary would block every tool call and make the session unusable.
- Tool identifiers were confirmed against an installed OpenCode 1.18.5: `bash`,
  `edit`, `write`, `read`, `patch` and `glob` all appear in its binary, and
  `filePath` is the field its edit tools carry.
- Delegation capture is FEASIBLE but not wired. `tool.execute.before` fires for
  every tool and hands the plugin `input.tool` plus the call's arguments, so a
  branch alongside the `bash` and `edit`/`write` ones could pipe a sub-agent
  tool's prompt to `magus session hook` and get the same `agent_spawn` event. Which tool
  identifier to match on has not been confirmed against an installed OpenCode,
  so the plugin above does not guess at one.
- OpenCode's documentation confirms that a throw blocks, but does not promise
  the thrown message reaches the model. Treat the deny as a hard stop whose
  explanation is best effort, and confirm against
  [OpenCode plugins](https://opencode.ai/docs/plugins).

## Verify

```sh
opencode debug config
magus doctor
```
