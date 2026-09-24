// lint-catch.mjs - reject a swallowed failure in console/src.
//
// The console's rule is that every failure reaches the person (lib/notifications reportFailure, or
// the server transport and fetchSSE, which report on their own). A catch that drops its error is how
// a failure turns into an empty list or "not connected", so these shapes fail the lint:
//
//   catch {                  a catch that never binds the error
//   catch (e) { }            an empty body; a comment alone does not count as handling
//   .catch(() => {})         and the same with undefined, null, or void 0
//
// A site that is genuinely not a failure, or whose failure is already reported elsewhere, says so on
// the catch line or the first line of its body (for .catch(), its line or the one above), and the
// rule accepts it:
//
//   // not-a-failure: <why>
//   // reported: <where>
//
// Tests and generated code are exempt. Run from console/: node scripts/lint-catch.mjs [dir]
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

const MARKER = /\/\/\s*(not-a-failure|reported):\s*\S/;
const CATCH = /\bcatch\s*(\(\s*[^)]*\))?\s*\{/g;
const PROMISE_CATCH = /\.catch\(\s*(?:\(\s*[\w$]*\s*\)|[\w$]+)\s*=>\s*(?:\{\s*\}|undefined|null|void 0)\s*\)/g;

// lineOf returns the 1-based line number of offset in src.
function lineOf(src, offset) {
  let n = 1;
  for (let i = 0; i < offset; i++) if (src.charCodeAt(i) === 10) n++;
  return n;
}

// bodyEnd returns the offset of the brace closing the block opened at open. Strings and comments
// are skipped, so a brace inside either does not end the block.
function bodyEnd(src, open) {
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    const c = src[i];
    if (c === "/" && src[i + 1] === "/") {
      i = src.indexOf("\n", i);
      if (i < 0) return src.length;
    } else if (c === "/" && src[i + 1] === "*") {
      i = src.indexOf("*/", i + 2) + 1;
      if (i <= 0) return src.length;
    } else if (c === '"' || c === "'" || c === "`") {
      for (i++; i < src.length && src[i] !== c; i++) if (src[i] === "\\") i++;
    } else if (c === "{") depth++;
    else if (c === "}" && --depth === 0) return i;
  }
  return src.length;
}

// marked reports whether the catch at [start, open] carries a marker on its own line or on the
// first line of its body.
function marked(src, start, open) {
  const lineStart = src.lastIndexOf("\n", start) + 1;
  const firstLineEnd = src.indexOf("\n", src.indexOf("\n", open) + 1);
  return MARKER.test(src.slice(lineStart, firstLineEnd < 0 ? src.length : firstLineEnd));
}

// findSwallows returns every unmarked swallowed catch in one file's source, as { line, shape }.
export function findSwallows(src) {
  const out = [];
  for (const m of src.matchAll(CATCH)) {
    const open = m.index + m[0].length - 1;
    if (marked(src, m.index, open)) continue;
    if (!m[1]) {
      out.push({ line: lineOf(src, m.index), shape: "catch {" });
      continue;
    }
    const body = src
      .slice(open + 1, bodyEnd(src, open))
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/\/\/.*$/gm, "")
      .trim();
    if (body === "") out.push({ line: lineOf(src, m.index), shape: "empty catch body" });
  }
  for (const m of src.matchAll(PROMISE_CATCH)) {
    // The line above counts too: a formatter moves a trailing comment off a long call.
    const lineStart = src.lastIndexOf("\n", src.lastIndexOf("\n", m.index) - 1) + 1;
    const lineEnd = src.indexOf("\n", m.index);
    if (MARKER.test(src.slice(lineStart, lineEnd < 0 ? src.length : lineEnd))) continue;
    out.push({ line: lineOf(src, m.index), shape: m[0] });
  }
  return out.sort((a, b) => a.line - b.line);
}

function* sources(dir) {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name !== "gen") yield* sources(p);
    } else if (e.name.endsWith(".ts") && !e.name.endsWith(".test.ts")) yield p;
  }
}

// lintTree returns "file:line: shape" for every swallow under dir.
export function lintTree(dir) {
  const out = [];
  for (const file of sources(dir)) {
    for (const s of findSwallows(readFileSync(file, "utf8")))
      out.push(relative(process.cwd(), file) + ":" + s.line + ": swallowed failure (" + s.shape + ")");
  }
  return out;
}

if (process.argv[1] && process.argv[1].endsWith("lint-catch.mjs")) {
  const found = lintTree(process.argv[2] || "src");
  for (const line of found) console.error(line);
  if (found.length > 0) {
    console.error(
      found.length +
        " swallowed failure(s). Report it (reportFailure), or mark why it is not one: " +
        "`// not-a-failure: <why>` or `// reported: <where>` on the catch line or the first line of its body.",
    );
    process.exit(1);
  }
}
