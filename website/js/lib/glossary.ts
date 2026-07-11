// glossary.ts - deep-link a magus-specific term to its glossary entry.
//
// The dashboard shows a lot of magus vocabulary (pool, slot, latency, remote
// cache, buzz, sandbox, ...). Each such term links out to its definition so an
// operator can learn the model without leaving the surface. The link is ABSOLUTE
// (https://eli.gladman.cc/magus/glossary/#<slug>) and opens in a NEW tab: the
// daemon does not serve /glossary/, so a relative link would 404 in live mode, and
// a new tab keeps the dashboard connection alive.
//
// The slug is the lowercased-hyphenated term, matching the id the site generates
// for a "### Term" heading in docs/glossary.md. Link a term ONCE per tile heading
// (not on every occurrence) - see each tile's Card term option.

const GLOSSARY_BASE = "https://eli.gladman.cc/magus/glossary/#";

// slugify mirrors the site's heading-id rule: lowercase, non-alphanumerics to
// single hyphens, trimmed. "Output reference" -> "output-reference".
export function slugify(term: string): string {
  return term.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
}

export function glossaryUrl(term: string, slug?: string): string {
  return GLOSSARY_BASE + (slug ?? slugify(term));
}

// glossaryLink returns an <a> that opens the term's glossary entry in a new tab.
// The text is the visible label (defaults to the term); the trailing new-tab arrow
// is added by the .gloss-link CSS (reusing the site's external-link arrow), so no
// glyph is baked into the string and the source stays ASCII.
export function glossaryLink(term: string, opts: { label?: string; slug?: string } = {}): HTMLAnchorElement {
  const a = document.createElement("a");
  a.className = "gloss-link";
  a.href = glossaryUrl(term, opts.slug);
  a.target = "_blank";
  a.rel = "noopener";
  a.textContent = opts.label ?? term;
  a.title = "Open the " + term + " glossary entry in a new tab";
  return a;
}
