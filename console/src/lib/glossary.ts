// glossary.ts - deep-link a magus-specific term to its glossary entry.
//
// The dashboard shows a lot of magus vocabulary (pool, slot, latency, remote
// cache, buzz, sandbox, ...). Each such term links to its definition so an operator
// can learn the model without leaving the surface. The link is SAME-ORIGIN and
// relative (../glossary/#<slug>, from a depth-1 console app).
//
// It NAVIGATES. Nothing intercepts it: this file, card.ts and dashboard.css each used to
// claim ref-drawer.ts opened the term inline in the reference panel, but ref-drawer.ts has
// no such handler (it wires its own toggles and [data-q]/[data-view]/[data-lens] inside its
// own body, nothing glossary-shaped), and the comments named a `.gloss-link` class the
// markup does not use either. So a click leaves the SPA, and on a console served without the
// docs tree beside it the destination 404s. Opening it inline is a real improvement and is
// still unbuilt - if you build it, intercept `.console-render-glosslink` and fix this comment
// in the same change rather than describing the intent again.
//
// The slug is the lowercased-hyphenated term, matching the id the site generates
// for a "### Term" heading in docs/glossary.md. Link a term ONCE per tile heading
// (not on every occurrence) - see each tile's Card term option.

const GLOSSARY_BASE = "../glossary/#";

// slugify mirrors the site's heading-id rule: lowercase, non-alphanumerics to
// single hyphens, trimmed. "Output reference" -> "output-reference".
export function slugify(term: string): string {
  return term
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

export function glossaryUrl(term: string, slug?: string): string {
  return GLOSSARY_BASE + (slug ?? slugify(term));
}

// glossaryLink returns an <a> for the term's glossary entry. It navigates to the glossary
// page; see the note at the top of this file about the inline-panel behavior that is
// described in several places but does not exist.
export function glossaryLink(
  term: string,
  opts: { label?: string; slug?: string } = {},
): HTMLAnchorElement {
  const a = document.createElement("a");
  a.className = "console-render-glosslink";
  a.href = glossaryUrl(term, opts.slug);
  a.textContent = opts.label ?? term;
  a.title = "Look up " + term + " in the glossary";
  return a;
}
