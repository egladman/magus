---
title: OpenCode
description: Wiring magus into OpenCode - skills in .opencode/skills, the TypeScript plugin that carries both guard surfaces, the post-compaction brief, and the idle checkpoint.
tags: [agents, opencode, skills, guard, plugin]
---

# OpenCode

OpenCode discovers Agent Skills and intercepts tool calls through a plugin
rather than a hook config. Throwing from `tool.execute.before` blocks the call, so
a deny reaches the model as the tool error; an advise is appended to the tool's
own result by `tool.execute.after`, which is the same call and the same context
window. One file carries all of it.

| what             | where                                                                                         |
| ---------------- | --------------------------------------------------------------------------------------------- |
| skills           | `.opencode/skills/` (it also reads `.claude/skills/`)                                         |
| guard wiring     | `~/.config/opencode/plugins/` or `.opencode/plugins/`                                         |
| command surface  | deny and advise both reach the model                                                          |
| file surface     | deny and advise both reach the model                                                          |
| MCP call surface | not wired: `tool.execute.before` sees it, its tool-name convention is unconfirmed (see below) |
| checkpoint       | the `session.idle` bus event                                                                  |
| rehydration      | `experimental.session.compacting`                                                             |
| MCP              | [MCP](../mcp.md)                                                                              |

## Skills

```sh
magus agent install .opencode/skills
```

Prefer wiring the OpenCode harness from the root magusfile so skills install
and multi-host apply see it beside the other hosts:

```buzz
import "oci://ghcr.io/egladman/magus/spells/opencode@sha256:<digest>" as opencode
magus\harness.provider(opencode)
```

The import is pinned by the digest cd's `spell-publish` step prints for a commit,
so the harness versions apart from your magus binary; see
[Remote spells](../../../reference/remote-spells.md).

The spell is skills-only (empty config path): `magus agent harness apply` writes
nothing for OpenCode, and verify does not invent a stamp JSON for Magus to check.
The guard is the TypeScript plugin, not managed hook fragments. To adapt without
Magus source edits, fork the spell and change only the import path; see
[Adapting a Buzz harness](../../../reference/skills/magus-workspace-rules.md).

`magus agent harness install --id opencode` refreshes `.opencode/skills` once the
spell is wired with `magus\harness.provider(opencode)`; OpenCode has no
shell-command hook config, so the descriptor it resolves to is skills-only too.

If you already installed into `.claude/skills` for Claude Code, OpenCode reads
those too and this step is optional. [Skills](skills.md) covers the rest of the
install surface.

## MCP

Configure MCP for OpenCode yourself. `magus agent harness apply --id opencode`
prints a short secret-ref hint and a docs pointer; Magus does not write OpenCode
MCP config. See [MCP](../mcp.md). An agent uses the CLI fallback when MCP is
unavailable.

## Guard hook

Save the plugin below to `~/.config/opencode/plugins/` or `.opencode/plugins/`,
then confirm OpenCode loaded it:

```sh
opencode debug config
```

It encodes no magus rule. Every decision comes from `magus shell`, which keeps it
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
// It encodes no magus rule. Every decision comes from `magus shell`, so
// this stays host-only glue rather than a second rule set that drifts out of
// step with the other hosts' templates. `--agent-name opencode` only labels the
// observation magus records; it cannot change a verdict.
//
// Covers BOTH guard surfaces, so OpenCode gets the same rules Claude Code does:
//   bash          the command rules
//   edit | write  the declared-output rule
//
// One handler (`apply`) serves both, and both decisions now reach the model: a
// deny throws and its reason arrives as the tool error, while an advise is
// appended to the tool's own result by tool.execute.after, joined to the call it
// belongs to by callID. That append is what replaced a console.warn, which reached
// the person and never the model. The declarations below record it, and they
// are machine-read by the host-parity gate; see the longer note in
// magus-command.sh.
//
// It also carries the two jobs that are not verdicts: a compacting session is
// handed this checkout back through the compaction prompt, and a checkpoint is
// recorded when the session goes idle, since OpenCode has no session-end event and
// idle is the proxy its own docs name.
//
// An ASK puts the call in front of the person, and a plugin cannot prompt: throwing is
// the only answer tool.execute.before has. So the prompt is OpenCode's own. The opencode
// harness writes each backend's push verb (`git push`, `hg push`, `sl push`, `jj git push`) and
// that verb with ` *` as "ask" under permission.bash in opencode.json, which makes OpenCode
// ask before any push, and this plugin's permission.ask hook answers that
// request from the verdict: allow for a push a gate covers, ask for an ungated one, deny
// for a leased worker's. Where that prompt cannot happen (the config does not ask, or the
// call is not a plain push the pattern matches) an ask throws, naming the person's own
// terminal. A decision this file does not know throws too, and never allows.
// magus-guard-template: 16
// magus-guard-coverage: schema=1 host=opencode surface=command deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=1 host=opencode surface=path deny=model advise=model pass=none ask=model
// magus-guard-coverage: schema=1 host=opencode surface=mcp deny=none advise=none pass=none ask=none
// NOT because tool.execute.before/.after cannot see an MCP call: they are generic and already
// intercept every tool call OpenCode makes, MCP included - only the two branches below (bash,
// edit/write) narrow that down by tool NAME. What is missing is knowing what name OpenCode
// gives an MCP tool call at all; no vendored source documents its convention (SOURCES.md:
// OpenCode ships no hook config schema, only the typed Plugin interface this file already
// type-checks against), and a third `if (input.tool === ...)` branch keyed on a guessed string
// risks silently misjudging an unrelated tool rather than catching magus's own calls. Flip this
// once that naming convention is confirmed against a real OpenCode session.
//
// PATH contract: this shells out to `magus` by name, inheriting PATH from the
// opencode process. If magus lives in a prefix PATH does not include (mise,
// brew, asdf, ~/.local/bin), set __MAGUS_BIN to an absolute path. That name
// deliberately avoids the MAGUS_* space, which is magus's own config surface.

import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
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
  | { schema_version: number; decision: "deny"; reason: string }
  | { schema_version: number; decision: "ask"; reason: string };

/** A verdict envelope before its decision is trusted: another process's stdout. */
type Envelope = { schema_version: number; decision: unknown } & Record<string, unknown>;

function isEnvelope(value: unknown): value is Envelope {
  if (typeof value !== "object" || value === null) return false;
  return typeof (value as Record<string, unknown>).schema_version === "number";
}

/**
 * Narrows an envelope to a Verdict. Anything it cannot narrow becomes a deny: a decision
 * this plugin does not know, or a known one missing the field it carries, is a verdict it
 * cannot honor, and reading it as a pass would allow what magus did not.
 */
function toVerdict(envelope: Envelope): Verdict {
  const { schema_version, decision, context, reason } = envelope;
  switch (decision) {
    case "pass":
      return { schema_version, decision };
    case "advise":
      if (typeof context === "string") return { schema_version, decision, context };
      break;
    case "deny":
    case "ask":
      if (typeof reason === "string") return { schema_version, decision, reason };
      break;
  }
  return {
    schema_version,
    decision: "deny",
    reason:
      `magus guard returned the decision ${JSON.stringify(decision)}, which this plugin does not know, ` +
      "so it refuses the call rather than allow it. Update the plugin from the magus docs.",
  };
}

/** The push verb of each backend magus drives, which is also the permission key for it. */
const PUSH_VERBS = ["git push", "hg push", "sl push", "jj git push"] as const;

/**
 * The push verb a command is one bare push through, or null. Only a bare push is known to
 * match its permission pattern. A compound line or `git -C dir push` may reach no pattern,
 * and OpenCode would run it without asking anyone.
 */
function plainPushVerb(command: string): string | null {
  if (/[;&|`$()<>\\\n]/.test(command)) return null;
  return PUSH_VERBS.find((verb) => command === verb || command.startsWith(`${verb} `)) ?? null;
}

/** Whether OpenCode's loaded config makes it ask before a push through verb. */
function asksBeforePush(config: unknown, verb: string): boolean {
  if (typeof config !== "object" || config === null) return false;
  const permission = (config as Record<string, unknown>).permission;
  if (typeof permission !== "object" || permission === null) return false;
  const bash = (permission as Record<string, unknown>).bash;
  if (typeof bash !== "object" || bash === null) return false;
  const patterns = bash as Record<string, unknown>;
  return patterns[verb] === "ask" && patterns[`${verb} *`] === "ask";
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

/**
 * The binary belonging to the workspace this process is inside, found by walking up to
 * the magusfile, or null when that workspace has not built one.
 *
 * Walked rather than testing `./magus` alone: a plugin runs in the host's session
 * directory, and that is not always the workspace root - a session opened in a
 * subdirectory, or opened in one checkout while the work happens in another, tests a
 * `./magus` that is not there and falls through to PATH. Where PATH's copy cannot load
 * the workspace at all, that is the entire guard failing open.
 */
function workspaceMagus(): string | null {
  for (let dir = process.cwd(); ; ) {
    if (existsSync(join(dir, "magusfile.buzz"))) {
      const bin = join(dir, "magus");
      return existsSync(bin) ? bin : null;
    }
    const parent = dirname(dir);
    if (parent === dir) return null;
    dir = parent;
  }
}

export const MagusGuard: Plugin = async () => {
  // Prefer the workspace's own ./magus over PATH, for the reason spelled out in
  // magus-command.sh: an older PATH binary does not fail when it lacks a rule, it
  // fails to recognize the config key that arms the rule and returns pass.
  const magus = process.env.__MAGUS_BIN ?? workspaceMagus() ?? "magus";

  // Said once per session. This plugin instance lives as long as the session does, so a
  // flag here IS the session and needs no marker on disk, unlike the sh templates whose
  // process ends with each tool call. The notice reports a broken installation: a fact
  // for the person, with nothing in it a model can act on, so a repeat is noise.
  // Measured over recent sessions: 99% of these firings were same-session repeats.
  let saidUnguarded = false;

  // Advisories waiting for the call they belong to, keyed by callID. An advise is
  // produced BEFORE a call and delivered AFTER it, because tool.execute.after is the
  // only hook that can rewrite what the model reads, and judging a second time there
  // would record two verdicts for one call.
  //
  // An entry is dropped when its call lands. A call that never reaches
  // tool.execute.after strands one string for the life of the session, which is a
  // cheaper leak than judging everything twice to avoid it.
  const pending = new Map<string, string>();

  /**
   * Runs one `magus shell` invocation and returns its raw stdout, or null when the
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
   * The thing being judged goes in on STDIN, never in argv. `magus shell` takes
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
   * its value stripped out. Same shape as magus-command.sh's `guard()`
   * fallback, fixed there after the same gap (memory:
   * agent-host-attribution-not-captured) and ported here so this plugin degrades
   * the same way.
   */
  const judge = async (args: readonly string[], input: string): Promise<Verdict | null> => {
    const unguarded = () => {
      if (saidUnguarded) return;
      saidUnguarded = true;
      console.warn(
        `[magus guard] could not run ${magus}; this call is UNGUARDED. ` +
          "Install magus, or set __MAGUS_BIN to its path.",
      );
    };

    let stdout = await runOnce(args, input);
    if (stdout === null) {
      unguarded();
      return null;
    }
    if (stdout.trim() === "") {
      const nameIndex = args.indexOf("--agent-name");
      // --renders-ask goes too: a binary that rejects it is too old to return an ask.
      const withoutName = (
        nameIndex === -1 ? args : [...args.slice(0, nameIndex), ...args.slice(nameIndex + 2)]
      ).filter((arg) => arg !== "--renders-ask");
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
    if (!isEnvelope(parsed)) {
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
    return toVerdict(parsed);
  };

  // OpenCode's loaded config, set by the config hook. Until it runs, nothing is known to ask
  // before a push, so an ask throws rather than trusting a prompt that may not come.
  let loadedConfig: unknown = null;

  // --renders-ask: apply never lets an ask through unasked (it passes one to OpenCode's own
  // prompt or throws), so this plugin may receive one. Without it magus answers with a deny.
  const shellArgs = ["shell", "--agent-name", "opencode", "--renders-ask", "-o", "json"];

  /**
   * Throws on a deny, which is OpenCode's only way to stop a call, and on an ask that
   * OpenCode's own prompt will not reach. Returns an advise's context for the caller to
   * hold until the call lands, and "" for everything else.
   */
  const apply = (verdict: Verdict | null, promptable: boolean): string => {
    if (verdict === null) return "";
    switch (verdict.decision) {
      case "deny":
        throw new Error(`[magus guard] ${verdict.reason}`);
      case "ask":
        if (promptable) return "";
        throw new Error(
          `[magus guard] ${verdict.reason}\n\nThis call needs the approval of the person you work for, ` +
            "and OpenCode will not ask them: only a plain git push, hg push, sl push or jj git push reaches the prompt " +
            'its own "permission.bash" entries configure, which magus agent harness apply --id opencode writes. ' +
            "Ask them to run it from their own terminal.",
        );
      case "advise":
        return verdict.context;
      case "pass":
        return "";
    }
  };

  /** Holds an advisory for the call it judged, so tool.execute.after can deliver it. */
  const remember = (callID: string, context: string): void => {
    if (context !== "") pending.set(callID, context);
  };

  return {
    "tool.execute.before": async (input, output) => {
      if (input.tool === "bash") {
        const command = argString(output.args, ["command"]);
        if (command === "") return;
        const verb = plainPushVerb(command);
        const promptable = verb !== null && asksBeforePush(loadedConfig, verb);
        remember(input.callID, apply(await judge(shellArgs, command), promptable));
        return;
      }

      if (input.tool === "edit" || input.tool === "write") {
        // OpenCode names this filePath; the fallbacks cost nothing and keep the
        // plugin working if a future tool spells it differently.
        const path = argString(output.args, ["filePath", "file_path", "path"]);
        if (path === "") return;
        const args = ["shell", "--path", "--agent-name", "opencode", "--renders-ask", "-o", "json"];
        remember(input.callID, apply(await judge(args, path), false));
      }
    },

    config: async (config) => {
      loadedConfig = config;
    },

    // OpenCode raises this before its own prompt. Only a push, the prompt magus configured,
    // is answered: any other request is the person's own rule, and a pass from the guard is
    // not their consent to it. With no verdict at all the prompt stays, so a broken guard
    // costs a question rather than an unasked push.
    "permission.ask": async (input, output) => {
      if (input.type !== "bash") return;
      const command =
        typeof input.metadata.command === "string" ? input.metadata.command : input.title;
      if (plainPushVerb(command) === null) return;
      const verdict = await judge(shellArgs, command);
      switch (verdict?.decision) {
        case "pass":
        case "advise":
          output.status = "allow";
          return;
        case "ask":
          output.status = "ask";
          return;
        case "deny":
          output.status = "deny";
          return;
      }
    },

    "tool.execute.after": async (input, output) => {
      const context = pending.get(input.callID);
      if (context === undefined) return;
      pending.delete(input.callID);
      // Appended to the result the model already reads, rather than replacing it:
      // the advisory explains what to do differently NEXT time, and the tool's own
      // output is what the call was for.
      output.output = `${output.output}\n\n[magus guard] ${context}`;
    },

    // NO experimental.session.compacting handler, deliberately, and this comment is the
    // record of why so it is not re-added as an obvious improvement.
    //
    // OpenCode's hook appends to the SUMMARIZER PROMPT (output.context) or replaces it
    // (output.prompt). This plugin used to push `magus session --brief` into it, which
    // read as rehydration and was not: on every other host the brief lands verbatim AFTER
    // the summary, while here it went in as summarizer input and survived only as much of
    // it as the summarizer chose to keep. The brief's own contract, in cmd/magus/session_brief.go,
    // is that it is state read from disk and never prose retold -- so the one host where it
    // was retold was the one host contradicting it.
    //
    // The second reason is parity. Of the four hosts magus wires, only OpenCode can steer
    // a summary at all: Claude Code has no PreCompact arm on hookSpecificOutput, Codex's
    // pre-compact.command.output.schema.json is additionalProperties:false over four fields
    // with no context channel, and Cursor's preCompact is documented as observational. A
    // behavior available on one host of four is not a feature, it is a difference nobody
    // can reason about, and magus's job is to stay out of the model's way rather than to
    // shape what it remembers on whichever host happens to allow it.
    //
    // The cost is real and is accepted: OpenCode has no post-compaction hook (its Plugin
    // type carries only the two pre-compaction ones), so a compacted OpenCode session gets
    // no brief. It can still ask, and `magus session --brief` prints the same state on
    // demand, which is the surface every host shares.

    event: async ({ event }) => {
      // OpenCode has no session-end event; session.idle is the proxy. The checkpoint
      // records where the work stands so whoever comes back reads `magus session`
      // rather than reconstructing it. It judges nothing and its output is ignored.
      if (event.type !== "session.idle") return;
      await runOnce(["session", "checkpoint", "--agent-name", "opencode"], "");
    },
  };
};

export default MagusGuard;
```

The thing being judged goes in on stdin, never in argv. `magus shell` takes no
positional arguments, and a plugin that passes the command as one does not get a
wrong verdict - it gets no verdict, which fails open on every call.

If magus lives in a prefix the OpenCode process does not have on PATH (mise,
brew, asdf, `~/.local/bin`), set `__MAGUS_BIN` to an absolute path.

A push at a commit no passing gate covers needs your approval, and a plugin cannot
ask: `tool.execute.before` can only throw. `magus agent harness apply --id opencode`
writes an `"ask"` entry under `permission.bash` in `opencode.json` for each backend's
push, bare and with arguments: `git push`, `hg push`, `sl push` and `jj git push`, each
also as `<verb> *`. OpenCode then prompts before every push, and the plugin's
`permission.ask` hook answers that prompt from the verdict: allow for a push a gate
covers, ask for an ungated one, deny for a session bound to a job lease, because
workers do not publish. Without the permission for that backend, or for a push written
as anything but a plain `git push`, `hg push`, `sl push` or `jj git push` command, the plugin refuses the push and names your own
terminal. `magus agent harness verify --id opencode` fails while the permission is
missing. A decision the plugin does not know is refused, never allowed.

## Notifications

Invoke `magus session notify` from the plugin with the same envelope every other host
uses; see [Attention hooks](notifications.md).

## Handing a compacted session its state back

`experimental.session.compacting` runs while OpenCode is building the summary
that will replace a session's history, and takes `context: string[]` straight
into the compaction prompt. **The plugin deliberately does not use it**, and a
compacted OpenCode session is therefore handed nothing.

That hook feeds the SUMMARIZER, not the model that comes after it. The plugin
used to push `magus session --brief` through it, which read as rehydration and
was not: on Claude Code and Codex the brief lands verbatim after the summary,
while here it went in as summarizer input and survived only as much of it as the
summarizer kept. The brief is state read off the disk, and this was the one place
it was retold.

The second reason is parity. Of the four hosts, only OpenCode can shape a summary
at all -- Claude Code exposes no `PreCompact` output arm, Codex's
`pre-compact.command.output` schema is closed over four fields with no context
channel, and Cursor's `preCompact` is observational. A behavior available on one
host of four is a difference nobody can reason about, and deciding what a model
remembers is not magus's call to make.

Run `magus session --brief` yourself after a compaction: it prints the same state
on demand, which is the surface every host shares.

## Recording where the work stands

OpenCode has no session-end event. `session.idle` on the read-only bus is the
proxy its own docs name, and the plugin subscribes to it and runs
`magus session checkpoint --agent-name opencode`: the revision, branch and
dirtiness of the tree, which `magus session` then lists.

Idle fires when the agent stops working rather than when the session is closed,
so a long session records several checkpoints. That is the same shape a `Stop`
hook produces on the hosts that have one, and the listing is ordered.

If you would rather not have the plugin do it, running
[`magus-checkpoint.sh`](guard-templates.md#magus-checkpointsh) with
`__MAGUS_AGENT_NAME=opencode` records the identical row.
`magus session checkpoint --note "..."` writes it by hand.

## Coverage and limits

- An `advise` verdict is appended to the tool result that call produced, joined
  to it by `callID`. It is judged once, before the call, and delivered after it -
  judging again in `tool.execute.after` would record two verdicts for one call.
  An advised call whose result never arrives strands one string for the life of
  the session, which is the cheaper of the two leaks.
- The plugin fails open when magus cannot be run, and says so in the log.
  Throwing is OpenCode's only way to stop a call, so a guard that threw on a
  missing binary would block every tool call and make the session unusable.
- Tool identifiers were confirmed against an installed OpenCode 1.18.5: `bash`,
  `edit`, `write`, `read`, `patch` and `glob` all appear in its binary, and
  `filePath` is the field its edit tools carry. An MCP tool's `input.tool`
  string was NOT among them (OpenCode has no hook config schema to check it
  against either, per `testdata/hosts/SOURCES.md`), so the MCP call
  surface is feasible - `tool.execute.before`/`.after` already see every call,
  MCP included - but not wired: a branch keyed on a guessed name risks judging
  an unrelated tool rather than magus's own calls. Confirm the string against a
  live session, then add a third `if (input.tool === ...)` branch beside `bash`
  and `edit`/`write`.
- Lease capture is FEASIBLE but not wired. `tool.execute.before` fires for
  every tool and hands the plugin `input.tool` plus the call's arguments, so a
  branch alongside the `bash` and `edit`/`write` ones could pipe a sub-agent
  tool's prompt to `magus shell` and get the same `agent_spawn` event. Which tool
  identifier to match on has not been confirmed against an installed OpenCode,
  so the plugin above does not guess at one.
- `shell.env` could export `BAGGAGE=magus.lease=<id>` into every shell the
  session runs, and deliberately does not. The only place the plugin could read
  that id is the marker `magus job exec` writes into the checkout, and the
  guard and the sandbox already read that marker directly; it exists precisely
  because a host runs its hooks with its own environment. Exporting a copy of it
  would be a second source of truth that can go stale, for a lease the tools can
  already see.
- OpenCode's documentation confirms that a throw blocks, but does not promise
  the thrown message reaches the model. Treat the deny as a hard stop whose
  explanation is best effort, and confirm against
  [OpenCode plugins](https://opencode.ai/docs/plugins).

## Verify

```sh
opencode debug config
magus doctor
```
