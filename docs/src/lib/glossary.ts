// glossary.ts - deep-link a magus-specific term to its glossary entry.
//
// The dashboard shows a lot of magus vocabulary (pool, slot, latency, remote
// cache, buzz, sandbox, ...). Each such term links to its definition so an operator
// can learn the model without leaving the surface. The link is SAME-ORIGIN and
// relative (../glossary/#<slug>, from a depth-1 console app) so ref-drawer.ts can
// intercept it and open the definition INLINE in the reference panel - you read the
// term without leaving the page. If the drawer is absent (or the fetch 404s, e.g. a
// daemon that does not serve /glossary/), the link just navigates normally.
//
// The slug is the lowercased-hyphenated term, matching the id the site generates
// for a "### Term" heading in docs/glossary.md. Link a term ONCE per tile heading
// (not on every occurrence) - see each tile's Card term option.

const GLOSSARY_BASE = "../glossary/#";

// slugify mirrors the site's heading-id rule: lowercase, non-alphanumerics to
// single hyphens, trimmed. "Output reference" -> "output-reference".
export function slugify(term: string): string {
  return term.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
}

export function glossaryUrl(term: string, slug?: string): string {
  return GLOSSARY_BASE + (slug ?? slugify(term));
}

// glossaryLink returns an <a> for the term's glossary entry. ref-drawer.ts intercepts .gloss-link
// clicks to open the definition inline in the reference panel (see its page-wide handler); with no
// drawer it navigates to the glossary page normally.
export function glossaryLink(term: string, opts: { label?: string; slug?: string } = {}): HTMLAnchorElement {
  const a = document.createElement("a");
  a.className = "gloss-link";
  a.href = glossaryUrl(term, opts.slug);
  a.textContent = opts.label ?? term;
  a.title = "Look up " + term + " in the reference panel";
  return a;
}
