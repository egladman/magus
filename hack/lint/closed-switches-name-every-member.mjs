// Prints every switch statement in the TypeScript files named on stdin, one JSON object per
// line, for hack/lint/closed-switches-name-every-member.buzz to judge. It parses with the
// TypeScript compiler API, so a switch inside a string or a comment is never counted. Each
// site says whether the switch has a default clause and whether the statement after it is
// an unreachable assertion for its value or union object. The rule decides
// what fails.
//
//	node hack/lint/closed-switches-name-every-member.mjs <root> < paths
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
const VIRTUAL_ROOT = "/closed-switches";

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

// symbolOf is the name of the function around node: its own name, else the name it is bound to
// (`const apply = () => {}`, `{ apply() {} }`), else "<module>" at the top level.
const symbolOf = (ts, sf, node) => {
  for (let up = node.parent; up !== undefined; up = up.parent) {
    if (ts.isFunctionDeclaration(up) || ts.isMethodDeclaration(up) || ts.isGetAccessorDeclaration(up) || ts.isSetAccessorDeclaration(up)) {
      if (up.name) return up.name.getText(sf);
    }
    if (ts.isConstructorDeclaration(up)) return "constructor";
    if (ts.isFunctionExpression(up) || ts.isArrowFunction(up)) {
      const bound = up.parent;
      if (ts.isVariableDeclaration(bound) || ts.isPropertyAssignment(bound) || ts.isPropertyDeclaration(bound)) {
        return bound.name.getText(sf);
      }
    }
  }
  return "<module>";
};

const unparenthesized = (ts, node) => {
  while (ts.isParenthesizedExpression(node)) node = node.expression;
  return node;
};

// A discriminant switch can narrow its containing union object to never.
const callsUnreachable = (ts, sf, stmt, subject) => {
  const expr = ts.isReturnStatement(stmt) || ts.isExpressionStatement(stmt) ? stmt.expression : undefined;
  if (
    expr === undefined ||
    !ts.isCallExpression(expr) ||
    expr.expression.getText(sf) !== "unreachable" ||
    expr.arguments.length !== 1
  ) return false;
  const value = unparenthesized(ts, subject);
  const asserted = unparenthesized(ts, expr.arguments[0]).getText(sf);
  return (
    asserted === value.getText(sf) ||
    ((ts.isPropertyAccessExpression(value) || ts.isElementAccessExpression(value)) &&
      asserted === unparenthesized(ts, value.expression).getText(sf))
  );
};

const sitesIn = (ts, path, sf) => {
  const sites = [];
  const visit = (node) => {
    if (ts.isSwitchStatement(node)) {
      const subject = node.expression.getText(sf);
      const siblings = node.parent.statements ?? [];
      const next = siblings[siblings.indexOf(node) + 1];
      const at = sf.getLineAndCharacterOfPosition(node.getStart(sf));
      sites.push({
        path,
        line: at.line + 1,
        col: at.character + 1,
        symbol: symbolOf(ts, sf, node),
        subject,
        hasDefault: node.caseBlock.clauses.some((c) => ts.isDefaultClause(c)),
        exhausted: next !== undefined && callsUnreachable(ts, sf, next, node.expression),
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
  process.stderr.write("usage: node hack/lint/closed-switches-name-every-member.mjs <root> < paths\n");
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
