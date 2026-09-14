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
