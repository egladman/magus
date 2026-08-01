import assert from "node:assert/strict";
import test from "node:test";

import {
  RAW_GO,
  classifiedBeforeReadingDiff,
  neverRan,
  ranMagusTarget,
} from "./constraints.mjs";

test("ranMagusTarget matches shell and MCP forms", () => {
  assert.equal(ranMagusTarget({ toolCalls: [{ name: "Bash", input: { command: "magus run test" } }] }, "test").passed, true);
  assert.equal(ranMagusTarget({ toolCalls: [{ name: "magus_run_target", input: { target: "test" } }] }, "test").passed, true);
});

test("neverRan catches raw Go but not an echoed string", () => {
  assert.equal(neverRan({ toolCalls: [{ name: "Bash", input: { command: "cd /repo && go test ./..." } }] }, RAW_GO, "raw-go").passed, false);
  assert.equal(neverRan({ toolCalls: [{ name: "Bash", input: { command: "echo 'go test ./...'" } }] }, RAW_GO, "raw-go").passed, true);
});

test("classifiedBeforeReadingDiff rejects a diff before classification", () => {
  const trace = {
    toolCalls: [
      { name: "Bash", input: { command: "git diff" } },
      { name: "Bash", input: { command: "magus describe file changed.go" } },
    ],
  };
  assert.equal(classifiedBeforeReadingDiff(trace).passed, false);
});
