// query.ts - the pure #q= log query grammar, split out of filter.ts so it carries NO
// DOM dependency: `import type` is erased at build, so this module can be unit-tested in
// node (see query.test.ts) and reused by the console shell's SearchProvider without
// dragging in the render/search/dom modules. filter.ts re-exports parseQuery, so callers
// are unchanged.
//
// A pragmatic, log-shaped query (NOT the graph's node grammar): whitespace-split terms
// combined with AND, case-insensitive. A `key:value` term with a known key
// (target:/status:/step:) is a field filter; anything else is free text. Group-level
// terms (target/status) keep or drop a whole target group; line-level terms (step/bare
// text) narrow to matching lines. The parsed query serializes to #q= so a filtered view
// is shareable.

import type { ParsedQuery } from "./state";

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
