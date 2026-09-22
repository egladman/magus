import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import test from "node:test";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  parseDescriptor,
  probeScript,
  resultCode,
  selectedDescriptorPaths,
  selectedScenarios,
  validateEvidence,
} from "./host-e2e.ts";

const shellDescriptor = parseDescriptor(
  JSON.stringify({
    schemaVersion: 1,
    id: "fixture-shell",
    binary: "fixture",
    guardTemplate: "guard.sh",
    setup: [{ kind: "json", path: "host.json", value: { hook: "{{guard_command}}" } }],
    launch: { args: ["run", "{{workspace}}"] },
    evidence: { kind: "shell-deny", transport: "shell" },
  }),
);

const pluginDescriptor = parseDescriptor(
  JSON.stringify({
    schemaVersion: 1,
    id: "fixture-plugin",
    binary: "fixture",
    setup: [],
    launch: { args: ["run"] },
    evidence: { kind: "guard-deny", transport: "plugin" },
  }),
);
const descriptorDirectory = path.join(path.dirname(fileURLToPath(import.meta.url)), "host-e2e");
const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../../..");

test("requires explicit descriptor paths and deduplicates them", () => {
  assert.throws(() => selectedDescriptorPaths([]), /supply one or more/);
  assert.deepEqual(selectedDescriptorPaths(["--", "one.json", "one.json", "two.json"]), [
    path.resolve("one.json"),
    path.resolve("two.json"),
  ]);
});

test("parses a collaborator-owned descriptor without recognizing its id", () => {
  assert.equal(shellDescriptor.id, "fixture-shell");
  assert.equal(shellDescriptor.launch.args[1], "{{workspace}}");
  assert.equal(pluginDescriptor.evidence.kind, "guard-deny");
  assert.throws(() => parseDescriptor("{}"), /schemaVersion/);
  assert.throws(
    () =>
      parseDescriptor(
        JSON.stringify({
          schemaVersion: 1,
          id: "fixture",
          binary: "fixture",
          setup: [],
          launch: { args: [] },
          evidence: { kind: "invented", transport: "fixture" },
        }),
      ),
    /unsupported/,
  );
});

test("validates every packaged descriptor without maintaining a provider registry", () => {
  const descriptors = readdirSync(descriptorDirectory)
    .filter((entry) => entry.endsWith(".json"))
    .map((entry) => parseDescriptor(readFileSync(path.join(descriptorDirectory, entry), "utf8")));
  assert.ok(descriptors.length > 0, "the packaged descriptor directory is not empty");
  assert.equal(new Set(descriptors.map((descriptor) => descriptor.id)).size, descriptors.length);
});

test("runtime harness spells cover read observation and checkpoints", () => {
  // Discrete-script hosts: harnesses/*.json compat descriptors are gone, so this
  // reads the Buzz spell source directly; each one must still wire both the Read
  // observer and the stop checkpoint by name.
  for (const name of ["codex", "claude-code"]) {
    const spell = readFileSync(path.join(repository, "spells/harness", name, "spell.buzz"), "utf8");
    // Either shipped form of the observer: the sh copy, or the Buzz port a
    // `magus buzz` wiring names. They render the same behavior, and an executed
    // case refuses a difference; what this asks is that the host records reads
    // at all, not which of the two files it reached for.
    assert.ok(
      spell.includes("magus-observe.sh") || spell.includes("magus-observe.buzz"),
      `${name} must record read observations`,
    );
    // Either form again, for the reason above: codex is still wired to the sh copy
    // and claude-code has moved to the Buzz port, and this asks whether the host
    // records where the work stopped, not which runtime it spells that in.
    assert.ok(
      spell.includes("magus-checkpoint.sh") || spell.includes("magus-checkpoint.buzz"),
      `${name} must record stop checkpoints`,
    );
  }

  // Cursor: the harness spell is the magusfile SoT, with no JSON sibling left to
  // stay in lockstep with. It points at the unified cursor-hook.sh, which covers
  // command/path/observe/checkpoint in one script (sessionEnd = checkpoint).
  const cursorSpell = readFileSync(
    path.join(repository, "spells/harness/cursor/spell.buzz"),
    "utf8",
  );
  assert.match(cursorSpell, /cursor-hook\.sh/, "cursor spell names the Cursor hook script");
  assert.match(cursorSpell, /sessionEnd/, "cursor spell wires sessionEnd for checkpoints");
  assert.match(cursorSpell, /beforeShellExecution/, "cursor spell wires the shell guard");
  assert.match(cursorSpell, /preToolUse/, "cursor spell wires the write guard");

  // OpenCode: plugin transport, not managed shell entries. Skills install paths
  // are the only apply surface (harness_entries is empty, pinned by the spell's
  // own test suite); the plugin calls magus directly.
  const opencodeSpell = readFileSync(
    path.join(repository, "spells/harness/opencode/spell.buzz"),
    "utf8",
  );
  assert.ok(opencodeSpell.includes('return "opencode"'), "opencode spell names its id");
  assert.ok(opencodeSpell.includes(".opencode/skills"), "opencode installs skills");
});

test("selects the named VCS-neutral command-deny scenario", () => {
  assert.deepEqual(selectedScenarios(), [
    {
      name: "command-deny",
      prompt:
        "Use the Bash tool once to run exactly `go test ./...`. Do not make any other change.",
    },
  ]);
});

test("the disposable probe stores the raw hook response and structured exit evidence", () => {
  assert.match(probeScript, /sh "\$2" > "\$MAGUS_HOST_E2E_RESPONSE"/);
  assert.match(probeScript, /MAGUS_HOST_E2E_TRACE/);
  assert.match(probeScript, /"transport":"shell"/);
  assert.match(probeScript, /cat "\$MAGUS_HOST_E2E_RESPONSE"/);
  assert.doesNotMatch(probeScript, /\bgit\b/);
});

test("shell denial evidence requires the complete PreToolUse denial reply", () => {
  const trace =
    '{"provider":"fixture-shell","scenario":"command-deny","transport":"shell","exitCode":0,"responseFile":"hook-response.json"}\n';
  const response = JSON.stringify({
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "deny",
      permissionDecisionReason: "Raw Go tests must run through Magus.",
    },
  });
  assert.equal(
    validateEvidence(shellDescriptor, "command-deny", trace, response),
    "Raw Go tests must run through Magus.",
  );
});

test("evidence validation distinguishes malformed, non-denial, and production guard replies", () => {
  const shellTrace =
    '{"provider":"fixture-shell","scenario":"command-deny","transport":"shell","exitCode":0,"responseFile":"hook-response.json"}\n';
  assert.throws(
    () => validateEvidence(shellDescriptor, "command-deny", shellTrace, "not json"),
    /not JSON/,
  );
  assert.throws(
    () =>
      validateEvidence(
        shellDescriptor,
        "command-deny",
        shellTrace,
        '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"no"}}',
      ),
    /does not deny/,
  );
  const pluginTrace =
    '{"provider":"fixture-plugin","scenario":"command-deny","transport":"plugin","outcome":"deny","reason":"[magus guard] denied"}\n';
  assert.equal(
    validateEvidence(pluginDescriptor, "command-deny", pluginTrace),
    "[magus guard] denied",
  );
});

test("all skipped host selections are inconclusive, while a real host failure remains distinct", () => {
  assert.equal(resultCode([]), 2);
  assert.equal(resultCode([{ status: "skip" }]), 2);
  assert.equal(resultCode([{ status: "pass" }, { status: "skip" }]), 0);
  assert.equal(resultCode([{ status: "pass" }, { status: "fail" }]), 1);
});
