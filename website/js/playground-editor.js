// Playground editor: upgrades the Go-driven textarea into a CodeMirror 6 editor
// with IDE affordances - inline diagnostics, semantic autocompletion, and hover -
// all backed by the WebAssembly interpreter's globalThis.buzz language service
// (see cmd/buzz-playground: buzz.diagnostics / complete / hover).
//
// This is progressive enhancement. The original textarea (#src) plus its Go-owned
// overlay and console keep working; CodeMirror mounts on top, becomes the visible
// editor, and mirrors its content into #src so every existing Go wiring
// (parse badge, `run`/`eval` console) stays live and untouched. If this module
// throws or the bundle fails to load, the page falls back to the textarea editor.
//
// esbuild bundles this file into playground/editor.js (a committed artifact, like
// buzz.wasm); see website/package.json `build-editor`. Only the playground page
// loads it.

import { EditorState } from "@codemirror/state";
import {
  EditorView, keymap, lineNumbers, highlightActiveLine,
  highlightActiveLineGutter, drawSelection, hoverTooltip,
} from "@codemirror/view";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import {
  StreamLanguage, syntaxHighlighting, HighlightStyle, bracketMatching, indentUnit,
} from "@codemirror/language";
import { autocompletion, completionKeymap, closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import { linter, lintKeymap, lintGutter, forceLinting } from "@codemirror/lint";
import { tags as t } from "@lezer/highlight";

// The reserved Buzz keywords, mirrored from gopherbuzz/token (token.Keywords).
// Used only for display highlighting; completion pulls the canonical set from the
// wasm, so a drift here is cosmetic.
const KEYWORDS = new Set([
  "import", "export", "final", "var", "mut", "fun", "return", "true", "false",
  "null", "void", "if", "else", "while", "for", "foreach", "in", "break",
  "continue", "and", "or", "object", "enum", "is", "as", "do", "until", "try",
  "catch", "throw", "yield", "resume", "resolve", "namespace",
]);

// buzzLanguage is a display-only stream tokenizer, the CodeMirror analog of the
// Go highlight overlay it replaces: comments, strings, numbers, and keyword vs
// identifier. Semantic accuracy is the language service's job, not the highlighter.
const buzzLanguage = StreamLanguage.define({
  name: "buzz",
  token(stream) {
    if (stream.eatSpace()) return null;
    if (stream.match("//")) { stream.skipToEnd(); return "comment"; }
    if (stream.match("/*")) {
      let prev = "";
      while (!stream.eol()) {
        const ch = stream.next();
        if (prev === "*" && ch === "/") return "comment";
        prev = ch;
      }
      return "comment"; // unterminated block: color to end of line
    }
    if (stream.match(/^"(?:[^"\\]|\\.)*"?/)) return "string";
    if (stream.match(/^\d+(?:\.\d+)?/)) return "number";
    if (stream.match(/^[A-Za-z_][A-Za-z0-9_]*/)) {
      return KEYWORDS.has(stream.current()) ? "keyword" : "variableName";
    }
    stream.next();
    return null;
  },
});

// Colors reuse the One Light / One Dark palette the page already pins as CSS
// variables (--hl-*), so the editor follows the site's light/dark mode with no
// extra rules - the same variables the old overlay used.
const buzzHighlight = HighlightStyle.define([
  { tag: t.keyword, color: "var(--hl-kw)" },
  { tag: [t.string, t.special(t.string)], color: "var(--hl-str)" },
  { tag: [t.number, t.bool, t.null], color: "var(--hl-num)" },
  { tag: [t.comment, t.lineComment, t.blockComment], color: "var(--hl-com)", fontStyle: "italic" },
]);

// editorTheme aligns CodeMirror's chrome with the playground panel: transparent
// background (the card shows through), Pico's monospace metrics, and selection /
// caret / gutter colors from Pico variables so both themes are covered.
const editorTheme = EditorView.theme({
  "&": {
    height: "100%",
    backgroundColor: "transparent",
    color: "var(--pico-color)",
    fontSize: "var(--code-size)",
  },
  ".cm-scroller": {
    fontFamily: "var(--pico-font-family-monospace)",
    lineHeight: "var(--code-lh)",
  },
  ".cm-content": { padding: "var(--code-pad) 0" },
  ".cm-gutters": {
    backgroundColor: "var(--pico-card-sectioning-background-color)",
    color: "var(--pico-muted-color)",
    border: "none",
    borderRight: "1px solid var(--pico-muted-border-color)",
  },
  ".cm-activeLine, .cm-activeLineGutter": {
    backgroundColor: "color-mix(in srgb, var(--pico-muted-border-color) 22%, transparent)",
  },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--pico-color)" },
  "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground":
    { backgroundColor: "color-mix(in srgb, var(--pico-primary) 24%, transparent)" },
  "&.cm-focused": { outline: "none" },
  ".cm-tooltip": {
    border: "1px solid var(--pico-muted-border-color)",
    borderRadius: "var(--pico-border-radius)",
    backgroundColor: "var(--pico-card-background-color)",
    color: "var(--pico-color)",
  },
  ".cm-tooltip.cm-tooltip-autocomplete > ul > li": {
    fontFamily: "var(--pico-font-family-monospace)",
  },
});

// utf8Offset converts a CodeMirror position (a UTF-16 code-unit index into the
// document string) to the UTF-8 byte offset the Go language service expects.
function utf8Offset(docText, pos) {
  return new TextEncoder().encode(docText.slice(0, pos)).length;
}

// cmKind maps a language-service completion kind to a CodeMirror completion type
// (which drives the item's icon). Unknown kinds fall back to "variable".
function cmKind(kind) {
  switch (kind) {
    case "module": return "namespace";
    case "method": return "method";
    case "field": return "property";
    case "function": return "function";
    case "constant": return "constant";
    case "type": return "type";
    case "keyword": return "keyword";
    default: return "variable";
  }
}

// buzzCompletionSource asks the wasm language service for completions at the
// cursor. It returns null (no popup) until the interpreter has booted or when the
// service offers nothing, so it is safe to register before the wasm is ready.
function buzzCompletionSource(ctx) {
  const buzz = globalThis.buzz;
  if (!buzz || !buzz.complete) return null;
  const docText = ctx.state.doc.toString();
  const items = buzz.complete(docText, utf8Offset(docText, ctx.pos)) || [];
  if (items.length === 0) return null;
  // Every item completes the same token, so they share one replace length.
  const from = ctx.pos - (items[0].replace || 0);
  return {
    from,
    options: items.map((it) => ({
      label: it.label,
      type: cmKind(it.kind),
      detail: it.detail || undefined,
      info: it.doc || undefined,
    })),
    validFor: /^[A-Za-z0-9_]*$/,
  };
}

// diagRange maps a 1-based line/col from the language service to a document range,
// underlining the identifier at that position (or one character when there is no
// word there, e.g. an unexpected token), clamped to the line.
function diagRange(doc, line, col) {
  const ln = doc.line(Math.min(Math.max(line || 1, 1), doc.lines));
  const start = Math.min(ln.from + Math.max((col || 1) - 1, 0), ln.to);
  let end = start;
  const text = ln.text;
  let i = start - ln.from;
  while (i < text.length && /[A-Za-z0-9_]/.test(text[i])) { i++; end++; }
  if (end === start) end = Math.min(start + 1, ln.to);
  if (end === start && ln.to > ln.from) return { from: Math.max(ln.to - 1, ln.from), to: ln.to };
  return { from: start, to: end };
}

// buzzLinter runs the wasm diagnostics on a debounce and paints each as a squiggle.
const buzzLinter = linter(
  (view) => {
    const buzz = globalThis.buzz;
    if (!buzz || !buzz.diagnostics) return [];
    const doc = view.state.doc;
    const ds = buzz.diagnostics(doc.toString()) || [];
    return ds.map((d) => {
      const { from, to } = diagRange(doc, d.line, d.col);
      return { from, to, severity: "error", message: d.msg || "error" };
    });
  },
  { delay: 300 },
);

// buzzHover shows the language service's hover (a signature and doc) for the
// symbol under the pointer.
const buzzHover = hoverTooltip((view, pos) => {
  const buzz = globalThis.buzz;
  if (!buzz || !buzz.hover) return null;
  const docText = view.state.doc.toString();
  const h = buzz.hover(docText, utf8Offset(docText, pos));
  if (!h) return null;
  return {
    pos,
    create() {
      const dom = document.createElement("div");
      dom.className = "cm-buzz-hover";
      const title = document.createElement("div");
      title.className = "cm-buzz-hover-title";
      title.textContent = h.title;
      dom.appendChild(title);
      if (h.doc) {
        const body = document.createElement("div");
        body.className = "cm-buzz-hover-doc";
        body.textContent = h.doc;
        dom.appendChild(body);
      }
      return { dom };
    },
  };
});

// mount wires a CodeMirror view over the textarea and keeps the two in sync. The
// textarea stays the Go side's source of truth: CodeMirror writes into it (and
// dispatches input) on every edit, and reflects external writes back (the Go
// wasm seeds the example and applies a #source= deep link by setting .value; the
// draft-restore script dispatches input). A `syncing` guard breaks the loop.
function mount(srcEl, area, panel) {
  let syncing = false;

  const mountEl = document.createElement("div");
  mountEl.id = "cm-root";
  area.appendChild(mountEl);

  const view = new EditorView({
    parent: mountEl,
    state: EditorState.create({
      doc: srcEl.value,
      extensions: [
        lineNumbers(),
        highlightActiveLineGutter(),
        highlightActiveLine(),
        history(),
        drawSelection(),
        bracketMatching(),
        closeBrackets(),
        indentUnit.of("    "),
        buzzLanguage,
        syntaxHighlighting(buzzHighlight),
        editorTheme,
        lintGutter(),
        buzzLinter,
        autocompletion({ override: [buzzCompletionSource], activateOnTyping: true }),
        buzzHover,
        keymap.of([
          ...closeBracketsKeymap,
          ...defaultKeymap,
          ...historyKeymap,
          ...completionKeymap,
          ...lintKeymap,
          indentWithTab,
        ]),
        EditorView.updateListener.of((u) => {
          if (!u.docChanged) return;
          syncing = true;
          srcEl.value = u.state.doc.toString();
          srcEl.dispatchEvent(new Event("input", { bubbles: true }));
          syncing = false;
        }),
      ],
    }),
  });

  // Reflect external writes to the textarea (draft restore dispatches input).
  srcEl.addEventListener("input", () => {
    if (syncing) return;
    const v = srcEl.value;
    if (v !== view.state.doc.toString()) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } });
    }
  });

  // The Go wasm seeds the editor and applies a deep link by setting .value with no
  // event, and it does so after booting. Poll briefly so that seeded content lands
  // in CodeMirror; once the user edits, CodeMirror is authoritative and this is a
  // no-op (the textarea already mirrors it).
  let ticks = 0;
  const seed = setInterval(() => {
    if (!syncing) {
      const v = srcEl.value;
      if (v !== "" && v !== view.state.doc.toString()) {
        view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } });
      }
    }
    if (++ticks > 40) clearInterval(seed); // ~2s
  }, 50);

  // Once the interpreter is up, force an initial lint so the seeded file gets
  // squiggles without waiting for the first keystroke.
  const wait = setInterval(() => {
    if (globalThis.buzz && globalThis.buzz.diagnostics) {
      clearInterval(wait);
      forceLinting(view);
    }
  }, 100);

  panel.classList.add("cm-active");
  return view;
}

function init() {
  const srcEl = document.getElementById("src");
  if (!srcEl) return; // not the playground page
  const area = srcEl.closest(".editor-area");
  const panel = srcEl.closest(".panel");
  if (!area || !panel) return;
  try {
    mount(srcEl, area, panel);
  } catch (err) {
    // Leave the textarea editor in place as the fallback.
    console.error("playground: CodeMirror init failed, using textarea editor", err);
  }
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init);
} else {
  init();
}
