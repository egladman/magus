import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import YAML from "yaml";

async function files(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const nested = await Promise.all(entries.map(async (entry) => {
    const path = join(dir, entry.name);
    return entry.isDirectory() ? files(path) : [path];
  }));
  return nested.flat().filter((path) => path.endsWith(".yaml"));
}

export async function readTasks(dir = "tasks") {
  const paths = await files(dir);
  return Promise.all(paths.sort().map(async (path) => ({
    path,
    task: YAML.parse(await readFile(path, "utf8")),
  })));
}

// loadTasks adapts the author-facing task schema to Promptfoo test cases. The
// task files remain independent of one provider's YAML shape.
export async function loadTasks() {
  return promptfooTests(await readTasks());
}

// loadSmokeTasks keeps the low-cost smoke matrix limited to explicitly tagged
// tasks while the grid covers the complete suite.
export async function loadSmokeTasks() {
  return promptfooTests((await readTasks()).filter(({ task }) => task.smoke === true));
}

function promptfooTests(tasks) {
  return tasks.map(({ task }) => ({
    vars: {
      task_id: task.id,
      skill: task.skill,
      smoke: task.smoke,
      prompt: task.prompt,
    },
    assert: task.constraints.map((constraint) => ({
      type: "javascript",
      value: "file://lib/assertions.mjs:constraint",
      config: constraint,
    })),
  }));
}
