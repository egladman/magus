// Prints every throw statement in the TypeScript and JavaScript files named on stdin, one JSON
// object per line, for hack/lint/no-throw.buzz to judge. It parses with the TypeScript compiler
// API, so a throw inside a string, a comment or a template is never counted. Each site carries
// the chain of classes and functions enclosing it; the rule, not this script, decides which are
// exempt.
//
//	node hack/lint/no-throw.mjs <root> < paths
//
// typescript resolves from libs/textsearch/node_modules, which
// `magus run install libs/textsearch` fills. TypeScript 7 parses in tsgo,
// reached through its typescript/unstable/sync API.
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const workspace = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

// Files are served to tsgo under this root, which holds no tsconfig, so
// each parses alone in the inferred project rather than in its own program.
const VIRTUAL_ROOT = "/no-throw";

const loadCompiler = async () => {
  try {
    const resolve = createRequire(join(workspace, "libs", "textsearch", "package.json")).resolve;
    const load = (name) => import(pathToFileURL(resolve(name)).href);
    const [api, ast, is] = await Promise.all([
      load("typescript/unstable/sync"), load("typescript/unstable/ast"), load("typescript/unstable/ast/is"),
    ]);
    return { ts: { ...is, SyntaxKind: ast.SyntaxKind, API: api.API } };
  } catch (err) {
    return { error: `cannot load typescript from libs/textsearch/node_modules (${err.message}): run \`magus run install libs/textsearch\`` };
  }
};

const readText = (path) => {
  try {
    return { text: readFileSync(path, "utf8") };
  } catch (err) {
    return { error: `cannot read ${path}: ${err.message}` };
  }
};

// eachSourceFile parses texts, keyed by workspace-relative path, in one tsgo
// session and hands each tree to visit before the session closes. A file
// outside the map falls back to the real filesystem.
const eachSourceFile = (ts, texts, visit) => {
  const files = new Map([...texts].map(([path, text]) => [`${VIRTUAL_ROOT}/${path}`, text]));
  const fs = {
    readFile: (name) => files.get(name),
    fileExists: (name) => (files.has(name) ? true : undefined),
  };
  let api;
  try {
    api = new ts.API({ cwd: VIRTUAL_ROOT, fs });
    const snapshot = api.updateSnapshot({ openFiles: [...files.keys()] });
    for (const [path] of texts) {
      const name = `${VIRTUAL_ROOT}/${path}`;
      const sf = snapshot.getDefaultProjectForFile(name)?.program.getSourceFile(name);
      if (sf === undefined) return { error: `tsgo returned no source file for ${path}` };
      visit(path, sf);
    }
    return {};
  } catch (err) {
    return { error: `tsgo could not parse: ${err.message}` };
  } finally {
    api?.close();
  }
};

const isFunction = (ts, node) =>
  ts.isFunctionDeclaration(node) ||
  ts.isFunctionExpression(node) ||
  ts.isArrowFunction(node) ||
  ts.isMethodDeclaration(node) ||
  ts.isConstructorDeclaration(node) ||
  ts.isGetAccessorDeclaration(node) ||
  ts.isSetAccessorDeclaration(node) ||
  ts.isClassStaticBlockDeclaration(node);

const isClass = (ts, node) => ts.isClassDeclaration(node) || ts.isClassExpression(node);

// nameOf is what a reader calls a function or class: its own name, else the name it is bound
// to (`const apply = () => {}`, `{ apply() {} }`, `o.apply = function () {}`).
const nameOf = (ts, sf, node) => {
  if (ts.isConstructorDeclaration(node)) return "constructor";
  if (ts.isClassStaticBlockDeclaration(node)) return "static";
  if (node.name) return node.name.getText(sf);
  const parent = node.parent;
  if (ts.isVariableDeclaration(parent) || ts.isPropertyAssignment(parent) || ts.isPropertyDeclaration(parent)) {
    return parent.name.getText(sf);
  }
  if (ts.isBinaryExpression(parent) && parent.right === node) return parent.left.getText(sf);
  return "<anonymous>";
};

// neverOnly reports whether fn takes exactly one parameter, typed never: unreachable(x: never).
const neverOnly = (ts, fn) =>
  fn.parameters !== undefined &&
  fn.parameters.length === 1 &&
  fn.parameters[0].type !== undefined &&
  fn.parameters[0].type.kind === ts.SyntaxKind.NeverKeyword;

const sitesIn = (ts, path, sf) => {
  const sites = [];
  const visit = (node) => {
    if (ts.isThrowStatement(node)) {
      const chain = [];
      let innermost;
      for (let up = node.parent; up !== undefined; up = up.parent) {
        if (isFunction(ts, up)) {
          innermost ??= up;
          chain.unshift(nameOf(ts, sf, up));
        } else if (isClass(ts, up)) {
          chain.unshift(nameOf(ts, sf, up));
        }
      }
      const at = sf.getLineAndCharacterOfPosition(node.getStart(sf));
      sites.push({
        path,
        line: at.line + 1,
        col: at.character + 1,
        construct: "throw",
        scope: chain.join("."),
        moduleScope: innermost === undefined,
        unreachable: innermost !== undefined && nameOf(ts, sf, innermost) === "unreachable" && neverOnly(ts, innermost),
      });
    }
    node.forEachChild(visit);
  };
  visit(sf);
  return sites;
};

const root = process.argv[2];
const compiler = await loadCompiler();
if (root === undefined) {
  process.stderr.write("usage: node hack/lint/no-throw.mjs <root> < paths\n");
  process.exitCode = 2;
} else if (compiler.error !== undefined) {
  process.stderr.write(`${compiler.error}\n`);
  process.exitCode = 2;
} else {
  const paths = readFileSync(0, "utf8").split("\n").filter((p) => p !== "");
  const texts = new Map();
  for (const path of paths) {
    const read = readText(join(root, path));
    if (read.error !== undefined) {
      process.stderr.write(`${read.error}\n`);
      process.exitCode = 2;
      continue;
    }
    texts.set(path, read.text);
  }
  const out = [];
  const parsed = texts.size === 0 ? {} : eachSourceFile(compiler.ts, texts, (path, sf) => {
    for (const site of sitesIn(compiler.ts, path, sf)) out.push(JSON.stringify(site));
  });
  if (parsed.error !== undefined) {
    process.stderr.write(`${parsed.error}\n`);
    process.exitCode = 2;
  }
  process.stdout.write(out.length === 0 ? "" : `${out.join("\n")}\n`);
}
