// textsearch.ts - a small, DOM-free full-text search engine with a Datadog-style query
// grammar. It is the one source of truth shared by the magus docs site and the console,
// and it depends on NEITHER app: callers inject their own records (any object carrying the
// searchable fields below) and, for the factory, their own index source (an array or a
// loader). Nothing here fetches a fixed URL, touches the DOM, reads a global, or knows a
// route - the data always comes from the caller.
//
// A lexer + recursive-descent parser build a query AST from the Datadog-style grammar:
// space-separated terms are AND-ed; "quoted phrases" must appear contiguously; a -term
// excludes; field:value scoping narrows a term (tag: is whole-tag match; title:,
// description:|desc:, text:|body: are substring; each negatable with a leading -); uppercase
// AND/OR/NOT + parentheses compose booleans; field:(a b) distributes the field; build* and
// *cache* are wildcards. runSearch ranks a caller-supplied index (title > tags > description
// > body, with a word-start bonus and a typo-tolerant title-subsequence fallback). buildQuery
// exposes the raw token stream (a chip preview) and getPositiveTerms the positive terms; a
// "how this parsed" preview (describeQuery) and a one-line excerpt (buildSnippet) are reached
// through the createTextSearch factory, which bundles an injected index/loader with all of the
// above into a searcher object. Unit-tested in textsearch.test.ts.

// The minimal shape the ranker reads off a record. Callers pass records that satisfy this,
// typically carrying extra fields of their own (a url, an id) that the engine ignores and
// hands back untouched on each result. Every searchable field but title is optional.
export interface TextSearchEntry {
  title: string;
  tags?: readonly string[];
  description?: string;
  text?: string;
}

// One ranked hit: the caller's own record (its full type preserved through the generic) and
// a relevance score.
export interface TextSearchResult<E extends TextSearchEntry> {
  entry: E;
  score: number;
}

// Lowercased fields for one record, computed once and cached in a WeakMap keyed by the
// record itself (search re-scans every record on every keystroke, so lowercasing per-call
// was pure waste). The cache lives here, not on the caller's object, so the engine never
// mutates injected records. tags stays an array (tag: needs whole-tag equality); tagsJoined
// + hay serve substring matching.
interface LcFields {
  title: string;
  tags: string[];
  tagsJoined: string;
  desc: string;
  text: string;
  hay: string;
}

const lcCache = new WeakMap<TextSearchEntry, LcFields>();

// One lexer token, a discriminated union on `t`: structural tokens ("(", ")", operators) carry
// only `t`, while a term token additionally carries the parsed field/value/phrase/wildcard bits
// (so `t === "term"` narrows all of them together). Exported so a caller's chip preview can
// render the raw token stream (fields, phrases, brackets, connectives) that the AST-walking
// describeQuery collapses away.
export type Token =
  | { t: "(" | ")" | "and" | "or" | "not" }
  | {
      t: "term";
      field: string | null;
      value: string;
      phrase: boolean;
      wildcard: boolean;
      display: string;
    };

// The parsed query AST: a discriminated union over `op`.
interface TermNode {
  op: "term";
  field: string | null;
  value: string;
  phrase: boolean;
  wildcard: boolean;
  re: RegExp | null;
}
interface NotNode {
  op: "not";
  kid: QueryNode;
}
interface GroupNode {
  op: "and" | "or";
  kids: QueryNode[];
}
type QueryNode = TermNode | NotNode | GroupNode;

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

// Lowercased fields for one record, memoized in lcCache.
function lc(e: TextSearchEntry): LcFields {
  const hit = lcCache.get(e);
  if (hit) return hit;
  const title = (e.title || "").toLowerCase();
  const tags = (e.tags || []).map((t) => String(t).toLowerCase());
  const joined = tags.join(" ");
  const desc = (e.description || "").toLowerCase();
  const text = (e.text || "").toLowerCase();
  const fields: LcFields = {
    title,
    tags,
    tagsJoined: joined,
    desc,
    text,
    hay: title + " " + joined + " " + desc + " " + text,
  };
  lcCache.set(e, fields);
  return fields;
}

// A wildcard value ("build*", "*cache*") compiles to a regex: escape regex specials, turn *
// into .*, anchor whole-tag matches (^...$) but leave field/free-text loose.
function buildWild(value: string, field: string | null): RegExp {
  const body = value.replace(/[.*+?^${}()|[\]\\]/g, (ch) => (ch === "*" ? ".*" : "\\" + ch));
  const anchored = field === "tag" || field === "tags";
  return new RegExp(anchored ? "^" + body + "$" : body, "i");
}

// Tokenize a raw query into (, ), AND, OR, NOT, and term objects. Datadog-style: uppercase
// AND/OR/NOT are operators, a leading - is NOT, field:value scopes a term, "quoted" is a
// phrase (or a multi-word field value), and field:(...) distributes the field over the bare
// terms inside via a field stack. Lenient by construction.
function tokenize(raw: string): Token[] {
  const toks: Token[] = [];
  const fieldStack: (string | null)[] = [];
  let i = 0;
  const n = raw.length;
  const curField = (): string | null =>
    fieldStack.length ? fieldStack[fieldStack.length - 1] : null;
  const isSpace = (c: string): boolean => c === " " || c === "\t" || c === "\n" || c === "\r";
  while (i < n) {
    const c = raw.charAt(i);
    if (isSpace(c)) {
      i++;
      continue;
    }
    if (c === "(") {
      toks.push({ t: "(" });
      fieldStack.push(null);
      i++;
      continue;
    }
    if (c === ")") {
      toks.push({ t: ")" });
      if (fieldStack.length) fieldStack.pop();
      i++;
      continue;
    }
    if (c === "-" && i + 1 < n && !isSpace(raw.charAt(i + 1))) {
      toks.push({ t: "not" });
      i++;
      continue;
    }
    // Optional field: prefix (word chars then a colon).
    let field: string | null = null;
    let j = i;
    while (j < n && /[a-z0-9_.\-]/i.test(raw.charAt(j))) j++;
    if (j < n && raw.charAt(j) === ":" && j > i) {
      field = raw.slice(i, j).toLowerCase();
      i = j + 1;
      if (i < n && raw.charAt(i) === "(") {
        toks.push({ t: "(" });
        fieldStack.push(field);
        i++;
        continue;
      }
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
      while (i < n && !isSpace(raw.charAt(i)) && raw.charAt(i) !== "(" && raw.charAt(i) !== ")")
        i++;
      value = raw.slice(vs, i);
    }
    if (value === "" && field === null) continue;
    if (field === null && !phrase) {
      const up = value.toUpperCase();
      if (up === "AND") {
        toks.push({ t: "and" });
        continue;
      }
      if (up === "OR") {
        toks.push({ t: "or" });
        continue;
      }
      if (up === "NOT") {
        toks.push({ t: "not" });
        continue;
      }
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

// Recursive-descent parse into an AST. Precedence: NOT > AND (implicit or explicit) > OR.
// Leaves precompile their wildcard regex. Tolerant of stray/unbalanced tokens.
function parse(toks: Token[]): QueryNode | null {
  let pos = 0;
  const peek = (): Token | undefined => toks[pos];
  function parseOr(): QueryNode | null {
    const kids = [parseAnd()];
    let tk: Token | undefined;
    while ((tk = peek()) && tk.t === "or") {
      pos++;
      kids.push(parseAnd());
    }
    const k = kids.filter((x): x is QueryNode => x !== null);
    return k.length === 1 ? k[0] : k.length ? { op: "or", kids: k } : null;
  }
  function parseAnd(): QueryNode | null {
    const kids = [parseNot()];
    let tk: Token | undefined;
    while ((tk = peek()) && tk.t !== "or" && tk.t !== ")") {
      if (tk.t === "and") {
        pos++;
        const next = peek();
        if (!next || next.t === "or" || next.t === ")") break;
      }
      kids.push(parseNot());
    }
    const k = kids.filter((x): x is QueryNode => x !== null);
    return k.length === 1 ? k[0] : k.length ? { op: "and", kids: k } : null;
  }
  function parseNot(): QueryNode | null {
    const tk = peek();
    if (tk && tk.t === "not") {
      pos++;
      const k = parseNot();
      return k ? { op: "not", kid: k } : null;
    }
    return parsePrimary();
  }
  function parsePrimary(): QueryNode | null {
    const tk = peek();
    if (!tk) return null;
    if (tk.t === "(") {
      pos++;
      const inner = parseOr();
      const close = peek();
      if (close && close.t === ")") pos++;
      return inner;
    }
    if (tk.t === "term") {
      pos++;
      return {
        op: "term",
        field: tk.field,
        value: tk.value,
        phrase: tk.phrase,
        wildcard: tk.wildcard,
        re: tk.wildcard ? buildWild(tk.value, tk.field) : null,
      };
    }
    pos++; // stray ) / operator - skip and continue
    return parsePrimary();
  }
  return parseOr();
}

// The raw token stream for a query. Exported so a consumer can render a chip preview (the
// fields, phrases, brackets, and connectives the user typed) without re-implementing the lexer.
// The parsed AST stays module-internal - no consumer reads it - so it never leaks the private
// QueryNode type through the public surface.
export function buildQuery(raw: string): Token[] {
  return tokenize(raw);
}

// Does one term leaf match a record? tag = whole-tag equality (or regex); other fields are
// substring on that field; a free-text term matches the combined haystack.
function matchLeaf(leaf: TermNode, e: TextSearchEntry): boolean {
  const L = lc(e);
  if (leaf.field === "tag" || leaf.field === "tags") {
    for (let i = 0; i < L.tags.length; i++) {
      if (leaf.wildcard && leaf.re ? leaf.re.test(L.tags[i]) : L.tags[i] === leaf.value)
        return true;
    }
    return false;
  }
  const hay =
    leaf.field === "title"
      ? L.title
      : leaf.field === "description" || leaf.field === "desc"
        ? L.desc
        : leaf.field === "text" || leaf.field === "body"
          ? L.text
          : L.hay;
  return leaf.wildcard && leaf.re ? leaf.re.test(hay) : hay.indexOf(leaf.value) !== -1;
}

// Boolean inclusion: evaluate the AST against a record. Missing node = matches all.
function evalNode(node: QueryNode | null, e: TextSearchEntry): boolean {
  if (!node) return true;
  if (node.op === "term") return matchLeaf(node, e);
  if (node.op === "not") return !evalNode(node.kid, e);
  if (node.op === "and") {
    for (let i = 0; i < node.kids.length; i++) if (!evalNode(node.kids[i], e)) return false;
    return true;
  }
  if (node.op === "or") {
    for (let j = 0; j < node.kids.length; j++) if (evalNode(node.kids[j], e)) return true;
    return false;
  }
  return true;
}

// Relevance weight of one positive term leaf (field-weighted: title > tags > description >
// body, with a word-start / title-start bonus).
function scoreLeaf(leaf: TermNode, e: TextSearchEntry): number {
  const L = lc(e);
  const v = leaf.value;
  const hit = (hay: string, base: number, ws: number): number => {
    if (leaf.wildcard) return leaf.re && leaf.re.test(hay) ? base : 0;
    const idx = hay.indexOf(v);
    return idx === -1 ? 0 : base + (wordStart(hay, idx) ? ws : 0);
  };
  if (leaf.field === "tag" || leaf.field === "tags") return matchLeaf(leaf, e) ? 8 : 0;
  if (leaf.field === "title") {
    let s = hit(L.title, 10, 4);
    if (!leaf.wildcard && L.title.indexOf(v) === 0) s += 4;
    return s;
  }
  if (leaf.field === "description" || leaf.field === "desc") return hit(L.desc, 4, 2);
  if (leaf.field === "text" || leaf.field === "body") return hit(L.text, 2, 1);
  let free = hit(L.title, 10, 4) + hit(L.tagsJoined, 8, 3) + hit(L.desc, 4, 2) + hit(L.text, 2, 1);
  if (!leaf.wildcard && L.title.indexOf(v) === 0) free += 4;
  return free;
}

// Rank = sum of positively-reachable leaf weights (an even number of NOTs above it), so
// OR/NOT decide inclusion while the positive hits that landed decide ordering.
function scoreAst(node: QueryNode | null, e: TextSearchEntry, neg: boolean): number {
  if (!node) return 0;
  if (node.op === "term") return neg ? 0 : scoreLeaf(node, e);
  if (node.op === "not") return scoreAst(node.kid, e, !neg);
  if (node.op === "and" || node.op === "or") {
    let s = 0;
    for (let i = 0; i < node.kids.length; i++) s += scoreAst(node.kids[i], e, neg);
    return s;
  }
  return 0;
}

// Positive (non-negated) leaf values, for snippet/title highlighting.
export function getPositiveTerms(raw: string): string[] {
  const collect = (node: QueryNode | null, neg: boolean, acc: string[]): string[] => {
    if (!node) return acc;
    if (node.op === "term") {
      if (!neg && node.value) acc.push(node.value);
      return acc;
    }
    if (node.op === "not") return collect(node.kid, !neg, acc);
    node.kids.forEach((k) => {
      collect(k, neg, acc);
    });
    return acc;
  };
  return collect(parse(tokenize(raw)), false, []);
}

// One interpreted piece of a query, for the "how this parsed" preview: the scope (a field or
// free text), the value, and whether it is negated / a phrase / a wildcard.
export interface QueryPart {
  field: string | null;
  value: string;
  neg: boolean;
  phrase: boolean;
  wildcard: boolean;
}

// describeQuery walks the parsed AST and returns the interpreted parts in order, so a UI can
// show a user how their typed query was understood (field scoping, exclusions, phrases,
// wildcards). Returns [] for an empty/unparseable query. Reached through the createTextSearch
// factory (describeQuery method), not imported directly by any consumer.
function describeQuery(raw: string): QueryPart[] {
  const parts: QueryPart[] = [];
  const walk = (node: QueryNode | null, neg: boolean): void => {
    if (!node) return;
    if (node.op === "term") {
      parts.push({
        field: node.field,
        value: node.value,
        neg,
        phrase: node.phrase,
        wildcard: node.wildcard,
      });
      return;
    }
    if (node.op === "not") {
      walk(node.kid, !neg);
      return;
    }
    node.kids.forEach((k) => walk(k, neg));
  };
  walk(parse(tokenize(raw)), false);
  return parts;
}

// runSearch scores a caller-supplied index against the raw query and returns matches sorted
// by relevance (ties broken by shorter title). The index is INJECTED by the caller on every
// call - the engine holds no corpus of its own. Needs at least one positive term (a bare
// -exclusion or pure operators match nothing, avoiding a stray "-" dumping the corpus). The
// caller's record type flows through the generic, so results carry the full records back.
export function runSearch<E extends TextSearchEntry>(
  index: readonly E[],
  raw: string,
): TextSearchResult<E>[] {
  const tokens = tokenize(raw);
  const ast = parse(tokens);
  const pos: string[] = [];
  getPositiveTerms(raw).forEach((p) => pos.push(p));
  if (!pos.length) return [];
  const out: TextSearchResult<E>[] = [];
  for (let i = 0; i < index.length; i++) {
    if (evalNode(ast, index[i]))
      out.push({ entry: index[i], score: scoreAst(ast, index[i], false) });
  }
  // Typo-tolerant fallback: only when the query is a plain AND of bare words that found
  // nothing (no fields/operators/parens), rescue near-misses via subsequence on titles.
  const simple =
    tokens.length > 0 &&
    tokens.every((t) => t.t === "term" && t.field === null && !t.phrase && !t.wildcard);
  if (!out.length && simple) {
    index.forEach((e) => {
      let ok = true;
      let sc = 0;
      for (let k = 0; k < pos.length; k++) {
        if (subseq(pos[k], lc(e).title)) sc += 2;
        else {
          ok = false;
          break;
        }
      }
      if (ok) out.push({ entry: e, score: sc });
    });
  }
  // Tie-break on shorter title. Guard a missing title (a malformed record) so one bad entry
  // cannot throw and take down the whole search.
  out.sort(
    (a, b) => b.score - a.score || (a.entry.title || "").length - (b.entry.title || "").length,
  );
  return out;
}

// A one-line excerpt centered on the first matched term (or the opening when no term appears,
// e.g. a pure tag: filter). Callers pass the raw field text and the positive terms; the
// engine returns plain text (a caller adds its own highlighting). Assumes already-cleaned
// text (fenced code stripped, whitespace collapsed). Reached through the createTextSearch
// factory (buildSnippet method), not imported directly by any consumer.
function buildSnippet(text: string, terms: string[], windowChars = 150): string {
  if (!text) return "";
  const low = text.toLowerCase();
  let pos = -1;
  for (let i = 0; i < terms.length; i++) {
    const p = low.indexOf(terms[i]);
    if (p !== -1 && (pos === -1 || p < pos)) pos = p;
  }
  let start = pos === -1 ? 0 : Math.max(0, pos - 50);
  if (start > 0) {
    const sp = text.indexOf(" ", start);
    if (sp !== -1 && sp - start < 20) start = sp + 1;
  }
  return (
    (start > 0 ? "..." : "") +
    text.slice(start, start + windowChars) +
    (start + windowChars < text.length ? "..." : "")
  );
}

// --- Injected-source searcher --------------------------------------------------------------
// createTextSearch bundles a caller-supplied data source with the grammar into a small
// searcher object, so an app need not thread the index through every call. The source is the
// injected dependency: either the records themselves (ready immediately) or a loader the app
// owns (its own retrieval, cache path, and locations - none of which live here). load() resolves
// the loader once and memoizes it; a failed/empty load leaves the searcher not-ready so a later
// load() retries rather than wedging. runSearch() ranks the loaded records, or returns null
// before they land (distinct from an empty array, which means "loaded, no matches").

// The data a searcher is built over: a ready array, or a loader the caller owns. The loader
// may be sync or async, takes an optional AbortSignal (so the caller can cancel a torn-down or
// superseded load), and returns null when the records are unavailable (the app decides what
// "unavailable" means - a 404, an offline dev build; the engine only sees null).
export type TextSearchSource<E extends TextSearchEntry> =
  | readonly E[]
  | ((signal?: AbortSignal) => readonly E[] | null | Promise<readonly E[] | null>);

export interface TextSearcher<E extends TextSearchEntry> {
  load(signal?: AbortSignal): Promise<readonly E[] | null>;
  isReady(): boolean;
  entries(): readonly E[] | null;
  runSearch(query: string): TextSearchResult<E>[] | null;
  getPositiveTerms(query: string): string[];
  describeQuery(query: string): QueryPart[];
  buildSnippet(text: string, terms: string[], windowChars?: number): string;
}

export function createTextSearch<E extends TextSearchEntry>(
  source: TextSearchSource<E>,
): TextSearcher<E> {
  const loader = typeof source === "function" ? source : null;
  let cached: readonly E[] | null = typeof source === "function" ? null : source;
  let inflight: Promise<readonly E[] | null> | null = null;

  const load = (signal?: AbortSignal): Promise<readonly E[] | null> => {
    if (cached) return Promise.resolve(cached);
    if (!loader) return Promise.resolve(null);
    if (inflight) return inflight;
    const p = Promise.resolve(loader(signal)).then((data) => {
      if (data) cached = data;
      return data;
    });
    // Clear inflight once the load settles, whether it fulfilled, rejected, or yielded null.
    // A rejected or null load leaves `cached` null, so isReady() stays false and the next
    // load() re-runs the loader rather than replaying the same failed/empty promise forever.
    inflight = p.finally(() => {
      inflight = null;
    });
    return inflight;
  };

  return {
    load,
    isReady: () => cached !== null,
    entries: () => cached,
    runSearch: (query) => (cached ? runSearch(cached, query) : null),
    getPositiveTerms: (query) => getPositiveTerms(query),
    describeQuery: (query) => describeQuery(query),
    buildSnippet: (text, terms, windowChars) => buildSnippet(text, terms, windowChars),
  };
}
