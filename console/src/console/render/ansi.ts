// ansi.ts - pure text parsing shared by the log viewer's renderers: ANSI SGR colour
// parsing (the captured output may carry escapes) and the leading magus status token
// ("[pass]" / "[fail]" ...) that the pretty view promotes to a badge. No DOM here - the
// DOM fill lives in render.ts; this module is a pure leaf so the model layer can strip
// ANSI without pulling in rendering.

// A run of text sharing one SGR state, with the CSS classes that colour it.
export interface AnsiSeg {
  text: string;
  cls: string[];
}

// The SGR state tracked across a line.
interface SgrState {
  bold: boolean;
  dim: boolean;
  italic: boolean;
  underline: boolean;
  fg: string | null;
}

// STATUS_RE matches a leading magus status token like "[pass]" / "[fail]" - the
// "brackets" we render as a colored badge instead. statusToken returns the bare word.
export const STATUS_RE = /^\s*\[(pass|fail|warn|error|info|dry|summary|cached)\]/i;

export function statusToken(raw: string): string {
  const m = STATUS_RE.exec(stripAnsi(raw));
  return m ? m[1].toLowerCase() : "";
}

// --- ANSI SGR parsing ---------------------------------------------------------
const ANSI_RE = /\x1b\[([0-9;]*)m/g;

export function stripAnsi(s: string): string {
  return s.replace(ANSI_RE, "");
}

const FG: Record<number, string> = {
  30: "console-render-ansi__fg--black",
  31: "console-render-ansi__fg--red",
  32: "console-render-ansi__fg--green",
  33: "console-render-ansi__fg--yellow",
  34: "console-render-ansi__fg--blue",
  35: "console-render-ansi__fg--magenta",
  36: "console-render-ansi__fg--cyan",
  37: "console-render-ansi__fg--white",
  90: "console-render-ansi__fg--black",
  91: "console-render-ansi__fg--red",
  92: "console-render-ansi__fg--green",
  93: "console-render-ansi__fg--yellow",
  94: "console-render-ansi__fg--blue",
  95: "console-render-ansi__fg--magenta",
  96: "console-render-ansi__fg--cyan",
  97: "console-render-ansi__fg--white",
};

// parseAnsi splits a line into {text, cls[]} runs by tracking SGR state across the
// line. Only the attributes magus emits (bold, dim, italic, underline, basic fg
// colors) are mapped; anything else is ignored so unknown codes never leak through.
export function parseAnsi(line: string): AnsiSeg[] {
  const out: AnsiSeg[] = [];
  const state: SgrState = { bold: false, dim: false, italic: false, underline: false, fg: null };
  let last = 0;
  let m: RegExpExecArray | null;
  ANSI_RE.lastIndex = 0;
  const push = (text: string): void => {
    if (!text) return;
    out.push({ text, cls: classesFor(state) });
  };
  while ((m = ANSI_RE.exec(line)) !== null) {
    push(line.slice(last, m.index));
    applySGR(state, m[1]);
    last = ANSI_RE.lastIndex;
  }
  push(line.slice(last));
  if (!out.length) out.push({ text: "", cls: [] });
  return out;
}

function applySGR(state: SgrState, params: string): void {
  const codes = params === "" ? [0] : params.split(";").map((n) => parseInt(n, 10));
  for (const c of codes) {
    if (c === 0) {
      state.bold = state.dim = state.italic = state.underline = false;
      state.fg = null;
    } else if (c === 1) state.bold = true;
    else if (c === 2) state.dim = true;
    else if (c === 3) state.italic = true;
    else if (c === 4) state.underline = true;
    else if (c === 22) {
      state.bold = false;
      state.dim = false;
    } else if (c === 23) state.italic = false;
    else if (c === 24) state.underline = false;
    else if (c === 39) state.fg = null;
    else if (FG[c]) state.fg = FG[c];
  }
}

function classesFor(state: SgrState): string[] {
  const cls: string[] = [];
  if (state.bold) cls.push("console-render-ansi--bold");
  if (state.dim) cls.push("console-render-ansi--dim");
  if (state.italic) cls.push("console-render-ansi--italic");
  if (state.underline) cls.push("console-render-ansi--underline");
  if (state.fg) cls.push(state.fg);
  return cls;
}
