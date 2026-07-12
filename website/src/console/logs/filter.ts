// filter.ts - the #q= log filter. A pragmatic, log-shaped query (NOT the graph's node grammar):
// whitespace-split terms combined with AND, case-insensitive. A `key:value` term with a known
// key (target:/status:/step:) is a field filter; anything else is free text. Group-level terms
// (target/status) keep or drop a whole target group; line-level terms (step/bare text) narrow to
// matching lines. The parsed query serializes to #q= so a filtered view is shareable/deep-linkable.

import type { ParsedQuery, Section, TargetSpan } from "./state";
import { state } from "./state";
import { statusToken, stripAnsi } from "./ansi";
import { el } from "./dom";
import { clearMarks } from "./search";
import { render } from "./render";

export function parseQuery(q: string): ParsedQuery {
  const groups: ParsedQuery["groups"] = [];
  const texts: string[] = [];
  for (const tok of (q || "").trim().split(/\s+/)) {
    if (!tok) continue;
    const ci = tok.indexOf(":");
    if (ci > 0) {
      const key = tok.slice(0, ci).toLowerCase();
      const val = tok.slice(ci + 1).toLowerCase();
      if (val && (key === "target" || key === "status")) { groups.push({ key, value: val }); continue; }
      if (val && key === "step") { texts.push(val); continue; }
      // Unknown key (or empty value): fall through and treat the whole token as free text.
    }
    texts.push(tok.toLowerCase());
  }
  return { groups, texts, empty: groups.length === 0 && texts.length === 0 };
}

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

// renderFilterChips echoes the parsed filter under the bar as chips - fields as bordered
// pills, free text as plain chips, joined by "AND" connectives - so you can SEE how the query
// was interpreted, the same "how your query parsed" cue the docs search page shows. Reuses
// site.css's .search-chips/.qchip/.qop classes verbatim.
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
      op.className = "qop";
      op.textContent = "AND";
      host.appendChild(op);
    }
    const chip = document.createElement("span");
    if (p.field !== undefined) {
      chip.className = "qchip qchip-field";
      const b = document.createElement("b");
      b.textContent = p.field;
      chip.appendChild(b);
      chip.appendChild(document.createTextNode(":" + p.value));
    } else {
      chip.className = "qchip";
      chip.textContent = p.text ?? "";
    }
    host.appendChild(chip);
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
