import { must } from "../../lib/guards";
// render.ts - the pretty/raw log views and the DOM plumbing around them. render() is the one
// entry every mode calls to repaint the body: it dispatches to the waterfall in timeline mode,
// to a flat line-numbered dump in raw mode, and otherwise to the stylized structural view
// (foldable target sections with status accents, per-section copy/cmd actions, and the active
// #q= filter narrowing lines and groups). Also owns the ANSI-aware DOM fill, the status badge,
// the GitHub-style #L line-range highlight, and the Timeline toolbar control's enable/label sync.

import { state } from "./state";
import { bodyEl, copyToClipboard, el, setToggleGroup, setToggleGroupDisabled } from "./dom";
import { stripAnsi } from "../../render/ansi";
import {
  countLabel,
  createSection,
  renderContent,
  renderLine as renderSectionLine,
  sectionAccent,
  sectionAction,
  setSectionOpen,
} from "../../render/sections";
import {
  applyFilterFromInput,
  clearFilterResults,
  matchAllTexts,
  matchGroup,
  sectionMeta,
  setFilterResults,
  syncFilterBox,
} from "./filter";
import { renderWaterfall, timelineAvailable, updateFocusUI } from "./waterfall";

export function render(): void {
  bodyEl.textContent = "";
  bodyEl.toggleAttribute("data-raw", !state.pretty && !state.timeline);
  bodyEl.toggleAttribute("data-waterfall", state.timeline);
  // The views below that filter report their own count; the others show none.
  clearFilterResults();
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
    const flat = state.rawLines || must(state.model).sections.flatMap((s) => s.lines);
    let n = 0;
    for (const raw of flat) bodyEl.appendChild(renderSectionLine(raw, ++n, onLineNumberClick));
    applyLineHighlight();
    return;
  }

  const model = must(state.model);
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
        if (keep) {
          bodyEl.appendChild(renderSectionLine(raw, n, onLineNumberClick));
          shown++;
        }
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

    // Accent the section by outcome (a coloured left rule) so pass/fail/warn read at a glance, not
    // just from the text. sectionAccent, not a second copy of the rule: this file had its own
    // inline version and the two had already drifted apart on what counts as cached.
    const titleText = sec.title;
    const status = sectionAccent(titleText);

    // The head IS the header line and doubles as the fold toggle. The line counts as row
    // `++lineNo` so search and line numbers stay in step; not repeated in the body.
    const headNo = ++lineNo;
    const gutter = document.createElement("span");
    gutter.className = "console-render-line__gutter";
    gutter.textContent = String(headNo);
    const bodyLines = sec.lines.slice(1);

    const actions: HTMLElement[] = [
      sectionAction("Copy", "Copy this section's text", (btn) =>
        copyToClipboard(sec.lines.map(stripAnsi).join("\n"), btn, "this section"),
      ),
    ];
    // A `magus query` one-liner scoped to this section's line range, so the exact lines can be
    // fetched in a terminal. Only when seeded by a ref.
    if (state.currentRef) {
      const start = headNo;
      const end = headNo + bodyLines.length;
      actions.push(
        sectionAction(
          "Copy command",
          "Copy a `magus query` command that prints these lines",
          (btn) =>
            copyToClipboard(
              bodyLines.length > 0
                ? "magus query " + state.currentRef + " | sed -n '" + start + "," + end + "p'"
                : "magus query " + state.currentRef,
              btn,
              "the command",
            ),
        ),
      );
    }

    // A cached target contributed nothing new this run, so it folds away by default - the fresh
    // work (and any failure) is what a reader came for.
    const { secEl, lines } = createSection({
      status,
      collapsed: status === "cached",
      gutter,
      countText: countLabel(bodyLines.length, ["line", "lines"]),
      fillTitle: (title) => renderContent(title, titleText),
      actions,
    });
    for (const raw of bodyLines) {
      const n = ++lineNo;
      if (!filtering || showAllBody || matchAllTexts(q, stripAnsi(raw))) {
        lines.appendChild(renderSectionLine(raw, n, onLineNumberClick));
        shown++;
      }
    }

    bodyEl.appendChild(secEl);
    shown++; // the visible head row
  }
  if (filtering) {
    setFilterResults(shown, lineNo, ["line", "lines"]);
    if (shown === 0) bodyEl.appendChild(noMatchNote());
  }
  applyLineHighlight();
}

// noMatchNote is the pretty view's answer to a filter that left nothing, with the way back beside it.
function noMatchNote(): HTMLElement {
  const note = document.createElement("div");
  note.className = "console-log-filter__empty";
  const text = document.createElement("p");
  text.textContent = "No lines match the filter.";
  const clear = document.createElement("button");
  clear.type = "button";
  clear.className = "pf-v6-c-button pf-m-secondary";
  clear.textContent = "Clear filter";
  clear.addEventListener("click", () => {
    syncFilterBox("");
    applyFilterFromInput("");
  });
  note.append(text, clear);
  return note;
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

// applyLineHighlight (re)paints the highlighted rows (data-highlight) from the current fragment.
// Called at the end of every render() so it survives view toggles, folds, and live re-renders.
export function applyLineHighlight(): void {
  for (const r of bodyEl.querySelectorAll("[data-highlight]")) r.removeAttribute("data-highlight");
  const range = lineRangeFromHash();
  if (!range) return;
  let first: HTMLElement | null = null;
  for (const ln of bodyEl.querySelectorAll(".console-render-line__gutter")) {
    const n = parseInt(must(ln.textContent), 10);
    if (!(n >= range.start && n <= range.end)) continue;
    // A body line, or the section head for a head row (its gutter sits inside the toggle).
    const row = (ln.closest(".console-render-section__head") ?? ln.parentElement) as HTMLElement;
    row.setAttribute("data-highlight", "");
    // Expand a collapsed section so a highlighted body line is actually visible.
    const sec = row.closest(".console-render-section");
    if (sec?.hasAttribute("data-collapsed")) setSectionOpen(sec, true);
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

// updateTimelineControl enables/disables the Log|Timeline switch by whether the loaded log carries
// plottable timing, forces the mode off when it does not (a new text log), and syncs the switch
// selection + the sibling controls that do not apply in the waterfall view.
export function updateTimelineControl(): void {
  const ok = timelineAvailable();
  setToggleGroupDisabled("timeline-mode", !ok);
  // Fall back to the log view when the loaded log has no timing (a text/pasted log). During
  // the #demo reveal the first frame may briefly precede any target span, so keep the mode on.
  if (!ok && state.timeline && !state.demoActive) state.timeline = false;
  setToggleGroup("timeline-mode", state.timeline);
  // The pretty/raw switch is meaningless in the waterfall; disable it while timeline is on.
  setToggleGroupDisabled("view-mode", state.timeline);
  // The time range only applies to the waterfall, so the whole group hides outside timeline mode
  // (rather than dangling as a disabled empty select in the log view).
  const timerange = el("log-timerange");
  if (timerange) timerange.hidden = !state.timeline;
  // renderWaterfall refreshes the readout when it draws, but when NOT in timeline mode nothing
  // else does, so reset the picker here.
  if (!state.timeline) updateFocusUI(null);
}
