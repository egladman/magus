# Navigating the magus docs

magus ships one official documentation corpus.<!-- why --> It is a static site, so its
structure is fixed and machine-readable: this skill teaches HOW to move through
it; the pages themselves carry the WHAT.<!-- /why --> Reach for it when a magus-domain fact
is not derivable from the workspace graph<!-- why --> - the docs are the source of truth for
magus's own behavior, so read them rather than guessing<!-- /why --><!-- terse --> - they are the source of
truth for magus's own behavior<!-- /terse -->.

Two places serve the same pages:

- In the magus repo (a `magusfile.buzz` at the root, a `docs/` tree): read `docs/<name>.md`
  directly.<!-- why --> This is where the skill is dogfooded, so prefer it here.<!-- /why -->
- Published: the deployed site at `https://eli.gladman.cc/magus/`. Every page is
  also emitted as raw Markdown at `<page-url>index.md` for clean fetching.

## FAST PATH: start from the index, do not guess URLs

Two files at the docs root turn "find the right page" into a lookup, not a guess:

- `llms.txt` - one titled link per page, each pointing at that page's raw
  Markdown (`<url>index.md`), with a one-line description. Read this FIRST to
  locate a page, then fetch its `index.md`.
- `search-index.json` - a flat array of `{url, title, text, tags, description}`,
  one record per page.<!-- why --> Grep it for a keyword when you do not know the page name.<!-- /why --><!-- terse --> Search it when you do not know the page name.<!-- /terse -->

<!-- why -->WRONG: guess `https://.../go-spell` or grep the open web.
CORRECT: read `llms.txt` (or `docs/` locally), find the entry, fetch its Markdown.<!-- /why -->

## URL scheme

Pages use extensionless directory URLs; append `index.md` for the raw source.

| You have                     | Page URL             | Raw Markdown                |
| ---------------------------- | -------------------- | --------------------------- |
| the `go` spell               | `/spells/go/`        | `/spells/go/index.md`       |
| the `magus run` command      | `/manpage/magus-run/`| `/manpage/magus-run/index.md`|
| diagnostic MGS2001           | `/codes/sandbox/MGS2001/` | `.../MGS2001/index.md` |

## Where things live (stable IDs route straight to a page)

magus mints stable IDs; each maps to a fixed section<!-- why -->, so you jump without searching<!-- /why -->:

| Looking for                        | Go to                        |
| ---------------------------------- | ---------------------------- |
| a CLI command / flag               | `/manpage/magus-<cmd>/`      |
| a spell and its ops                | `/spells/<name>/`            |
| a diagnostic `MGSxxxx`             | `/codes/` (grouped by family)|
| a stdlib module (fs, os, http, ...)| `/buzz/modules/<name>/`      |
| a core concept (targets, cache, charms, sandbox, affected, ...) | `/<concept>/` |
| install / download                 | `/download/` and its children|
| the whole map                      | `/documentation/`            |

## Traversing within the docs

Every page gives you three axes<!-- why -->, so from one page you can reach its whole area<!-- /why -->:

- Breadcrumb (up): the trail back to `/documentation/`.
- "In this section" (siblings + children): the other pages under this page's
  section landing.<!-- why --> A `page_type: overview` page IS a section landing.<!-- /why -->
- Prev / next (pager): the adjacent pages in the same section.

<!-- why -->So: land via `llms.txt`, read the page, then use "In this section" to sweep its
siblings - do not re-search for each one.<!-- /why --><!-- terse -->Land via `llms.txt`, then sweep siblings via "In this section".<!-- /terse -->

## In the magus repo

The `docs/` Markdown is the source of truth; `docs/gen/` is generated output
(never edit it - change the source and regenerate). MAGUS.md is a routing index
generated for HUMAN readers, so do not answer from it<!-- why -->: it is true only as of the
last regeneration, and every fact in it has a live command<!-- /why -->.<!-- why --> The knowledge graph
carries every page as a `doc` node, so `magus query "kind:doc"` (see the
magus-query skill) lists them from the graph.<!-- /why --><!-- terse --> `magus query "kind:doc"` lists every
page from the graph.<!-- /terse -->
