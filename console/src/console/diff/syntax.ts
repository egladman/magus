// syntax.ts - a small, dependency-free tokenizer for diff line text.
//
// WHY HAND-ROLLED. The console is served under a strict CSP with no CDN and no external
// script origin, so highlighting is a BUNDLED tokenizer or it does not exist. It did not
// exist: the original scoping wrote it off, and a diff viewer whose whole job is making a
// change readable rendered every language as undifferentiated text. A full grammar engine
// (Prism, highlight.js, Shiki) is tens to hundreds of kilobytes shipped into a surface that
// already ships a virtualizer, and it buys precision this context cannot use - a diff line is
// a FRAGMENT, so no tokenizer can be correct about it anyway (see below).
//
// WHAT IT DELIBERATELY DOES NOT DO. A diff line has no context: the hunk above it may be
// missing, so an unterminated block comment, a template literal spanning lines, or a here-doc
// simply cannot be resolved from one line. Rather than carry cross-line state that would be
// wrong whenever a hunk starts mid-construct, each line is tokenized alone and anything
// ambiguous falls back to plain text. Under-highlighting is invisible; a confidently wrong
// colour is worse than none, because it teaches the reader to distrust the whole column.
//
// The output is RANGES, not markup. The renderer composes them with the intra-line emphasis
// from words.ts - syntax owns the foreground colour, emphasis owns the background - so the two
// signals never fight over the same property.

/// Token classes. Kept few on purpose: more classes means more palette, and a diff already
/// spends its colour budget on add/delete.
export type TokenClass = "kw" | "str" | "num" | "com" | "fn" | "";

export interface Token {
  readonly start: number;
  readonly end: number;
  readonly cls: TokenClass;
}

export type Language =
  | "go"
  | "ts"
  | "buzz"
  | "rust"
  | "python"
  | "shell"
  | "json"
  | "yaml"
  | "toml"
  | "css"
  | "markdown"
  | "none";

const KEYWORDS: Partial<Record<Language, readonly string[]>> = {
  go: `break case chan const continue default defer else fallthrough for func go goto if import
       interface map package range return select struct switch type var nil true false iota
       string int int8 int16 int32 int64 uint uint8 uint16 uint32 uint64 float32 float64 byte
       rune bool error any make new len cap append copy delete panic recover`.split(/\s+/),
  ts: `abstract as async await break case catch class const continue declare default delete do
       else enum export extends false finally for from function get if implements import in
       instanceof interface let new null of private protected public readonly return satisfies
       set static super switch this throw true try type typeof undefined var void while yield
       keyof infer never unknown string number boolean object symbol bigint`.split(/\s+/),
  buzz: `fun var final const if else while for foreach in return break continue import export
         object enum protocol extern test zdef resolve fiber resume yield try catch throw
         true false null void bool int double str pat ud any range from as is`.split(/\s+/),
  rust: `as async await break const continue crate dyn else enum extern false fn for if impl in
         let loop match mod move mut pub ref return self Self static struct super trait true
         type unsafe use where while String Vec Option Result Some None Ok Err`.split(/\s+/),
  python: `and as assert async await break class continue def del elif else except False finally
           for from global if import in is lambda None nonlocal not or pass raise return True
           try while with yield self`.split(/\s+/),
  shell: `if then else elif fi for while do done case esac function return exit local export
          readonly declare set unset shift source eval exec trap echo cd test`.split(/\s+/),
  yaml: ["true", "false", "null", "yes", "no", "on", "off"],
  toml: ["true", "false"],
  json: ["true", "false", "null"],
  css: [],
  markdown: [],
};

// Line-comment openers per language. Order matters only in that the first match wins.
const LINE_COMMENT: Partial<Record<Language, readonly string[]>> = {
  go: ["//"],
  ts: ["//"],
  buzz: ["//"],
  rust: ["//"],
  python: ["#"],
  shell: ["#"],
  yaml: ["#"],
  toml: ["#"],
  css: [],
  json: [],
  markdown: [],
};

const QUOTES: Partial<Record<Language, readonly string[]>> = {
  go: ['"', "`", "'"],
  ts: ['"', "'", "`"],
  buzz: ['"', "'"],
  rust: ['"', "'"],
  python: ['"', "'"],
  shell: ['"', "'"],
  yaml: ['"', "'"],
  toml: ['"', "'"],
  json: ['"'],
  css: ['"', "'"],
  markdown: [],
};

const BY_EXTENSION: Record<string, Language> = {
  go: "go",
  ts: "ts",
  tsx: "ts",
  js: "ts",
  mjs: "ts",
  cjs: "ts",
  jsx: "ts",
  buzz: "buzz",
  bo: "buzz",
  rs: "rust",
  py: "python",
  sh: "shell",
  bash: "shell",
  zsh: "shell",
  json: "json",
  yaml: "yaml",
  yml: "yaml",
  toml: "toml",
  css: "css",
  md: "markdown",
};

/// languageFor maps a path to a tokenizer, or "none" when magus has no business guessing.
export function languageFor(path: string): Language {
  const base = path.slice(path.lastIndexOf("/") + 1);
  if (base === "magusfile.buzz" || base.endsWith(".buzz")) return "buzz";
  if (base === "Dockerfile" || base.startsWith("Dockerfile.")) return "none";
  const dot = base.lastIndexOf(".");
  if (dot < 0) return "none";
  return BY_EXTENSION[base.slice(dot + 1).toLowerCase()] ?? "none";
}

const isIdentStart = (c: string): boolean => /[A-Za-z_$]/.test(c);
const isIdent = (c: string): boolean => /[A-Za-z0-9_$]/.test(c);
const isDigit = (c: string): boolean => /[0-9]/.test(c);

// blockCommentLang reports the languages whose block comments use the /* ... */ shape.
function blockCommentLang(lang: Language): boolean {
  return lang === "go" || lang === "ts" || lang === "rust" || lang === "css";
}

/// tokenize splits one line into non-overlapping ranges, in order. Ranges with class "" are
/// omitted: the renderer treats any gap as plain text, so the common case allocates nothing.
export function tokenize(text: string, lang: Language): Token[] {
  if (lang === "none" || text === "") return [];
  const keywords = new Set(KEYWORDS[lang] ?? []);
  const lineComments = LINE_COMMENT[lang] ?? [];
  const quotes = QUOTES[lang] ?? [];
  const out: Token[] = [];

  // A continuation line of a block comment. The opener is in a line the hunk may not include,
  // so it cannot be derived - but the convention is unambiguous enough to read: a leading "*"
  // is a comment body, and a leading "*" in real code would be a dereference or a multiply,
  // neither of which starts a line in any language handled here.
  if (blockCommentLang(lang)) {
    const lead = text.trimStart();
    if (lead.startsWith("*")) {
      return [{ start: text.length - lead.length, end: text.length, cls: "com" }];
    }
  }

  let i = 0;
  while (i < text.length) {
    const c = text[i] ?? "";

    // A line comment runs to end of line, so it ends the scan.
    const opener = lineComments.find((o) => text.startsWith(o, i));
    if (opener !== undefined) {
      out.push({ start: i, end: text.length, cls: "com" });
      break;
    }

    // Block comments. Closed on this line is exact; UNCLOSED runs to end of line, which is the
    // one cross-line inference worth making. Every convention in every language here continues
    // a block comment on the following lines, so an opener with no closer is a comment for the
    // rest of the line with near-certainty - and this codebase is dense with long comment
    // blocks, so refusing to colour them left the most common thing on screen unstyled.
    if (blockCommentLang(lang) && text.startsWith("/*", i)) {
      const close = text.indexOf("*/", i + 2);
      out.push({ start: i, end: close < 0 ? text.length : close + 2, cls: "com" });
      if (close < 0) break;
      i = close + 2;
      continue;
    }

    if (quotes.includes(c)) {
      let j = i + 1;
      let closed = false;
      while (j < text.length) {
        if (text[j] === "\\") {
          j += 2;
          continue;
        }
        if (text[j] === c) {
          closed = true;
          break;
        }
        j++;
      }
      // An unterminated quote is a fragment; leave it plain rather than painting the rest of
      // the line as a string.
      if (!closed) break;
      out.push({ start: i, end: j + 1, cls: "str" });
      i = j + 1;
      continue;
    }

    if (isDigit(c)) {
      let j = i;
      while (j < text.length && /[0-9a-fA-FxXoObB._]/.test(text[j] ?? "")) j++;
      out.push({ start: i, end: j, cls: "num" });
      i = j;
      continue;
    }

    if (isIdentStart(c)) {
      let j = i;
      while (j < text.length && isIdent(text[j] ?? "")) j++;
      const word = text.slice(i, j);
      if (keywords.has(word)) {
        out.push({ start: i, end: j, cls: "kw" });
      } else if (text[j] === "(") {
        // A call or a declaration. Not resolved further: telling the two apart needs a parser,
        // and both read usefully as "the thing being invoked here".
        out.push({ start: i, end: j, cls: "fn" });
      }
      i = j;
      continue;
    }

    i++;
  }
  return out;
}
