# Navigating the magus docs

magus ships one official documentation site.{{if .Full}} It is a static site, so its
structure is fixed and machine-readable: this skill teaches HOW to move through
it; the pages themselves carry the WHAT.{{end}} Use it for a magus-domain fact the
workspace graph cannot give{{if .Full}}: the docs are the source of truth for
magus's own behavior, so read them rather than guessing{{else}}: the docs are the source of
truth for magus's behavior{{end}}.

A docs lookup you need only the answer to goes to the `magus-scout` agent where your
harness installed it, or to a worker on your host's cheapest model.

Two places serve the same pages:

- In the magus repo (a `magusfile.buzz` at the root, a `docs/` tree): query the section
  (next), or read `docs/<name>.md` when you know the page.{{if .Full}} This is where
  the skill is dogfooded, so prefer it here.{{end}}
- Published: `https://eli.gladman.cc/magus/`. Every page is also raw Markdown at
  `<page-url>index.md`.

## In a magus workspace, ask the graph for the passage

Every Markdown heading is a `docsection` node, so "where is this explained" is a
query, not a scan:

```sh
magus query kind=docsection "cache key"
```

Each result's id is `<path>#<anchor>`. Read that section, not the whole file.{{if .Full}}
`project=<p>` scopes it, and `magus explain "docsection:<path>#<anchor>"` walks the
page's outline from there.{{end}} The index route below is for the PUBLISHED site,
which has no graph.

## Fast path: start from the index, do not guess URLs

Two files at the docs root turn "find the page" into a lookup:

- `llms.txt`: one titled link per page to its raw Markdown (`<url>index.md`), with a
  one-line description. Read it FIRST, then fetch the page's `index.md`.
- `search-index.json`: an array of `{url, title, text, tags, description}`, one
  record per page.{{if .Full}} Grep it for a keyword when you do not know the page name.{{else}} Search it when you do not know the page name.{{end}}

{{if .Full}}WRONG: guess `https://.../go-spell` or grep the open web.
CORRECT: read `llms.txt` (or `docs/` locally), find the entry, fetch its Markdown.{{end}}

## URL scheme

Pages use extensionless directory URLs; append `index.md` for the raw source.

| You have                     | Page URL             | Raw Markdown                |
| ---------------------------- | -------------------- | --------------------------- |
| the `go` spell               | `/spells/go/`        | `/spells/go/index.md`       |
| the `magus run` command      | `/manpage/{{skill "run"}}/`| `/manpage/{{skill "run"}}/index.md`|
| diagnostic {{mgs "MGS2001"}}           | `/codes/sandbox/{{mgs "MGS2001"}}/` | `.../{{mgs "MGS2001"}}/index.md` |

## Where things live (stable IDs route straight to a page)

Each stable ID maps to a fixed section{{if .Full}}, so you jump without searching{{end}}:

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

Every page has three axes{{if .Full}}, so from one page you can reach its whole area{{end}}:

- Breadcrumb (up): the trail back to `/documentation/`.
- "In this section" (siblings + children): the other pages under this section's
  landing.{{if .Full}} A `page_type: overview` page IS a section landing.{{end}}
- Prev / next (pager): the adjacent pages in the section.

{{if .Full}}So: land via `llms.txt`, read the page, then use "In this section" to sweep its
siblings; do not re-search for each one.{{else}}Land via `llms.txt`, then sweep siblings via "In this section".{{end}}

## The published site follows main, not your build

The site and its source links (`/blob/main/`) are rendered from main. The binary
you run may be older{{if .Full}}: a flag, an op or a diagnostic can differ between a page
and your build{{end}}. When they disagree, trust what the binary prints, such as
`magus <command> -h`.

To read a linked file as your build has it, replace `blob/main` with
`blob/<commit>`. Take the commit from `{{cmd "version"}} -o json`. Find the symbol by
name{{if .Full}}: line numbers move between versions, so the link's anchor points at main's line{{else}}, because line numbers move{{end}}.

If the docs and the behavior still disagree after that, and you have a
reproduction, the {{skill "upstream-source"}} skill traces the code at your build.

## In the magus repo

`docs/` Markdown is the source of truth; `docs/gen/` is generated (never edit it;
change the source and regenerate). MAGUS.md is a routing index for HUMAN readers;
do not answer from it{{if .Full}}: it is true only as of the
last regeneration, and every fact in it has a live command{{else}}: it is true only as of its last
regeneration{{end}}.{{if .Full}} The knowledge graph
carries every page as a `doc` node, so `magus query "kind=doc"` (see the
{{skill "query"}} skill) lists them from the graph.{{else}} `magus query "kind=doc"` lists every
page from the graph.{{end}}
