// The plugin finds its runtime by OpenCode's installed layout, not the
// spell's source layout. Stage the installed layout and load it unmodified.

import { copyFileSync, mkdirSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const SPELL = fileURLToPath(new URL("../../../../spells/harness/opencode/", import.meta.url));

const root = mkdtempSync(join(tmpdir(), "magus-opencode-installed-"));
mkdirSync(join(root, "plugins"));
mkdirSync(join(root, "magus"));
copyFileSync(join(SPELL, "plugins", "magus.js"), join(root, "plugins", "magus.js"));
for (const file of ["magus-opencode.wasm", "wasm_exec.js"]) {
  copyFileSync(join(SPELL, "gen", file), join(root, "magus", file));
}

export const { MagusGuard } = await import(pathToFileURL(join(root, "plugins", "magus.js")).href);
