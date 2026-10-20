{{if .Full}}# Navigating the magus docs{{else}}# magus docs{{end}}
{{if .Full}}
magus ships one official documentation site. It is a static site, so its
structure is fixed and machine-readable: this skill teaches HOW to move through
it; the pages themselves carry the WHAT. Use it for a magus-domain fact the
workspace graph cannot give: the docs are the source of truth for
magus's own behavior, so read them rather than guessing.
{{end}}
{{if .Full}}Send a docs lookup whose answer is all you need to the `magus-scout` agent, where your
harness installed one. Otherwise use a worker on your host's cheapest model.{{else}}One official docs site.
Use for magus-domain fact workspace graph cannot give: docs are source of truth for magus behavior.
Docs lookup whose answer is all you need -> `magus-scout` agent, where harness installed one; otherwise worker on host's cheapest model.{{end}}

{{if .Full}}Two places serve the same pages:
{{else}}Two places serve same pages:{{end}}
{{if .Full}}- In the magus repo (a `magusfile.buzz` at the root, a `docs/` tree): query the section
  (next), or read `docs/<name>.md` when you know the page. This is where
  the skill is dogfooded, so prefer it here.{{else}}- magus repo (`magusfile.buzz` at root, `docs/` tree): query section (next), or read `docs/<name>.md` when you know page.{{end}}
{{if .Full}}- Published: `https://eli.gladman.cc/magus/`. Every page is also raw Markdown at
  `<page-url>index.md`.{{else}}- Published: `https://eli.gladman.cc/magus/`; every page also raw Markdown at `<page-url>index.md`.{{end}}

{{if .Full}}## In a magus workspace, ask the graph for the passage{{else}}## In workspace: ask graph for passage{{end}}

{{if .Full}}Every Markdown heading is a `docsection` node, so "where is this explained" is a
query, not a scan:{{else}}Every Markdown heading is `docsection` node -> "where is this explained" is query, not scan:{{end}}

```sh
magus query kind=docsection "cache key"
```

{{if .Full}}Each result's id is `<path>#<anchor>`. Read that section, not the whole file.
`project=<p>` scopes it, and `{{cmd "explain"}} "docsection:<path>#<anchor>"` walks the
page's outline from there. The index route below is for the PUBLISHED site,
which has no graph.{{else}}Result id: `<path>#<anchor>`. Each result: read that section, not whole file.
Index route below: PUBLISHED site only, which has no graph.{{end}}

{{if .Full}}## Fast path: start from the index, do not guess URLs{{else}}## Fast path: index, do not guess URLs{{end}}

{{if .Full}}Two files at the docs root turn "find the page" into a lookup:
{{else}}Two files at docs root:{{end}}
{{if .Full}}- `llms.txt`: one titled link per page to its raw Markdown (`<url>index.md`), with a
  one-line description. Read it FIRST, then fetch the page's `index.md`.{{else}}- `llms.txt`: one titled link per page to raw Markdown (`<url>index.md`), one-line description. Read FIRST, then fetch page's `index.md`.{{end}}
{{if .Full}}- `search-index.json`: an array of `{url, title, text, tags, description}`, one
  record per page. Grep it for a keyword when you do not know the page name.{{else}}- `search-index.json`: array of `{url, title, text, tags, description}`, one record per page. Search when you do not know page name.{{end}}{{if .Full}}

WRONG: guess `https://.../go-spell` or grep the open web.
CORRECT: read `llms.txt` (or `docs/` locally), find the entry, fetch its Markdown.{{end}}

## URL scheme

{{if .Full}}Pages use extensionless directory URLs; append `index.md` for the raw source.

| You have                     | Page URL             | Raw Markdown                |
| ---------------------------- | -------------------- | --------------------------- |
| the `go` spell               | `/spells/go/`        | `/spells/go/index.md`       |
| the `{{cmd "run"}}` command      | `/manpage/{{skill "run"}}/`| `/manpage/{{skill "run"}}/index.md`|
| diagnostic {{mgs "MGS2001"}}           | `/codes/sandbox/{{mgs "MGS2001"}}/` | `.../{{mgs "MGS2001"}}/index.md` |{{else}}Extensionless directory URLs; append `index.md` for raw source.
- `go` spell: `/spells/go/` -> `/spells/go/index.md`
- `{{cmd "run"}}` command: `/manpage/magus-run/` -> `/manpage/magus-run/index.md`
- diagnostic {{mgs "MGS2001"}}: `/codes/sandbox/MGS2001/` -> `.../MGS2001/index.md`{{end}}

{{if .Full}}## Where things live (stable IDs route straight to a page){{else}}## Where things live{{end}}

{{if .Full}}Each stable ID maps to a fixed section, so you jump without searching:

| Looking for                        | Go to                        |
| ---------------------------------- | ---------------------------- |
| a CLI command / flag               | `/manpage/magus-<cmd>/`      |
| a spell and its ops                | `/spells/<name>/`            |
| a diagnostic `MGSxxxx`             | `/codes/` (grouped by family)|
| a stdlib module (fs, os, http, ...)| `/buzz/modules/<name>/`      |
| a core concept (targets, cache, charms, sandbox, affected, ...) | `/<concept>/` |
| install / download                 | `/download/` and its children|
| the whole map                      | `/documentation/`            |{{else}}Each stable ID maps to fixed section:
- CLI command / flag: `/manpage/magus-<cmd>/`
- spell and ops: `/spells/<name>/`
- diagnostic `MGSxxxx`: `/codes/` (grouped by family)
- stdlib module (fs, os, http, ...): `/buzz/modules/<name>/`
- core concept (targets, cache, charms, sandbox, affected, ...): `/<concept>/`
- install / download: `/download/` and children
- whole map: `/documentation/`{{end}}

{{if .Full}}## Traversing within the docs{{else}}## Traversing{{end}}
{{if .Full}}
Every page has three axes, so from one page you can reach its whole area:
{{end}}
{{if .Full}}- Breadcrumb (up): the trail back to `/documentation/`.{{else}}Every page, three axes:
- Breadcrumb (up): trail back to `/documentation/`.{{end}}
{{if .Full}}- "In this section" (siblings + children): the other pages under this section's
  landing. A `page_type: overview` page IS a section landing.{{else}}- "In this section" (siblings, children): other pages under section landing.{{end}}
{{if .Full}}- Prev / next (pager): the adjacent pages in the section.{{else}}- Prev / next (pager): adjacent pages in section.{{end}}

{{if .Full}}So: land via `llms.txt`, read the page, then use "In this section" to sweep its
siblings; do not re-search for each one.{{else}}Land via `llms.txt`, then sweep siblings via "In this section".{{end}}

{{if .Full}}## The published site follows main, not your build{{else}}## Published site follows main, not your build{{end}}

{{if .Full}}The site and its source links (`/blob/main/`) are rendered from main. The binary
you run may be older: a flag, an op or a diagnostic can differ between a page
and your build. When they disagree, trust what the binary prints, such as
`magus <command> -h`.

To read a linked file as your build has it, replace `blob/main` with
`blob/<commit>`. Take the commit from `{{cmd "version"}} -o json`. Find the symbol by
name: line numbers move between versions, so the link's anchor points at main's line.

If the docs and the behavior still disagree after that, and you have a
reproduction, the {{skill "upstream-source"}} skill traces the code at your build.{{else}}Site and source links (`/blob/main/`) render from main; binary may be older.
When they disagree -> trust binary output, like `magus <command> -h`.
Linked file as your build has it: replace `blob/main` with `blob/<commit>`; commit from `{{cmd "version"}} -o json`.
Find symbol by name; line numbers move.
If docs and behavior still disagree after that, and you have reproduction -> {{skill "upstream-source"}} skill traces code at your build.{{end}}

{{if .Full}}## In the magus repo{{else}}## In magus repo{{end}}

{{if .Full}}`docs/` Markdown is the source of truth; `docs/gen/` is generated (never edit it;
change the source and regenerate). MAGUS.md is a routing index for HUMAN readers;
do not answer from it: it is true only as of the
last regeneration, and every fact in it has a live command. The knowledge graph
carries every page as a `doc` node, so `{{cmd "query"}} "kind=doc"` (see the
{{skill "query"}} skill) lists them from the graph.{{else}}`docs/` Markdown: source of truth.
`docs/gen/`: generated; never edit; change source, regenerate.
MAGUS.md: routing index for HUMAN readers; do not answer from it: true only as of last regeneration.
`{{cmd "query"}} "kind=doc"` lists every page from graph.{{end}}
