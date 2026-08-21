// markdown.ts - renders a note's prose the way it was written.
//
// A note is a markdown file, and markdown's paragraph rule is the point: a single newline
// inside a paragraph is a SOFT break that reflows, and only a blank line starts a new one.
// Printing the source verbatim ignores that, so a file hard-wrapped at 90 columns - which is
// most of them, because people write markdown in editors - breaks mid-sentence at whatever
// width the pane happens to be. That is what a reader sees as "broken", and it is why every
// forge renders READMEs rather than showing them.
//
// UNTRUSTED, and that is why this builds DOM rather than HTML. The body is prose out of a file
// magus did not write, and the proto says so out loud: a client must render it as text, never
// as trusted HTML. Every node here comes from createElement with textContent, so there is no
// string of markup for a `<script>` in someone's note to ride in on - the guarantee is
// structural rather than a sanitizer that has to be right every time. Raw HTML in the source is
// deliberately NOT passed through; it renders as the characters it is.
//
// A SUBSET, on purpose: paragraphs, ATX and setext headings, fenced code, lists, blockquotes,
// rules, and inline code/emphasis/links. Anything unrecognised degrades to its own text rather
// than disappearing, because a note losing a line is worse than a note showing a stray asterisk.
// No dependency: the console carries no markdown library, and a parser plus a sanitizer is a
// large supply-chain surface for a feature this bounded.

// SAFE_LINK matches the schemes a link may use. Anything else - javascript:, data:, vbscript: -
// renders as plain text with its label intact, so a hostile href cannot become a live control.
const SAFE_LINK = /^(https?:\/\/|mailto:|\/|\.{1,2}\/|#)/i;

const FENCE = /^(?:```|~~~)/;
const ATX = /^(#{1,6})\s+(.*)$/;
const SETEXT = /^(=+|-{3,})\s*$/;
const RULE = /^(?:\*{3,}|_{3,}|-{3,})\s*$/;
const BULLET = /^\s{0,3}[-*+]\s+(.*)$/;
const ORDERED = /^\s{0,3}(\d{1,9})[.)]\s+(.*)$/;
const QUOTE = /^\s{0,3}>\s?(.*)$/;

// renderMarkdown returns the note's prose as nodes, ready to append.
export function renderMarkdown(src: string): DocumentFragment {
  const out = document.createDocumentFragment();
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  let i = 0;

  const el = (tag: string, cls?: string): HTMLElement => {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    return e;
  };

  while (i < lines.length) {
    const line = lines[i] ?? "";

    if (line.trim() === "") {
      i++;
      continue;
    }

    // Fenced code first: everything inside is literal, including lines that would otherwise
    // read as headings or list items.
    if (FENCE.test(line.trim())) {
      const fence = line.trim().slice(0, 3);
      const pre = el("pre", "console-notes-app__md-code");
      const code = document.createElement("code");
      const body: string[] = [];
      i++;
      while (i < lines.length && !(lines[i] ?? "").trim().startsWith(fence)) {
        body.push(lines[i] ?? "");
        i++;
      }
      i++; // the closing fence, or the end of the note if it was never closed
      code.textContent = body.join("\n");
      pre.append(code);
      out.append(pre);
      continue;
    }

    const atx = ATX.exec(line);
    if (atx) {
      const level = Math.min(6, (atx[1] ?? "#").length);
      // h3 and down: the note's own title is the h2 above this prose, so a `#` in the body is
      // a section within it rather than a peer of the title.
      const h = el("h" + Math.min(6, level + 2), "console-notes-app__md-h");
      h.append(inline(atx[2] ?? ""));
      out.append(h);
      i++;
      continue;
    }

    // Setext: a text line underlined by = or ---. Checked before the rule and before a
    // paragraph, because the underline belongs to the line above it.
    const next = lines[i + 1] ?? "";
    if (line.trim() !== "" && SETEXT.test(next.trim()) && !BULLET.test(line) && !QUOTE.test(line)) {
      const h = el(next.trim().startsWith("=") ? "h3" : "h4", "console-notes-app__md-h");
      h.append(inline(line.trim()));
      out.append(h);
      i += 2;
      continue;
    }

    if (RULE.test(line.trim())) {
      out.append(el("hr", "console-notes-app__md-rule"));
      i++;
      continue;
    }

    if (QUOTE.test(line)) {
      const quote = el("blockquote", "console-notes-app__md-quote");
      const body: string[] = [];
      while (i < lines.length && QUOTE.test(lines[i] ?? "")) {
        body.push(QUOTE.exec(lines[i] ?? "")?.[1] ?? "");
        i++;
      }
      quote.append(renderMarkdown(body.join("\n")));
      out.append(quote);
      continue;
    }

    if (BULLET.test(line) || ORDERED.test(line)) {
      const ordered = ORDERED.test(line);
      const list = el(ordered ? "ol" : "ul", "console-notes-app__md-list");
      while (i < lines.length) {
        const cur = lines[i] ?? "";
        const m = ordered ? ORDERED.exec(cur) : BULLET.exec(cur);
        if (!m) break;
        const item = document.createElement("li");
        // Continuation lines reflow into the item, which is markdown's soft-break rule applied
        // one level in - a wrapped bullet is still one bullet.
        const text: string[] = [ordered ? (m[2] ?? "") : (m[1] ?? "")];
        i++;
        while (i < lines.length) {
          const cont = lines[i] ?? "";
          if (cont.trim() === "" || BULLET.test(cont) || ORDERED.test(cont)) break;
          text.push(cont.trim());
          i++;
        }
        item.append(inline(text.join(" ")));
        list.append(item);
      }
      out.append(list);
      continue;
    }

    // A paragraph: every line up to the next blank one or the next block, joined with spaces.
    // This join IS the fix - it is what turns a hard-wrapped source file back into prose that
    // reflows to the reader's pane.
    const para: string[] = [];
    while (i < lines.length) {
      const cur = lines[i] ?? "";
      if (cur.trim() === "") break;
      if (FENCE.test(cur.trim()) || ATX.test(cur) || RULE.test(cur.trim())) break;
      if (BULLET.test(cur) || ORDERED.test(cur) || QUOTE.test(cur)) break;
      if (SETEXT.test((lines[i + 1] ?? "").trim()) && para.length > 0) break;
      para.push(cur.trim());
      i++;
    }
    if (para.length > 0) {
      const p = el("p", "console-notes-app__md-p");
      p.append(inline(para.join(" ")));
      out.append(p);
    }
  }
  return out;
}

// INLINE splits a line into the spans that carry formatting. Code first, so an asterisk inside
// backticks stays an asterisk.
//
// The link href allows ONE level of balanced parentheses, which both CommonMark and real URLs
// need - a Wikipedia link ends in "(disambiguation)". Stopping at the first `)` instead left the
// closing paren behind in the sentence: `[the docs](javascript:alert(1))` rendered as
// "see the docs) for more". The link was correctly refused either way; the stray bracket was the
// bug, and the test that found it was the one checking the refusal.
const INLINE =
  /(`[^`]+`)|(\*\*[^*]+\*\*)|(__[^_]+__)|(\*[^*\n]+\*)|(_[^_\n]+_)|(\[[^\]]*\]\((?:[^()\s]|\([^()\s]*\))*\))/;

// inline renders one line's text, emphasis, code and links.
function inline(src: string): DocumentFragment {
  const out = document.createDocumentFragment();
  let rest = src;
  while (rest !== "") {
    const m = INLINE.exec(rest);
    if (!m || m.index === undefined) {
      out.append(document.createTextNode(rest));
      break;
    }
    if (m.index > 0) out.append(document.createTextNode(rest.slice(0, m.index)));
    const tok = m[0];
    rest = rest.slice(m.index + tok.length);

    if (tok.startsWith("`")) {
      const code = document.createElement("code");
      code.textContent = tok.slice(1, -1);
      out.append(code);
      continue;
    }
    if (tok.startsWith("[")) {
      out.append(link(tok));
      continue;
    }
    const strong = tok.startsWith("**") || tok.startsWith("__");
    const em = document.createElement(strong ? "strong" : "em");
    em.textContent = tok.slice(strong ? 2 : 1, strong ? -2 : -1);
    out.append(em);
  }
  return out;
}

// link renders `[label](href)`, or the raw text when the href is not a scheme worth following.
// A rejected link keeps its LABEL rather than vanishing: the words were part of the sentence.
function link(token: string): Node {
  const split = token.indexOf("](");
  const label = token.slice(1, split);
  const href = token.slice(split + 2, -1);
  if (!SAFE_LINK.test(href)) return document.createTextNode(label || href);
  const a = document.createElement("a");
  a.href = href;
  a.textContent = label || href;
  a.rel = "noreferrer noopener";
  if (/^https?:/i.test(href)) a.target = "_blank";
  return a;
}
