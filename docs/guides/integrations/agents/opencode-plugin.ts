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
// Covers every call magus judges on the other hosts:
//   bash                    the command rules
//   edit | write            the declared-output rule
//   apply_patch             the same rule over a patch (the tool OpenCode offers gpt-* models
//                           in place of edit and write); the patch goes to magus whole
//   task                    a subagent spawn, recorded and graded like Claude Code's Agent
//   <server>_<tool>         a call to magus's own MCP server
//
// One handler (`apply`) serves them all, and both decisions reach the model: a deny throws
// and its reason arrives as the tool error, while an advise is held and appended to the
// tool's own result by tool.execute.after, joined to the call it belongs to by callID. An MCP
// result has no `output` string, so there the advise goes in as a text part of `content`.
//
// A subagent runs in a session of its own. Its calls carry the root session's id and the
// child's own as the agent, so a rule that tells a worker from the main conversation sees
// the same thing it sees on Claude Code. The parent comes from the session.created event,
// else from the SDK.
//
// Beyond verdicts it carries four jobs: a message the person types is recorded, the shell env
// gets MAGUS_LOG_SILENT, a checkpoint is recorded when a root session goes idle (OpenCode has
// no session-end event; idle is the proxy its own docs name), and the reply that ends a turn
// is proofread, with the findings sent back as a follow-up prompt.
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
// magus-guard-template: 25
// magus-guard-coverage: schema=2 host=opencode input=command deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=opencode input=path deny=model advise=model pass=none ask=model
// magus-guard-coverage: schema=2 host=opencode input=mcp deny=model advise=model pass=none ask=model
//
// PATH contract: this shells out to `magus` by name, inheriting PATH from the
// opencode process. If magus lives in a prefix PATH does not include (mise,
// brew, asdf, ~/.local/bin), set __MAGUS_BIN to an absolute path. That name
// deliberately avoids the MAGUS_* space, which is magus's own settings.
//
// The reply check runs proofread-reply.buzz: the path in __PROOFREAD_REPLY_BUZZ when that is
// set, else the copy beside this file, else the workspace's docs/guides/integrations/agents
// one. With none of them (or no proofread binary) the check is silent.

import { existsSync } from "node:fs";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
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

/** The nearest directory at or above dir holding the file name, or null when none does. */
function nearestWith(name: string, dir: string): string | null {
  for (let at = dir; ; ) {
    if (existsSync(join(at, name))) return at;
    const parent = dirname(at);
    if (parent === at) return null;
    at = parent;
  }
}

/**
 * The binary belonging to the workspace this process is inside, found by walking up to
 * the workspace root, or null when that workspace has not built one.
 *
 * The root is the nearest magus.yaml, the file magus.FindRoot treats as "the workspace
 * starts here", exactly as lib/hook.buzz resolves it. The nearest magusfile.buzz is wrong:
 * console/, docs/ and libs/* each carry one and no binary, so a call made from any of them
 * found no ./magus and fell to PATH. The nearest magus.yaml still never climbs into a
 * parent checkout, since a worktree nested under one holds its own. Only a tree with no
 * magus.yaml above falls back to the nearest magusfile.buzz.
 *
 * Walked rather than testing `./magus` alone: a plugin runs in the host's session
 * directory, and that is not always the workspace root: a session opened in a
 * subdirectory, or opened in one checkout while the work happens in another, tests a
 * `./magus` that is not there and falls through to PATH. Where PATH's copy cannot load
 * the workspace at all, that is the entire guard failing open.
 */
function workspaceMagus(): string | null {
  const root =
    nearestWith("magus.yaml", process.cwd()) ?? nearestWith("magusfile.buzz", process.cwd());
  if (root === null) return null;
  const bin = join(root, "magus");
  return existsSync(bin) ? bin : null;
}

/** OpenCode's name for a server or tool inside a tool id: every other character becomes "_". */
function sanitize(name: string): string {
  return name.replace(/[^a-zA-Z0-9_-]/g, "_");
}

/**
 * The keys of OpenCode's `mcp` config that run magus's own server: a local command naming
 * magus and `mcp`, or a key called "magus" that is not a local command (a remote server).
 */
function magusServers(config: unknown): string[] {
  if (typeof config !== "object" || config === null) return [];
  const mcp = (config as Record<string, unknown>).mcp;
  if (typeof mcp !== "object" || mcp === null) return [];
  const keys: string[] = [];
  for (const [key, entry] of Object.entries(mcp)) {
    const command = (entry as { command?: unknown } | null)?.command;
    if (Array.isArray(command)) {
      const words = command.map(String);
      if (words.some((word) => basename(word) === "magus") && words.includes("mcp")) keys.push(key);
    } else if (key === "magus") {
      keys.push(key);
    }
  }
  return keys;
}

/**
 * The magus tool a call to an OpenCode tool id names (`<server>_<tool>`), or null when the
 * id belongs to no magus server. The guard keeps only the names it knows, so an id that
 * merely starts with the server's name is dropped there.
 */
function magusTool(tool: string, servers: readonly string[]): string | null {
  for (const server of servers) {
    const prefix = `${sanitize(server)}_`;
    if (tool.startsWith(prefix) && tool.length > prefix.length) return tool.slice(prefix.length);
  }
  return null;
}

/**
 * Where proofread-reply.buzz lives for this install, or null when no copy exists.
 * __PROOFREAD_REPLY_BUZZ, when set, is the only place looked: a path that does not exist
 * turns the reply check off.
 */
function replyScript(): string | null {
  const named = process.env.__PROOFREAD_REPLY_BUZZ;
  if (named !== undefined && named !== "") return existsSync(named) ? named : null;
  const root = nearestWith("magus.yaml", process.cwd());
  const candidates = [
    join(dirname(fileURLToPath(import.meta.url)), "proofread-reply.buzz"),
    root === null ? "" : join(root, "docs/guides/integrations/agents/proofread-reply.buzz"),
  ];
  return candidates.find((path) => path !== "" && existsSync(path)) ?? null;
}

/** The session a call acts in: the root's id, plus the child's when a subagent made it. */
type Who = { session?: string; agent?: string };

/** One message of a session as the SDK lists it, narrowed to what the reply check reads. */
type Row = {
  info?: { role?: string };
  parts?: Array<{ type?: string; text?: string; synthetic?: boolean; ignored?: boolean }>;
};

/** An advisory held for its call, and whether that call returns MCP content instead of text. */
type Held = { context: string; mcp: boolean };

export const MagusGuard: Plugin = async (ctx) => {
  // Prefer the workspace's own ./magus over PATH, for the reason spelled out in
  // lib/hook.buzz: an older PATH binary does not fail when it lacks a rule, it
  // fails to recognize the config key that arms the rule and returns pass.
  const magus = process.env.__MAGUS_BIN ?? workspaceMagus() ?? "magus";

  // Said once per session. This plugin instance lives as long as the session does, so a
  // flag here IS the session and needs no marker on disk, unlike the Buzz templates whose
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
  const pending = new Map<string, Held>();

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
   * its value stripped out. Same shape as magus-command.buzz's unattributed
   * retry, fixed there after the same gap (memory:
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
      // --renders-ask, --session and --agent go too: a binary that rejects any predates them.
      const withoutValued = (list: readonly string[], flag: string): string[] => {
        const at = list.indexOf(flag);
        return at === -1 ? [...list] : [...list.slice(0, at), ...list.slice(at + 2)];
      };
      const withoutName = ["--agent-name", "--session", "--agent"]
        .reduce((list, flag) => withoutValued(list, flag), [...args])
        .filter((arg) => arg !== "--renders-ask");
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

  // A session's parent, null for a root. Filled by session.created, which fires before the
  // session's first call; a session first met any other way is asked of the SDK once, and
  // one the SDK cannot answer for is taken as a root.
  const parents = new Map<string, string | null>();

  const parentOf = async (id: string): Promise<string | null> => {
    const known = parents.get(id);
    if (known !== undefined) return known;
    let parent: string | null = null;
    try {
      const found = await ctx.client.session.get({ path: { id } });
      parent = found.data?.parentID ?? null;
    } catch {
      // Not knowing a session's parent costs the thread, never the call.
    }
    parents.set(id, parent);
    return parent;
  };

  // The root session a call belongs to, and the subagent's own session when it is one. A rule
  // that must tell the main session from a subagent (a push) refuses a call that names no
  // session, so every judged call carries OpenCode's own id.
  const whoIs = async (sessionID: string | undefined): Promise<Who> => {
    if (!sessionID) return {};
    let root = sessionID;
    for (let hops = 0; hops < 16; hops++) {
      const parent = await parentOf(root);
      if (parent === null) break;
      root = parent;
    }
    return root === sessionID ? { session: root } : { session: root, agent: sessionID };
  };

  const inThread = (args: readonly string[], who: Who): string[] => [
    ...args,
    ...(who.session ? ["--session", who.session] : []),
    ...(who.agent ? ["--agent", who.agent] : []),
  ];

  // The Claude Code envelope, for the calls magus reads by shape: a spawn, an MCP call, a
  // patch. The thread rides inside it, where a flag cannot reach the guard's decoder.
  const envelope = (
    event: "PreToolUse" | "PostToolUse",
    who: Who,
    tool: string,
    input: unknown,
    response?: unknown,
  ): string =>
    JSON.stringify({
      hook_event_name: event,
      session_id: who.session,
      agent_id: who.agent,
      tool_name: tool,
      tool_input: input,
      tool_response: response,
      cwd: ctx.directory,
    });

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
            'its own "permission.bash" entries configure, which magus describe harness opencode prints. ' +
            "Ask them to run it from their own terminal.",
        );
      case "advise":
        return verdict.context;
      case "pass":
        return "";
    }
  };

  /** Holds an advisory for the call it judged, so tool.execute.after can deliver it. */
  const remember = (callID: string, context: string, mcp = false): void => {
    if (context !== "") pending.set(callID, { context, mcp });
  };

  // The reply a follow-up prompt of ours was sent for, by session. The idle that follows it
  // belongs to that follow-up, and judging it would answer the check with the check.
  const followUps = new Map<string, string>();

  // The text of the last assistant message that has any, stopping at the last user message:
  // a turn that has said nothing yet has no reply.
  const lastReply = async (id: string): Promise<string> => {
    try {
      const listed = await ctx.client.session.messages({ path: { id } });
      const rows = (listed.data ?? []) as unknown as Row[];
      for (let at = rows.length - 1; at >= 0; at--) {
        const role = rows[at].info?.role;
        if (role === "user") return "";
        if (role !== "assistant") continue;
        const text = (rows[at].parts ?? [])
          .filter((part) => part.type === "text" && !part.synthetic && !part.ignored)
          .map((part) => part.text ?? "")
          .filter((part) => part.trim() !== "")
          .join("\n\n");
        if (text !== "") return text;
      }
    } catch {
      // No reply to judge is the same as no finding.
    }
    return "";
  };

  /**
   * Proofreads the reply a root session just ended its turn with and, on findings, sends
   * them back as a prompt. One follow-up per turn: the idle after it is skipped.
   *
   * A headless `opencode run` exits at the first idle, before the follow-up is answered.
   * Under `opencode serve` with `run --attach` the follow-up is answered.
   */
  const checkReply = async (id: string): Promise<void> => {
    if (followUps.delete(id)) return;
    const script = replyScript();
    if (script === null) return;
    const reply = await lastReply(id);
    if (reply === "") return;
    const stdout = await runOnce(
      ["buzz", "-s", script, "--", "--agent-name", "opencode"],
      JSON.stringify({ last_assistant_message: reply, stop_hook_active: false }),
    );
    let prompt: unknown;
    try {
      prompt = JSON.parse(stdout ?? "").prompt;
    } catch {
      return;
    }
    if (typeof prompt !== "string" || prompt === "") return;
    followUps.set(id, prompt);
    try {
      await ctx.client.session.promptAsync({
        path: { id },
        body: { parts: [{ type: "text", text: prompt }] },
      });
    } catch (error) {
      followUps.delete(id);
      console.warn(`[magus guard] could not send the reply check back: ${String(error)}`);
    }
  };

  return {
    "tool.execute.before": async (input, output) => {
      const who = await whoIs(input.sessionID);

      if (input.tool === "bash") {
        const command = argString(output.args, ["command"]);
        if (command === "") return;
        const verb = plainPushVerb(command);
        const promptable = verb !== null && asksBeforePush(loadedConfig, verb);
        remember(input.callID, apply(await judge(inThread(shellArgs, who), command), promptable));
        return;
      }

      if (input.tool === "edit" || input.tool === "write") {
        // OpenCode names this filePath; the fallbacks cost nothing and keep the
        // plugin working if a future tool spells it differently.
        const path = argString(output.args, ["filePath", "file_path", "path"]);
        if (path === "") return;
        const args = ["shell", "--path", "--agent-name", "opencode", "--renders-ask", "-o", "json"];
        remember(input.callID, apply(await judge(inThread(args, who), path), false));
        return;
      }

      // Offered to gpt-* models in place of edit and write. The patch goes whole: magus reads
      // the files it names.
      if (input.tool === "apply_patch") {
        const patchText = argString(output.args, ["patchText"]);
        if (patchText === "") return;
        const body = envelope("PreToolUse", who, input.tool, { patchText });
        remember(input.callID, apply(await judge(shellArgs, body), false));
        return;
      }

      // A subagent spawn. magus reads it by its prompt, so the tool's own name is only a label.
      if (input.tool === "task") {
        if (argString(output.args, ["prompt"]) === "") return;
        const body = envelope("PreToolUse", who, input.tool, output.args);
        remember(input.callID, apply(await judge(shellArgs, body), false));
        return;
      }

      const mcp = magusTool(input.tool, magusServers(loadedConfig));
      if (mcp !== null) {
        const body = envelope("PreToolUse", who, `mcp__magus__${mcp}`, output.args);
        remember(input.callID, apply(await judge(shellArgs, body), false), true);
      }
    },

    config: async (config) => {
      loadedConfig = config;
    },

    // A message the person typed is recorded and judged by nothing. A subagent's first message
    // is the spawn prompt, which the spawn already carried; our own follow-up is not the
    // person speaking either, and any other message ends the pause it started.
    "chat.message": async (input, output) => {
      if ((await parentOf(input.sessionID)) !== null) return;
      const text = output.parts
        .flatMap((part) => (part.type === "text" && !part.synthetic ? [part.text] : []))
        .join("\n\n");
      if (text.trim() === "") return;
      if (followUps.get(input.sessionID) === text) return;
      followUps.delete(input.sessionID);
      await runOnce(
        ["shell", "--message", "--agent-name", "opencode", "--session", input.sessionID],
        text,
      );
    },

    // The env of every command the agent runs. A person's own setting wins.
    "shell.env": async (_input, output) => {
      const set = process.env.MAGUS_LOG_SILENT ?? output.env.MAGUS_LOG_SILENT;
      if (set === undefined) output.env.MAGUS_LOG_SILENT = "true";
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
      const verdict = await judge(inThread(shellArgs, await whoIs(input.sessionID)), command);
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
      // The hook types a result as text, but an MCP result carries `content` parts and no
      // `output`: a string set there is dropped, a part pushed there reaches the model.
      const result = output as { output?: string; content?: unknown; metadata?: unknown };
      const held = pending.get(input.callID);
      if (held !== undefined) {
        pending.delete(input.callID);
        // Appended to the result the model already reads, rather than replacing it:
        // the advisory explains what to do differently NEXT time, and the tool's own
        // output is what the call was for.
        const text = `[magus guard] ${held.context}`;
        if (held.mcp && Array.isArray(result.content)) {
          result.content.push({ type: "text", text });
        } else if (typeof result.output === "string") {
          result.output = `${result.output}\n\n${text}`;
        } else {
          result.content = [{ type: "text", text }];
        }
      }

      // The finished spawn names the child, which is how magus ties the child's calls to
      // the job it was spawned for. The child's session is already known to be this one's.
      if (input.tool !== "task") return;
      const child = (result.metadata as { sessionId?: unknown } | undefined)?.sessionId;
      if (typeof child !== "string" || child === "") return;
      parents.set(child, input.sessionID);
      const who = await whoIs(input.sessionID);
      await judge(
        shellArgs,
        envelope("PostToolUse", who, input.tool, input.args, { agent_id: child }),
      );
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
    // The cost is real and is accepted: the session.compacted event fires after a
    // compaction, but the bus is read-only and this plugin does not subscribe to it, so a
    // compacted OpenCode session gets no brief. It can still ask, and `magus session --brief`
    // prints the same state on demand, which is the command every host shares.

    event: async ({ event }) => {
      if (event.type === "session.created" || event.type === "session.updated") {
        parents.set(event.properties.info.id, event.properties.info.parentID ?? null);
        return;
      }
      if (event.type !== "session.idle") return;

      // A subagent goes idle too, once per spawn. Its work is reported by the spawn that
      // finished, and a checkpoint or a reply check per child would drown the root's.
      const id = event.properties.sessionID;
      if ((await parentOf(id)) !== null) return;

      // OpenCode has no session-end event; session.idle is the proxy. The checkpoint
      // records where the work stands so whoever comes back reads `magus session`
      // rather than reconstructing it. It judges nothing and its output is ignored.
      await runOnce(["session", "checkpoint", "--agent-name", "opencode"], "");
      await checkReply(id);
    },
  };
};

export default MagusGuard;
