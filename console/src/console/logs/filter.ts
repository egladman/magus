// filter.ts - the #q= log filter. A pragmatic, log-shaped query (NOT the graph's node grammar):
// whitespace-split terms combined with AND, case-insensitive. A `key:value` term with a known
// key (target:/status:/step:) is a field filter; anything else is free text. Group-level terms
// (target/status) keep or drop a whole target group; line-level terms (step/bare text) narrow to
// matching lines. The parsed query serializes to #q= so a filtered view is shareable/deep-linkable.

import type { ParsedQuery, Section, TargetSpan } from "./state";
import { state } from "./state";
import { statusToken, stripAnsi } from "../render/ansi";
import { el } from "./dom";
import { clearMarks } from "./search";
import { render } from "./render";
import { parseQuery } from "./query";

// parseQuery is the pure grammar; it lives in query.ts (DOM-free, unit-tested) and is
// re-exported here so existing importers keep resolving it from filter.ts.
export { parseQuery };

// setFilter records a new filter string and its parsed form; render()/renderWaterfall read the
// shared state.filterParsed, so every mode (static, live, demo) filters through the one path.
export function setFilter(q: string): void {
  state.filterQuery = (q || "").trim();
  state.filterParsed = parseQuery(state.filterQuery);
}

// matchGroup tests the group-level terms (target:/status:) against a target's label + status.
export function matchGroup(q: ParsedQuery, label: string, status: string): boolean {
  const lab = (label || "").toLowerCase();
  const st = (status || "").toLowerCase();
  for (const g of q.groups) {
    if (g.key === "target" && !lab.includes(g.value)) return false;
    if (g.key === "status" && !st.includes(g.value)) return false;
  }
  return true;
}

// matchAllTexts tests that a string contains EVERY free-text/step term (AND).
export function matchAllTexts(q: ParsedQuery, str: string): boolean {
  const s = (str || "").toLowerCase();
  for (const t of q.texts) if (!s.includes(t)) return false;
  return true;
}

// sectionMeta returns a target section's {label, status} for filtering: the structured meta
// attached by buildModelFromEvents when present, else derived from a heuristic section's title.
export function sectionMeta(sec: Section): { label: string; status: string } {
  if (sec.meta) return sec.meta;
  return { label: stripAnsi(sec.title || ""), status: statusToken(sec.title || "") || "running" };
}

// targetRelevant tests whether a waterfall target span matches the filter (used to decide which
// rows stay bright vs dim, and for the caption's match count).
export function targetRelevant(q: ParsedQuery, t: TargetSpan): boolean {
  if (q.empty) return true;
  if (!matchGroup(q, t.label, t.status || "running")) return false;
  return q.texts.length === 0 || matchAllTexts(q, t.label) || t.steps.some((s) => matchAllTexts(q, s.label));
}

// setQueryFragment mirrors the active filter to #q= via replaceState (no history spam),
// preserving every OTHER fragment part (#ref/#data/#live/#demo, the #L line token). Clearing
// the filter drops the q= part entirely.
export function setQueryFragment(query: string): void {
  const kept: string[] = [];
  for (const part of location.hash.replace(/^#/, "").split("&")) {
    if (!part) continue;
    const eq = part.indexOf("=");
    const key = eq < 0 ? part : part.slice(0, eq);
    if (key === "q") continue;
    kept.push(part);
  }
  if (query) kept.push("q=" + encodeURIComponent(query));
  const frag = kept.join("&");
  history.replaceState(null, "", location.pathname + location.search + (frag ? "#" + frag : ""));
}

// renderFilterChips echoes the parsed filter under the bar as PatternFly Labels - one per parsed
// term, joined by muted "AND" connectives - so you can SEE how the query was interpreted, the same
// "how your query parsed" cue the docs search page shows. The chips are PF Labels (pf-v6-c-label);
// only the row + the "AND" connective (.console-log-filter/.console-log-filter__op) are custom.
export function renderFilterChips(): void {
  const host = el("filter-chips");
  if (!host) return;
  host.textContent = "";
  if (state.filterParsed.empty) { host.hidden = true; return; }
  const parts: { field?: string; value?: string; text?: string }[] = [
    ...state.filterParsed.groups.map((g) => ({ field: g.key, value: g.value })),
    ...state.filterParsed.texts.map((t) => ({ text: t })),
  ];
  parts.forEach((p, i) => {
    if (i > 0) {
      const op = document.createElement("span");
      op.className = "console-log-filter__op";
      op.textContent = "AND";
      host.appendChild(op);
    }
    const label = document.createElement("span");
    label.className = "pf-v6-c-label pf-m-compact";
    const content = document.createElement("span");
    content.className = "pf-v6-c-label__content";
    if (p.field !== undefined) {
      const b = document.createElement("b");
      b.textContent = p.field;
      content.append(b, document.createTextNode(":" + p.value));
    } else {
      content.textContent = p.text ?? "";
    }
    label.appendChild(content);
    host.appendChild(label);
  });
  host.hidden = false;
}

// applyFilterFromInput is the debounced input handler: record the filter, sync #q=, echo the
// parsed chips, drop any stale search highlights (the DOM is rebuilt), and re-render.
export function applyFilterFromInput(value: string): void {
  setFilter(value);
  setQueryFragment(state.filterQuery);
  renderFilterChips();
  clearMarks();
  const cnt = el("search-count");
  if (cnt) cnt.textContent = "";
  if (state.model) render();
}
