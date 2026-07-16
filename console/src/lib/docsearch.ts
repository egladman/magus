// docsearch.ts - the documentation search grammar + index, ported from the docs
// site (docs/src/site/search.ts) so the console's Reference panel searches the same
// corpus with the same Datadog-style query language. The parser/ranker here is a
// faithful, DOM-free port of the site's pure functions (tokenize/parse/buildWild/
// scoreLeaf/runSearch and helpers); it is unit-tested in docsearch.test.ts. The UI
// that drives it lives in ui/ref-drawer.ts.
//
// Query syntax: space-separated terms are AND-ed; "quoted phrases" must appear
// contiguously; a -term excludes. Datadog-style field scoping narrows a term:
// tag:foo (whole-tag match), title:/description:|desc:/text:|body: (substring), and
// their negations (-tag:foo). Values with spaces are quoted: tag:"remote cache".
// Uppercase AND/OR/NOT + parentheses compose booleans; field:(a b) distributes the
// field over the bare terms. Wildcards build*/*cache*. Ranking weighs title > tags >
// description > body with a word-start bonus, and a typo-tolerant title-subsequence
// pass rescues near-misses when a plain AND-of-words finds nothing.

// One record in the search index (search-index.json), with a lazily-populated
// lowercased-fields cache hung off it (_lc) so re-scans do not re-lowercase.
interface LcFields {
  title: string;
  tags: string[];
  tagsJoined: string;
  desc: string;
  text: string;
  hay: string;
}

export interface DocSearchEntry {
  url: string;
  title: string;
  text?: string;
  tags?: string[];
  description?: string;
  _lc?: LcFields;
}

// One lexer token. Structural tokens ("(", ")", operators) carry only `t`; term
// tokens additionally carry the parsed field/value/phrase/wildcard bits.
interface Token {
  t: "(" | ")" | "and" | "or" | "not" | "term";
  field?: string | null;
  value?: string;
  phrase?: boolean;
  wildcard?: boolean;
  display?: string;
}

// The parsed query AST: a discriminated union over `op`.
interface TermNode {
  op: "term";
  field: string | null;
  value: string;
  phrase: boolean;
  wildcard: boolean;
  re: RegExp | null;
}
interface NotNode { op: "not"; kid: QueryNode; }
interface GroupNode { op: "and" | "or"; kids: QueryNode[]; }
type QueryNode = TermNode | NotNode | GroupNode;

export interface DocSearchResult {
  entry: DocSearchEntry;
  score: number;
}

function wordStart(hay: string, i: number): boolean {
  return i === 0 || /[^a-z0-9]/.test(hay.charAt(i - 1));
}

// True if needle's chars appear in order within hay (typo-tolerant fallback).
function subseq(needle: string, hay: string): boolean {
  let j = 0;
  for (let i = 0; i < hay.length && j < needle.length; i++) {
    if (hay.charAt(i) === needle.charAt(j)) j++;
  }
  return j === needle.length;
}

// Lowercased fields for one record, computed once and cached (search re-scans every
// record on every keystroke, so lowercasing per-call was pure waste). tags stays an
// array (tag: needs whole-tag equality); tagsJoined + hay serve substring matching.
function lc(e: DocSearchEntry): LcFields {
  if (e._lc) return e._lc;
  const title = (e.title || "").toLowerCase();
  const tags = (e.tags || []).map((t) => String(t).toLowerCase());
  const joined = tags.join(" ");
  const desc = (e.description || "").toLowerCase();
  const text = (e.text || "").toLowerCase();
  const fields: LcFields = {
    title, tags, tagsJoined: joined, desc, text,
    hay: title + " " + joined + " " + desc + " " + text,
  };
  e._lc = fields;
  return fields;
}

// A wildcard value ("build*", "*cache*") compiles to a regex: escape regex specials,
// turn * into .*, anchor whole-tag matches (^...$) but leave field/free-text loose.
function buildWild(value: string, field: string | null): RegExp {
  const body = value.replace(/[.*+?^${}()|[\]\\]/g, (ch) => (ch === "*" ? ".*" : "\\" + ch));
  const anchored = field === "tag" || field === "tags";
  return new RegExp(anchored ? "^" + body + "$" : body, "i");
}

// Tokenize a raw query into (, ), AND, OR, NOT, and term objects. Datadog-style:
// uppercase AND/OR/NOT are operators, a leading - is NOT, field:value scopes a term,
// "quoted" is a phrase (or a multi-word field value), and field:(...) distributes the
// field over the bare terms inside via a field stack. Lenient by construction.
function tokenize(raw: string): Token[] {
  const toks: Token[] = [];
  const fieldStack: (string | null)[] = [];
  let i = 0;
  const n = raw.length;
  const curField = (): string | null => (fieldStack.length ? fieldStack[fieldStack.length - 1] : null);
  const isSpace = (c: string): boolean => c === " " || c === "\t" || c === "\n" || c === "\r";
  while (i < n) {
    const c = raw.charAt(i);
    if (isSpace(c)) { i++; continue; }
    if (c === "(") { toks.push({ t: "(" }); fieldStack.push(null); i++; continue; }
    if (c === ")") { toks.push({ t: ")" }); if (fieldStack.length) fieldStack.pop(); i++; continue; }
    if (c === "-" && i + 1 < n && !isSpace(raw.charAt(i + 1))) { toks.push({ t: "not" }); i++; continue; }
    // Optional field: prefix (word chars then a colon).
    let field: string | null = null;
    let j = i;
    while (j < n && /[a-z0-9_.\-]/i.test(raw.charAt(j))) j++;
    if (j < n && raw.charAt(j) === ":" && j > i) {
      field = raw.slice(i, j).toLowerCase();
      i = j + 1;
      if (i < n && raw.charAt(i) === "(") { toks.push({ t: "(" }); fieldStack.push(field); i++; continue; }
    }
    // Value: quoted or bare (stopping at whitespace/parens).
    let value: string;
    let phrase = false;
    if (i < n && raw.charAt(i) === '"') {
      let end = raw.indexOf('"', i + 1);
      if (end === -1) end = n;
      value = raw.slice(i + 1, end);
      phrase = field === null;
      i = end + 1;
    } else {
      const vs = i;
      while (i < n && !isSpace(raw.charAt(i)) && raw.charAt(i) !== "(" && raw.charAt(i) !== ")") i++;
      value = raw.slice(vs, i);
    }
    if (value === "" && field === null) continue;
    if (field === null && !phrase) {
      const up = value.toUpperCase();
      if (up === "AND") { toks.push({ t: "and" }); continue; }
      if (up === "OR") { toks.push({ t: "or" }); continue; }
      if (up === "NOT") { toks.push({ t: "not" }); continue; }
    }
    toks.push({
      t: "term",
      field: field !== null ? field : curField(),
      value: value.toLowerCase(),
      phrase,
      wildcard: value.indexOf("*") !== -1,
      display: value,
    });
  }
  return toks;
}

// Recursive-descent parse into an AST. Precedence: NOT > AND (implicit or explicit) >
// OR. Leaves precompile their wildcard regex. Tolerant of stray/unbalanced tokens.
function parse(toks: Token[]): QueryNode | null {
  let pos = 0;
  const peek = (): Token | undefined => toks[pos];
  function parseOr(): QueryNode | null {
    const kids = [parseAnd()];
    while (peek() && peek()!.t === "or") { pos++; kids.push(parseAnd()); }
    const k = kids.filter((x): x is QueryNode => x !== null);
    return k.length === 1 ? k[0] : (k.length ? { op: "or", kids: k } : null);
  }
  function parseAnd(): QueryNode | null {
    const kids = [parseNot()];
    while (peek() && peek()!.t !== "or" && peek()!.t !== ")") {
      if (peek()!.t === "and") { pos++; if (!peek() || peek()!.t === "or" || peek()!.t === ")") break; }
      kids.push(parseNot());
    }
    const k = kids.filter((x): x is QueryNode => x !== null);
    return k.length === 1 ? k[0] : (k.length ? { op: "and", kids: k } : null);
  }
  function parseNot(): QueryNode | null {
    if (peek() && peek()!.t === "not") { pos++; const k = parseNot(); return k ? { op: "not", kid: k } : null; }
    return parsePrimary();
  }
  function parsePrimary(): QueryNode | null {
    const tk = peek();
    if (!tk) return null;
    if (tk.t === "(") { pos++; const inner = parseOr(); if (peek() && peek()!.t === ")") pos++; return inner; }
    if (tk.t === "term") {
      pos++;
      const value = tk.value ?? "";
      const field = tk.field ?? null;
      return {
        op: "term", field, value, phrase: tk.phrase ?? false,
        wildcard: tk.wildcard ?? false, re: tk.wildcard ? buildWild(value, field) : null,
      };
    }
    pos++; // stray ) / operator - skip and continue
    return parsePrimary();
  }
  return parseOr();
}

// Build the parsed query once: token stream + AST.
function buildQuery(raw: string): { tokens: Token[]; ast: QueryNode | null } {
  const tokens = tokenize(raw);
  return { tokens, ast: parse(tokens) };
}

// Does one term leaf match a record? tag = whole-tag equality (or regex); other fields
// are substring on that field; a free-text term matches the combined haystack.
function matchLeaf(leaf: TermNode, e: DocSearchEntry): boolean {
  const L = lc(e);
  if (leaf.field === "tag" || leaf.field === "tags") {
    for (let i = 0; i < L.tags.length; i++) {
      if (leaf.wildcard ? leaf.re!.test(L.tags[i]) : L.tags[i] === leaf.value) return true;
    }
    return false;
  }
  const hay = leaf.field === "title" ? L.title
    : (leaf.field === "description" || leaf.field === "desc") ? L.desc
    : (leaf.field === "text" || leaf.field === "body") ? L.text
    : L.hay;
  return leaf.wildcard ? leaf.re!.test(hay) : hay.indexOf(leaf.value) !== -1;
}

// Boolean inclusion: evaluate the AST against a record. Missing node = matches all.
function evalNode(node: QueryNode | null, e: DocSearchEntry): boolean {
  if (!node) return true;
  if (node.op === "term") return matchLeaf(node, e);
  if (node.op === "not") return !evalNode(node.kid, e);
  if (node.op === "and") { for (let i = 0; i < node.kids.length; i++) if (!evalNode(node.kids[i], e)) return false; return true; }
  if (node.op === "or") { for (let j = 0; j < node.kids.length; j++) if (evalNode(node.kids[j], e)) return true; return false; }
  return true;
}

// Relevance weight of one positive term leaf (field-weighted: title > tags >
// description > body, with a word-start / title-start bonus).
function scoreLeaf(leaf: TermNode, e: DocSearchEntry): number {
  const L = lc(e);
  const v = leaf.value;
  const hit = (hay: string, base: number, ws: number): number => {
    if (leaf.wildcard) return leaf.re!.test(hay) ? base : 0;
    const idx = hay.indexOf(v);
    return idx === -1 ? 0 : base + (wordStart(hay, idx) ? ws : 0);
  };
  if (leaf.field === "tag" || leaf.field === "tags") return matchLeaf(leaf, e) ? 8 : 0;
  if (leaf.field === "title") { let s = hit(L.title, 10, 4); if (!leaf.wildcard && L.title.indexOf(v) === 0) s += 4; return s; }
  if (leaf.field === "description" || leaf.field === "desc") return hit(L.desc, 4, 2);
  if (leaf.field === "text" || leaf.field === "body") return hit(L.text, 2, 1);
  let free = hit(L.title, 10, 4) + hit(L.tagsJoined, 8, 3) + hit(L.desc, 4, 2) + hit(L.text, 2, 1);
  if (!leaf.wildcard && L.title.indexOf(v) === 0) free += 4;
  return free;
}

// Rank = sum of positively-reachable leaf weights (an even number of NOTs above it),
// so OR/NOT decide inclusion while the positive hits that landed decide ordering.
function scoreAst(node: QueryNode | null, e: DocSearchEntry, neg: boolean): number {
  if (!node) return 0;
  if (node.op === "term") return neg ? 0 : scoreLeaf(node, e);
  if (node.op === "not") return scoreAst(node.kid, e, !neg);
  if (node.op === "and" || node.op === "or") {
    let s = 0; for (let i = 0; i < node.kids.length; i++) s += scoreAst(node.kids[i], e, neg); return s;
  }
  return 0;
}

// Positive (non-negated) leaf values, for snippet/title highlighting.
export function positiveTerms(raw: string): string[] {
  const collect = (node: QueryNode | null, neg: boolean, acc: string[]): string[] => {
    if (!node) return acc;
    if (node.op === "term") { if (!neg && node.value) acc.push(node.value); return acc; }
    if (node.op === "not") return collect(node.kid, !neg, acc);
    node.kids.forEach((k) => { collect(k, neg, acc); });
    return acc;
  };
  return collect(buildQuery(raw).ast, false, []);
}

// One interpreted piece of a query, for the "how this parsed" preview: the scope (a field or free
// text), the value, and whether it is negated / a phrase / a wildcard.
export interface QueryPart {
  field: string | null;
  value: string;
  neg: boolean;
  phrase: boolean;
  wildcard: boolean;
}

// describeQuery walks the parsed AST and returns the interpreted parts in order, so the UI can show a
// user how their typed query was understood (field scoping, exclusions, phrases, wildcards) - the same
// teach-the-grammar-as-you-type cue the docs site gives. Returns [] for an empty/unparseable query.
export function describeQuery(raw: string): QueryPart[] {
  const parts: QueryPart[] = [];
  const walk = (node: QueryNode | null, neg: boolean): void => {
    if (!node) return;
    if (node.op === "term") {
      parts.push({ field: node.field, value: node.value, neg, phrase: node.phrase, wildcard: node.wildcard });
      return;
    }
    if (node.op === "not") { walk(node.kid, !neg); return; }
    node.kids.forEach((k) => walk(k, neg));
  };
  walk(buildQuery(raw).ast, false);
  return parts;
}

// runSearch scores the whole index against the raw query and returns matches sorted by
// relevance (ties broken by shorter title). Needs at least one positive term - a bare
// -exclusion or pure operators match nothing (avoids a stray "-" dumping the corpus).
export function runSearch(index: DocSearchEntry[], raw: string): DocSearchResult[] {
  const q = buildQuery(raw);
  const pos: string[] = [];
  positiveTerms(raw).forEach((p) => pos.push(p));
  if (!pos.length) return [];
  const out: DocSearchResult[] = [];
  for (let i = 0; i < index.length; i++) {
    if (evalNode(q.ast, index[i])) out.push({ entry: index[i], score: scoreAst(q.ast, index[i], false) });
  }
  // Typo-tolerant fallback: only when the query is a plain AND of bare words that found
  // nothing (no fields/operators/parens), rescue near-misses via subsequence on titles.
  const simple = q.tokens.length > 0 && q.tokens.every((t) => t.t === "term" && t.field == null && !t.phrase && !t.wildcard);
  if (!out.length && simple) {
    index.forEach((e) => {
      let ok = true;
      let sc = 0;
      for (let k = 0; k < pos.length; k++) { if (subseq(pos[k], lc(e).title)) sc += 2; else { ok = false; break; } }
      if (ok) out.push({ entry: e, score: sc });
    });
  }
  out.sort((a, b) => b.score - a.score || a.entry.title.length - b.entry.title.length);
  return out;
}

// A one-line body excerpt centered on the first matched term (or the opening when no
// term appears in the body, e.g. a pure tag: filter). The index text is already cleaned
// at build time (fenced code stripped, whitespace collapsed).
export function snippet(text: string, terms: string[], win = 150): string {
  if (!text) return "";
  const low = text.toLowerCase();
  let pos = -1;
  for (let i = 0; i < terms.length; i++) {
    const p = low.indexOf(terms[i]);
    if (p !== -1 && (pos === -1 || p < pos)) pos = p;
  }
  let start = pos === -1 ? 0 : Math.max(0, pos - 50);
  if (start > 0) { const sp = text.indexOf(" ", start); if (sp !== -1 && sp - start < 20) start = sp + 1; }
  return (start > 0 ? "..." : "") + text.slice(start, start + win) + (start + win < text.length ? "..." : "");
}

// --- Index fetch + cache -------------------------------------------------------------
// The docs site ships search-index.json at the SITE ROOT; the console lives at the
// site's /console/ sibling, so the index is one level up. In standalone local dev there
// is no docs site, so the site-root fetch 404s; a console-relative copy (a dev-only
// artifact, gitignored) is tried as a fallback so search still works while iterating.
// Either way a miss resolves to null (never throws) and the panel degrades to a note.

let cached: DocSearchEntry[] | null = null;
let inflight: Promise<DocSearchEntry[] | null> | null = null;

function indexCandidates(): string[] {
  // import.meta.url is this module's URL under esbuild's --format=esm output; the
  // console bundle sits at the console root (gen/console.js -> /console/), so strip the
  // filename to get the console base, then resolve the site-root and dev-local siblings.
  let base = "";
  try { base = import.meta.url.replace(/[^/]*$/, ""); } catch { base = ""; }
  const siteRoot = base ? base + "../search-index.json" : "../search-index.json";
  const local = base ? base + "search-index.json" : "search-index.json";
  return [siteRoot, local];
}

async function fetchFirst(urls: string[]): Promise<DocSearchEntry[] | null> {
  for (const url of urls) {
    try {
      const r = await fetch(url);
      if (!r.ok) continue;
      const data = (await r.json()) as DocSearchEntry[];
      if (Array.isArray(data)) return data;
    } catch { /* try the next candidate */ }
  }
  return null;
}

// loadDocIndex fetches the index once and caches it. A failed load leaves the cache
// empty (returns null) so a later call retries rather than wedging on the first miss.
export function loadDocIndex(): Promise<DocSearchEntry[] | null> {
  if (cached) return Promise.resolve(cached);
  if (inflight) return inflight;
  inflight = fetchFirst(indexCandidates()).then((data) => {
    inflight = null;
    if (data) cached = data;
    return data;
  });
  return inflight;
}
