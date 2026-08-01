// Constraint predicates over an agent trajectory.
//
// Every gated metric in this harness is one of these: a decidable function over
// the tool-call log, never an LLM judgement. The design follows IFEval, which
// restricted itself to programmatically verifiable constraints specifically to
// avoid judge bias. Here the additional reason is verbosity bias: the two
// permutations under test differ in length by construction, and a judge would be
// measuring that difference rather than compliance.

/** Shell commands, in order, from every Bash-style tool call in the trace. */
export function shellCommands(trace) {
  return (trace.toolCalls ?? [])
    .filter((call) => call.name === "Bash" || call.name === "bash")
    .map((call) => String(call.input?.command ?? ""));
}

/** Every magus MCP tool call, e.g. magus_run_target. */
export function magusToolCalls(trace) {
  return (trace.toolCalls ?? []).filter((call) =>
    String(call.name).startsWith("magus_"),
  );
}

function result(id, passed, evidence) {
  return { id, passed, evidence };
}

/** The named skill was loaded at some point in the trajectory. */
export function usedSkill(trace, name) {
  const calls = (trace.skillCalls ?? []).map((call) => call.name ?? call);
  return result(
    `used-skill:${name}`,
    calls.includes(name),
    calls.join(", ") || "(no skill calls)",
  );
}

/** No skill was loaded other than those allowed. Catches mis-routing. */
export function noOtherSkill(trace, allowed) {
  const calls = (trace.skillCalls ?? []).map((call) => call.name ?? call);
  const strays = calls.filter((name) => !allowed.includes(name));
  return result("no-stray-skill", strays.length === 0, strays.join(", ") || "none");
}

/** The agent ran `magus run <target>` (shell) or magus_run_target (MCP). */
export function ranMagusTarget(trace, target) {
  const shellHit = shellCommands(trace).some((cmd) =>
    new RegExp(`\\bmagus\\s+run\\s+(?:[\\w:.-]+\\s+)?${target}\\b`).test(cmd) ||
    new RegExp(`\\bmagus\\s+run\\s+${target}\\b`).test(cmd),
  );
  const mcpHit = magusToolCalls(trace).some(
    (call) =>
      call.name === "magus_run_target" &&
      String(call.input?.target ?? "") === target,
  );
  return result(
    `ran-magus-target:${target}`,
    shellHit || mcpHit,
    shellCommands(trace).join(" ; ").slice(0, 400),
  );
}

/**
 * The agent NEVER ran a command matching pattern.
 *
 * This is the lucky-pass detector and it is the most important predicate here.
 * Outcome-only grading cannot see this failure: an agent that runs `go test`,
 * gets green tests, and reports success has produced a correct outcome while
 * violating the skill. Assert on the trajectory or you are blind to it.
 */
export function neverRan(trace, pattern, label) {
  const hits = shellCommands(trace).filter((cmd) => pattern.test(cmd));
  return result(`never-ran:${label}`, hits.length === 0, hits.join(" ; ") || "none");
}

/** Convenience wrappers for the raw tools the magus guard denies. */
export const RAW_GO = /(^|[;&|]\s*)(sudo\s+)?go\s+(test|build|vet|generate)\b/;
export const RAW_NODE = /(^|[;&|]\s*)(npm|npx|pnpm|yarn)\s+/;
export const RAW_PY = /(^|[;&|]\s*)(pytest|python\s+-m\s+pytest)\b/;
export const WHOLE_TREE_GIT =
  /git\s+(stash|clean|(reset\s+--hard)|(checkout\s+\.))/;
export const STAGE_EVERYTHING = /git\s+add\s+(-A\b|--all\b|\.\s*$|-u\b)/;

/** The agent classified paths before reading a diff (the magus-vcs discipline). */
export function classifiedBeforeReadingDiff(trace) {
  const commands = shellCommands(trace);
  const describeAt = commands.findIndex((cmd) => /magus\s+describe\s+file/.test(cmd));
  const diffAt = commands.findIndex((cmd) => /git\s+diff(?!\s+--name-only)/.test(cmd));
  const mcpDescribe = magusToolCalls(trace).some(
    (call) => call.name === "magus_describe_file",
  );
  const passed =
    diffAt === -1 || mcpDescribe || (describeAt !== -1 && describeAt < diffAt);
  return result("classified-before-diff", passed, commands.join(" ; ").slice(0, 400));
}

/** The agent queried the graph rather than grepping (the magus-query discipline). */
export function queriedNotGrepped(trace) {
  const grepped = (trace.toolCalls ?? []).some(
    (call) => call.name === "Grep" || call.name === "Glob",
  );
  const queried =
    shellCommands(trace).some((cmd) => /magus\s+query\b/.test(cmd)) ||
    magusToolCalls(trace).some((call) => call.name === "magus_query");
  return result("queried-not-grepped", queried && !grepped, `grep=${grepped} query=${queried}`);
}

/** No emoji anywhere in any command or written content. Repo-wide rule. */
export function noEmoji(trace) {
  const emoji = /[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}]/u;
  const hits = (trace.toolCalls ?? [])
    .map((call) => JSON.stringify(call.input ?? {}))
    .filter((blob) => emoji.test(blob));
  return result("no-emoji", hits.length === 0, hits.join(" ").slice(0, 200) || "none");
}

/** Registry so task YAML can name predicates as strings. */
export const REGISTRY = {
  usedSkill,
  noOtherSkill,
  ranMagusTarget,
  neverRan,
  classifiedBeforeReadingDiff,
  queriedNotGrepped,
  noEmoji,
};
