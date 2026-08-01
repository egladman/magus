import { spawnSync } from "node:child_process";
import process from "node:process";

import { REGISTRY } from "../lib/constraints.mjs";
import { readTasks } from "../lib/tasks.mjs";

const required = ["id", "skill", "smoke", "prompt", "constraints"];
const seen = new Set();
const entries = await readTasks();

for (const { path, task } of entries) {
  if (!task || typeof task !== "object" || Array.isArray(task)) {
    throw new Error(`${path}: task must be a YAML object`);
  }
  for (const field of required) {
    if (!(field in task)) {
      throw new Error(`${path}: missing required field ${field}`);
    }
  }
  if (typeof task.id !== "string" || task.id === "") {
    throw new Error(`${path}: id must be a non-empty string`);
  }
  if (seen.has(task.id)) {
    throw new Error(`${path}: duplicate task id ${task.id}`);
  }
  seen.add(task.id);
  if (typeof task.skill !== "string" || typeof task.smoke !== "boolean" || typeof task.prompt !== "string") {
    throw new Error(`${path}: skill, smoke, and prompt have invalid types`);
  }
  if (!Array.isArray(task.constraints) || task.constraints.length === 0) {
    throw new Error(`${path}: constraints must be a non-empty list`);
  }
  for (const constraint of task.constraints) {
    if (!constraint || typeof constraint.fn !== "string" || !(constraint.fn in REGISTRY)) {
      throw new Error(`${path}: unknown constraint predicate ${constraint?.fn ?? "(missing)"}`);
    }
    if (constraint.args !== undefined && !Array.isArray(constraint.args)) {
      throw new Error(`${path}: constraint ${constraint.fn} args must be a list`);
    }
  }
}

const tests = spawnSync(process.execPath, ["--test", "lib/constraints.test.mjs", "tools/analyze.test.mjs"], {
  encoding: "utf8",
});
if (tests.status !== 0) {
  process.stderr.write(tests.stdout);
  process.stderr.write(tests.stderr);
  process.exit(tests.status ?? 1);
}
