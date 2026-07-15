// render.ts - the pretty/raw log views and the DOM plumbing around them. render() is the one
// entry every mode calls to repaint the body: it dispatches to the waterfall in timeline mode,
// to a flat line-numbered dump in raw mode, and otherwise to the stylized structural view
// (foldable target sections with status accents, per-section copy/cmd actions, and the active
// #q= filter narrowing lines and groups). Also owns the ANSI-aware DOM fill, the status badge,
// the GitHub-style #L line-range highlight, and the Timeline toolbar control's enable/label sync.

import { state } from "./state";
import { bodyEl, copyToClipboard, el } from "./dom";
import { statusToken, stripAnsi } from "../render/ansi";
import { renderContent, renderLine as renderSectionLine, toggleSection } from "../render/sections";
import { matchAllTexts, matchGroup, sectionMeta } from "./filter";
import { renderWaterfall, timelineAvailable, updateFocusUI } from "./waterfall";

export function render(): void {
  bodyEl.textContent = "";
  bodyEl.classList.toggle("raw", !state.pretty && !state.timeline);
  bodyEl.classList.toggle("wf-mode", state.timeline);
  // Timeline view: a trace waterfall built from the events' timing, not the log text.
  if (state.timeline) {
    renderWaterfall();
    return;
  }
  // Raw view: the exact captured text, flat - line numbers + ANSI color, no folds,
  // no badges, no structural chrome. The pretty view (default) styles it below.
  if (!state.pretty) {
    // RAW: in Journal mode, the exact reconstructed output (what `magus query <ref>` prints);
    // in heuristic mode, the parsed section lines. Flat, line-numbered, no folds/badges.
    const flat = state.rawLines || state.model!.sections.flatMap((s) => s.lines);
    let n = 0;
    for (const raw of flat) bodyEl.appendChild(renderSectionLine(raw, ++n, onLineNumberClick));
    applyLineHighlight();
    return;
  }

  const model = state.model!;
  // The active filter narrows the pretty view: non-matching lines and whole target groups are
  // hidden. lineNo still advances over hidden rows so line numbers (and #L links) stay stable.
  const q = state.filterParsed;
  const filtering = !q.empty;
  let shown = 0;

  let lineNo = 0;
  for (const sec of model.sections) {
    if (sec.title === null) {
      // Preamble / flat log: no target/status, so any group-level term excludes it; with only
      // text terms, keep matching lines; unfiltered, keep all.
      for (const raw of sec.lines) {
        const n = ++lineNo;
        const keep = !filtering || (q.groups.length === 0 && matchAllTexts(q, stripAnsi(raw)));
        if (keep) { bodyEl.appendChild(renderSectionLine(raw, n, onLineNumberClick)); shown++; }
      }
      continue;
    }

    // Filter this target group: drop it entirely if a group-level term excludes it, or if text
    // terms match neither its title nor any body line. showAllBody is true when the group is
    // kept for its title/status (show the whole group); otherwise only matching lines show.
    const bodyLinesAll = sec.lines.slice(1);
    let showAllBody = true;
    if (filtering) {
      const meta = sectionMeta(sec);
      const noText = q.texts.length === 0;
      const titleHit = noText || matchAllTexts(q, stripAnsi(sec.title));
      const anyBody = noText || bodyLinesAll.some((l) => matchAllTexts(q, stripAnsi(l)));
      if (!(matchGroup(q, meta.label, meta.status) && (titleHit || anyBody))) {
        lineNo += sec.lines.length; // advance numbering past the hidden head + body
        continue;
      }
      showAllBody = titleHit;
    }

    const secEl = document.createElement("div");
    secEl.className = "log-section";

    // Accent the section by outcome (a colored left rule) so pass/fail/warn read at a
    // glance, not just from the text. Cached hits are muted (low signal) and fold by default -
    // recognized from a "[cached]" head (structured) or a "(cached)" note (heuristic text).
    const st = statusToken(sec.title);
    const cached = st === "cached" || /\(cached/i.test(stripAnsi(sec.title));
    const status = cached ? "cached" : st;
    if (status) secEl.classList.add("status-" + status);

    const head = document.createElement("button");
    head.type = "button";
    head.className = "log-section-head";

    // The head IS the header line and doubles as the fold toggle. The line counts as
    // row `++lineNo` so search and line numbers stay in step; not repeated in the body.
    const headNo = ++lineNo;
    const ln = document.createElement("span");
    ln.className = "ln";
    ln.textContent = String(headNo);
    const twist = document.createElement("span");
    twist.className = "twist"; // caret drawn in CSS (.twist); no glyph, so the source stays ASCII
    twist.setAttribute("aria-hidden", "true");
    const title = document.createElement("span");
    title.className = "sec-title lc";
    renderContent(title, sec.title);
    const bodyLines = sec.lines.slice(1);

    // A cached target contributed nothing new this run, so fold it away by default -
    // the fresh work (and any failure) is what a reader came for.
    if (cached) secEl.classList.add("collapsed");
    head.setAttribute("aria-expanded", cached ? "false" : "true");
    const count = document.createElement("span");
    count.className = "sec-count";
    count.textContent = bodyLines.length > 0 ? bodyLines.length + (bodyLines.length === 1 ? " line" : " lines") : "";

    const actions = document.createElement("span");
    actions.className = "sec-actions";
    const copy = document.createElement("button");
    copy.type = "button";
    copy.className = "sec-btn outline";
    copy.textContent = "copy";
    copy.title = "Copy this section's text";
    copy.addEventListener("click", (ev) => {
      ev.stopPropagation();
      copyToClipboard(sec.lines.map(stripAnsi).join("\n"), copy);
    });
    actions.append(copy);
    // "cmd": a `magus query` one-liner scoped to this section's line range, so it can
    // be handed to an agent to fetch exactly these lines. Only when seeded by a ref.
    if (state.currentRef) {
      const cmd = document.createElement("button");
      cmd.type = "button";
      cmd.className = "sec-btn outline";
      cmd.textContent = "cmd";
      cmd.title = "Copy a `magus query` command for these lines";
      const start = headNo;
      const end = headNo + bodyLines.length;
      cmd.addEventListener("click", (ev) => {
        ev.stopPropagation();
        const command =
          bodyLines.length > 0
            ? "magus query " + state.currentRef + " | sed -n '" + start + "," + end + "p'"
            : "magus query " + state.currentRef;
        copyToClipboard(command, cmd);
      });
      actions.append(cmd);
    }

    head.append(ln, twist, title, count, actions);
    head.addEventListener("click", () => toggleSection(secEl, head));

    const linesWrap = document.createElement("div");
    linesWrap.className = "log-lines";
    for (const raw of bodyLines) {
      const n = ++lineNo;
      if (!filtering || showAllBody || matchAllTexts(q, stripAnsi(raw))) {
        linesWrap.appendChild(renderSectionLine(raw, n, onLineNumberClick));
        shown++;
      }
    }

    secEl.append(head, linesWrap);
    bodyEl.appendChild(secEl);
    shown++; // the visible head row
  }
  if (filtering && shown === 0) {
    const note = document.createElement("p");
    note.className = "filter-empty";
    note.textContent = "No lines match the filter.";
    bodyEl.appendChild(note);
  }
  applyLineHighlight();
}

// --- Line-range highlight (#L10-L20, GitHub-style) ----------------------------
// A fragment token like `L10-L20` (or a single `L10`) highlights those line rows and scrolls
// the first into view. It coexists with data=/ref= (which viewerParams parses); this token
// has no "=" so it is read separately here.

function lineRangeFromHash(): { start: number; end: number } | null {
  for (const part of location.hash.replace(/^#/, "").split("&")) {
    const m = /^L(\d+)(?:-L?(\d+))?$/.exec(part);
    if (!m) continue;
    const a = parseInt(m[1], 10);
    const b = m[2] ? parseInt(m[2], 10) : a;
    return { start: Math.min(a, b), end: Math.max(a, b) };
  }
  return null;
}

// applyLineHighlight (re)paints the .line-highlight rows from the current fragment. Called at
// the end of every render() so it survives view toggles, folds, and live re-renders.
export function applyLineHighlight(): void {
  for (const r of bodyEl.querySelectorAll(".line-highlight")) r.classList.remove("line-highlight");
  const range = lineRangeFromHash();
  if (!range) return;
  let first: HTMLElement | null = null;
  for (const ln of bodyEl.querySelectorAll(".ln")) {
    const n = parseInt(ln.textContent!, 10);
    if (!(n >= range.start && n <= range.end)) continue;
    const row = ln.parentElement as HTMLElement; // .log-line, or the .log-section-head button for a head row
    row.classList.add("line-highlight");
    // Expand a collapsed section so a highlighted body line is actually visible.
    const sec = row.closest && row.closest(".log-section");
    if (sec && sec.classList.contains("collapsed")) {
      sec.classList.remove("collapsed");
      const head = sec.querySelector(".log-section-head");
      if (head) head.setAttribute("aria-expanded", "true");
    }
    if (first === null) first = row;
  }
  if (first) first.scrollIntoView({ block: "center" });
}

// onLineNumberClick sets the fragment to a GitHub-style L token and re-highlights: a plain
// click anchors a single line (L<n>); shift-click extends from the anchor to a range (L<a>-L<b>).
function onLineNumberClick(n: number, ev: MouseEvent): void {
  ev.stopPropagation();
  let start = n;
  let end = n;
  if (ev.shiftKey && state.highlightStart !== null) {
    start = Math.min(state.highlightStart, n);
    end = Math.max(state.highlightStart, n);
  } else {
    state.highlightStart = n;
  }
  setLineFragment(start, end);
  applyLineHighlight();
}

// setLineFragment replaces the L token in the fragment (preserving ref=/data= and other keys)
// via replaceState, so the highlight is shareable/bookmarkable without adding a history entry.
function setLineFragment(start: number, end: number): void {
  const kept: string[] = [];
  for (const part of location.hash.replace(/^#/, "").split("&")) {
    if (!part || /^L\d+/.test(part)) continue;
    kept.push(part);
  }
  kept.push(start === end ? "L" + start : "L" + start + "-L" + end);
  history.replaceState(null, "", location.pathname + location.search + "#" + kept.join("&"));
}

// updateTimelineControl shows/hides the Timeline button by whether the loaded log carries
// plottable timing, forces the mode off when it does not (a new text log), and syncs the
// button label + the sibling controls that do not apply in the waterfall view.
export function updateTimelineControl(): void {
  const tlBtn = el("timeline-btn");
  const ok = timelineAvailable();
  if (tlBtn) (tlBtn as HTMLButtonElement).disabled = !ok;
  // Fall back to the log view when the loaded log has no timing (a text/pasted log). During
  // the #demo reveal the first frame may briefly precede any target span, so keep the mode on.
  if (!ok && state.timeline && !state.demoActive) state.timeline = false;
  // aria-pressed drives the segmented slider's active side (true => Timeline); no text relabel.
  if (tlBtn) tlBtn.setAttribute("aria-pressed", state.timeline ? "true" : "false");
  // The pretty/raw toggle is meaningless in the waterfall; hide it while timeline is on.
  const viewBtn = el("view-toggle");
  if (viewBtn) (viewBtn as HTMLButtonElement).disabled = state.timeline;
  // The time range only applies to the waterfall; renderWaterfall refreshes the readout when
  // it draws, but when NOT in timeline mode nothing else does, so disable the picker here.
  if (!state.timeline) updateFocusUI(null);
}
