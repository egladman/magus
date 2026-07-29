// magus guard hook for OpenCode, which has no shell-command hook config: a
// plugin gets Bun's `$` shell handle instead, and throwing from
// tool.execute.before blocks the call.
//
// This file is the source of truth. Drop it in .opencode/plugins/ and adjust to
// taste; nothing in it is magus-internal.
//
// Covers BOTH guard surfaces, so OpenCode gets the same rules Claude Code does:
//   - bash        -> the command rules (deny + advise)
//   - edit/write  -> the declared-output rule (deny only)
// A host that wires only the first silently loses the generated-file guard,
// which is the one rule that is not a heuristic.
//
// Stance on a missing magus: this BLOCKS, unlike the Cursor wrapper, which
// allows. Throwing is OpenCode's only way to stop a call, so failing closed is
// the natural default here; Cursor's contract already fails open on a crash
// unless failClosed is set, so a wrapper pretending otherwise would mislead.
//
// Bun's `$` escapes the interpolated value, so passing it as one argument is
// injection-safe.

import type { Plugin } from "@opencode-ai/plugin"

type Verdict = {
  schema_version?: number
  decision?: string
  reason?: string
  context?: string
}

export const MagusGuard: Plugin = async ({ $ }) => {
  // Fail closed on every uncertainty: a missing binary, a non-zero exit, or
  // unparseable output must block rather than wave the call through.
  // `.nothrow()` keeps Bun from throwing on exit status so the decision stays
  // ours to make.
  const judge = async (args: string[]): Promise<Verdict> => {
    const res = await $`magus ${args}`.nothrow()
    if (res.exitCode !== 0) {
      throw new Error(`magus agent hook unavailable (exit ${res.exitCode}); blocking`)
    }
    let verdict: Verdict
    try {
      verdict = JSON.parse(res.text())
    } catch {
      throw new Error("magus agent hook returned unparseable output; blocking")
    }
    if (verdict.schema_version !== 1) {
      throw new Error(`unsupported hook schema ${verdict.schema_version}; blocking`)
    }
    return verdict
  }

  return {
    "tool.execute.before": async (input, output) => {
      // A pass and a deny are BOTH exit 0, so `decision` is the only signal - a
      // plugin branching on exit status alone would never block anything.
      if (input.tool === "bash") {
        const command = output.args.command ?? ""
        const verdict = await judge(["agent", "hook", "-o", "json", "--", command])
        if (verdict.decision === "deny") throw new Error(verdict.reason)
        // OpenCode has no context-injection arm, so an advise cannot reach the
        // model here. Logging keeps it visible to the human instead of dropping
        // it silently; the same guidance is in the installed skills.
        if (verdict.decision === "advise" && verdict.context) {
          console.warn(`magus: ${verdict.context}`)
        }
        return
      }

      if (input.tool === "edit" || input.tool === "write") {
        const path = output.args.filePath ?? output.args.path ?? ""
        if (!path) return
        const verdict = await judge(["agent", "hook", "--path", "-o", "json", "--", path])
        if (verdict.decision === "deny") throw new Error(verdict.reason)
      }
    },
  }
}
