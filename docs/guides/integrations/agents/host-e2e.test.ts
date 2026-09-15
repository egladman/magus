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

test("runtime harness descriptors cover read observation and checkpoints", () => {
  // Discrete-script hosts: JSON under harnesses/ is still the apply SoT, and each
  // one must wire both the Read observer and the stop checkpoint by name.
  for (const name of ["codex.json", "claude-code.json"]) {
    const harness = JSON.parse(
      readFileSync(path.join(repository, "harnesses", name), "utf8"),
    ) as { id: string; managed_entries: Array<Record<string, unknown>> };
    const managed = JSON.stringify(harness.managed_entries);
    assert.ok(
      managed.includes("magus-guard-observe.sh"),
      `${harness.id} must record read observations`,
    );
    assert.ok(
      managed.includes("magus-checkpoint.sh"),
      `${harness.id} must record stop checkpoints`,
    );
  }

  // Cursor: the harness spell is the magusfile SoT; harnesses/cursor.json stays
  // for --id without a wire. Both point at the unified cursor-guard.sh, which
  // covers command/path/observe/checkpoint in one script (sessionEnd = checkpoint).
  const cursorSpell = readFileSync(
    path.join(repository, "spells/harness/cursor/spell.buzz"),
    "utf8",
  );
  assert.match(cursorSpell, /cursor-guard\.sh/, "cursor spell names the Cursor guard script");
  assert.match(cursorSpell, /sessionEnd/, "cursor spell wires sessionEnd for checkpoints");
  assert.match(cursorSpell, /beforeShellExecution/, "cursor spell wires the shell guard");
  assert.match(cursorSpell, /preToolUse/, "cursor spell wires the write guard");

  const cursorJson = JSON.parse(
    readFileSync(path.join(repository, "harnesses", "cursor.json"), "utf8"),
  ) as { id: string; managed_entries: Array<Record<string, unknown>> };
  const cursorManaged = JSON.stringify(cursorJson.managed_entries);
  assert.equal(cursorJson.id, "cursor");
  assert.ok(cursorManaged.includes("cursor-guard.sh"), "cursor JSON stays in lockstep with the spell");
  assert.ok(cursorManaged.includes("sessionEnd"), "cursor JSON wires sessionEnd for checkpoints");

  // OpenCode: plugin transport, not managed shell entries. Skills install paths
  // are the only apply surface; the plugin calls magus directly.
  const opencode = JSON.parse(
    readFileSync(path.join(repository, "harnesses", "opencode.json"), "utf8"),
  ) as { id: string; managed_entries?: unknown; skills: { paths: string[] } };
  assert.equal(opencode.id, "opencode");
  assert.equal(opencode.managed_entries, undefined);
  assert.ok(opencode.skills.paths.some((p) => p.includes(".opencode")), "opencode installs skills");
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
