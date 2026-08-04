// Report compression stats for the embedded agent skills.
//
// Installs BOTH permutations with the real renderer into temporary directories
// and measures the flat files that actually ship.
//
// Going through `magus agent install` rather than re-implementing applyVariant
// here is deliberate: this script cannot drift from the Go renderer, and it
// keeps working unchanged when the body syntax changes.
//
// Usage: node tools/measure_skills.mjs [--json]
//
// MAGUS_BIN overrides which magus renders (e.g. MAGUS_BIN=./magus for a HEAD
// build); the default is the magus on PATH.

import { execFileSync } from "node:child_process";
import { globSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import process from "node:process";

// Paths are resolved from this file, not the caller's cwd, so the script works
// no matter where it is invoked from.
const ROOT = new URL("../../", import.meta.url).pathname;
const MAGUS = (process.env.MAGUS_BIN ?? "magus").split(/\s+/);

// Render one permutation into dest with the real renderer.
function install(dest, simple) {
  const args = [...MAGUS.slice(1), "agent", "install", "--dir", dest, "--force", ".claude/skills"];
  if (simple) {
    args.push("--simple");
  }
  // The renderer narrates every file it writes; keep that out of the report
  // and surface it only when the install actually fails.
  try {
    execFileSync(MAGUS[0], args, { cwd: ROOT, stdio: ["ignore", "ignore", "pipe"] });
  } catch (error) {
    throw new Error(`install failed: ${[MAGUS[0], ...args].join(" ")}\n${error.stderr ?? error.message}`);
  }
  const bodies = {};
  for (const path of globSync(join(dest, ".claude", "skills", "*", "SKILL.md")).sort()) {
    bodies[basename(dirname(path))] = readFileSync(path, "utf8");
  }
  return bodies;
}

// Byte counts by line category, for the prose vs hard-content split.
function compose(text) {
  const counts = { prose: 0, table: 0, code: 0, heading: 0, blank: 0 };
  let inFence = false;
  for (const line of text.split("\n")) {
    const n = line.length + 1;
    const stripped = line.trim();
    if (stripped.startsWith("```")) {
      inFence = !inFence;
      counts.code += n;
    } else if (inFence) {
      counts.code += n;
    } else if (stripped.startsWith("|")) {
      counts.table += n;
    } else if (stripped.startsWith("#")) {
      counts.heading += n;
    } else if (stripped === "") {
      counts.blank += n;
    } else {
      counts.prose += n;
    }
  }
  return counts;
}

function count(haystack, needle) {
  return haystack.split(needle).length - 1;
}

function round1(number) {
  return Math.round(number * 10) / 10;
}

function main() {
  const asJson = process.argv.includes("--json");

  const tmp = mkdtempSync(join(tmpdir(), "measure-skills-"));
  let fullBodies;
  let simpleBodies;
  try {
    fullBodies = install(join(tmp, "full"), false);
    simpleBodies = install(join(tmp, "simple"), true);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }

  if (Object.keys(fullBodies).length === 0) {
    process.stderr.write("no skills installed; is magus on PATH?\n");
    return 1;
  }

  const sources = {};
  for (const path of globSync(join(ROOT, "cmd", "magus", "skills", "*", "SKILL.md")).sort()) {
    sources[basename(dirname(path))] = readFileSync(path, "utf8");
  }

  const rows = [];
  for (const name of Object.keys(fullBodies).sort()) {
    const full = fullBodies[name];
    const simple = simpleBodies[name] ?? "";
    const body = sources[name] ?? "";
    const comp = compose(simple);
    const hard = comp.table + comp.code;
    const total = Object.values(comp).reduce((sum, v) => sum + v, 0) || 1;
    rows.push({
      skill: name,
      full_bytes: full.length,
      simple_bytes: simple.length,
      cut_pct: full.length > 0 ? round1(100 - (100 * simple.length) / full.length) : 0,
      full_only_branches: count(body, "{{if .Full}}"),
      simple_only_branches: count(body, "{{if .Simple}}"),
      two_wordings: count(body, "{{else}}"),
      simple_prose_pct: round1((100 * comp.prose) / total),
      simple_hard_pct: round1((100 * hard) / total),
    });
  }

  const totFull = rows.reduce((sum, row) => sum + row.full_bytes, 0);
  const totSimple = rows.reduce((sum, row) => sum + row.simple_bytes, 0);
  const summary = {
    skills: rows,
    total_full_bytes: totFull,
    total_simple_bytes: totSimple,
    total_cut_pct: round1(100 - (100 * totSimple) / totFull),
    total_full_only_branches: rows.reduce((sum, row) => sum + row.full_only_branches, 0),
    total_simple_only_branches: rows.reduce((sum, row) => sum + row.simple_only_branches, 0),
    total_two_wordings: rows.reduce((sum, row) => sum + row.two_wordings, 0),
  };

  if (asJson) {
    console.log(JSON.stringify(summary, null, 2));
    return 0;
  }

  const cell = (value, width) => String(value).padStart(width);
  console.log(
    `${"skill".padEnd(22)} ${cell("full", 7)} ${cell("simple", 7)} ${cell("cut", 6)} ` +
      `${cell("ifFull", 6)} ${cell("else", 6)} ${cell("prose%", 8)}`,
  );
  for (const row of rows) {
    console.log(
      `${row.skill.padEnd(22)} ${cell(row.full_bytes, 7)} ${cell(row.simple_bytes, 7)} ` +
        `${cell(row.cut_pct.toFixed(1), 5)}% ${cell(row.full_only_branches, 6)} ` +
        `${cell(row.two_wordings, 6)} ${cell(row.simple_prose_pct.toFixed(1), 7)}%`,
    );
  }
  console.log(
    `${"TOTAL".padEnd(22)} ${cell(summary.total_full_bytes, 7)} ${cell(summary.total_simple_bytes, 7)} ` +
      `${cell(summary.total_cut_pct.toFixed(1), 5)}% ${cell(summary.total_full_only_branches, 6)} ` +
      `${cell(summary.total_two_wordings, 6)}`,
  );
  return 0;
}

process.exit(main());
