// magus guard hook for OpenCode. OpenCode has no shell-command hook config: a
// plugin intercepts tool calls instead, and throwing from tool.execute.before
// blocks the call.
//
// This file is the source of truth. Copy it to ~/.config/opencode/plugins/ (or
// .opencode/plugins/) and adjust to taste; nothing in it is magus-internal.
//
// It encodes no magus rule. Every decision comes from `magus hook`, so
// this stays host-only glue rather than a second rule set that drifts out of
// step with the other hosts' templates. `--host opencode` only labels the
// observation magus records; it cannot change a verdict.
//
// Covers BOTH guard surfaces, so OpenCode gets the same rules Claude Code does:
//   bash          the command rules (deny; advise surfaced to the human)
//   edit | write  the declared-output rule (deny only)
//
// PATH contract: this shells out to `magus` by name, inheriting PATH from the
// opencode process. If magus lives in a prefix PATH does not include (mise,
// brew, asdf, ~/.local/bin), set GUARD_MAGUS_BIN to an absolute path. That name
// deliberately avoids the MAGUS_* space, which is magus's own config surface.

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
  const magus = process.env.GUARD_MAGUS_BIN ?? "magus";

  /**
   * One `magus hook` invocation. `ran` says whether the process completed
   * successfully at all, which is what separates "magus judged this and the
   * verdict is unusable" from "this binary could not run that call" - the
   * second is retryable, the first is not.
   */
  type Attempt = { ran: boolean; verdict: Verdict | null };

  /**
   * Runs one guard query. `magus hook` reads the command or file path from
   * STDIN and takes no positional arguments, so the subject is piped rather
   * than appended to argv.
   *
   * Failing OPEN is deliberate. Throwing is OpenCode's only way to stop a call,
   * so a guard that threw whenever magus was missing would block every tool
   * call and make the session unusable - worse than no guard. The failure is
   * logged rather than swallowed, so an unguarded session stays visible.
   */
  const run = async (subject: string, args: readonly string[]): Promise<Attempt> => {
    let stdout: string;
    let code: number;
    try {
      const proc = Bun.spawn([magus, "hook", ...args, "-o", "json"], {
        stdin: new Blob([subject]),
        stdout: "pipe",
        stderr: "ignore",
      });
      stdout = await new Response(proc.stdout).text();
      code = await proc.exited;
    } catch {
      console.warn(
        `[magus guard] could not run ${magus}; this call is UNGUARDED. ` +
          "Install magus, or set GUARD_MAGUS_BIN to its path.",
      );
      return { ran: false, verdict: null };
    }
    // A non-zero exit is magus declining the call itself - an unknown flag is
    // the case this plugin plans for - and it prints usage on stdout, which
    // would otherwise be reported as a malformed verdict.
    if (code !== 0) return { ran: false, verdict: null };

    let parsed: unknown;
    try {
      parsed = JSON.parse(stdout);
    } catch {
      console.warn("[magus guard] verdict was not JSON; allowing");
      return { ran: true, verdict: null };
    }
    if (!isVerdict(parsed)) {
      console.warn("[magus guard] unrecognized verdict shape; allowing");
      return { ran: true, verdict: null };
    }
    if (parsed.schema_version !== SUPPORTED_SCHEMA) {
      console.warn(
        `[magus guard] verdict schema ${parsed.schema_version} differs from the expected ` +
          `${SUPPORTED_SCHEMA}; allowing. Update this plugin from the magus docs.`,
      );
      return { ran: true, verdict: null };
    }
    return { ran: true, verdict: parsed };
  };

  /**
   * Judges one subject, with attribution when the binary supports it.
   *
   * Attribution is BEST EFFORT; the verdict is not. `--host` postdates the
   * current magus release, and this file is copied and run against whatever
   * binary a reader already has. Passing it unconditionally does not degrade
   * the guard, it BREAKS it: an older binary rejects the unknown flag, prints
   * usage and exits non-zero, so no verdict comes back and every deny rule
   * silently stops being enforced. So: try with attribution, and on a failed
   * run retry without it - exactly the call this plugin made before
   * attribution existed. One extra process only on an older binary.
   */
  const judge = async (subject: string, args: readonly string[]): Promise<Verdict | null> => {
    const attributed = await run(subject, [...args, "--host", "opencode"]);
    if (attributed.ran) return attributed.verdict;
    return (await run(subject, args)).verdict;
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
        apply(await judge(command, []));
        return;
      }

      if (input.tool === "edit" || input.tool === "write") {
        // OpenCode names this filePath; the fallbacks cost nothing and keep the
        // plugin working if a future tool spells it differently.
        const path = argString(output.args, ["filePath", "file_path", "path"]);
        if (path === "") return;
        apply(await judge(path, ["--path"]));
      }
    },
  };
};

export default MagusGuard;
