---
title: magus-upstream-docs
generated_from: internal/agent/skills/magus-upstream-docs/SKILL.md
description: "Traverse magus's own documentation to answer a \"how does magus do X / what does Y mean / where is Z documented\" question, instead of guessing an answer or a URL."
tags: [agents, skills, magus-upstream-docs]
skill_full_bytes: 5144
skill_short_bytes: 3908
---

# magus-upstream-docs

Traverse magus's own documentation to answer a "how does magus do X / what does Y mean / where is Z documented" question, instead of guessing an answer or a URL. Use when you need authoritative magus behavior (a CLI flag, a spell op, a diagnostic code, a config key, a stdlib module) and the workspace graph cannot give it, or when the docs and your magus binary disagree. Do NOT use for facts about THIS workspace (use magus-query) or to run work (use magus-run).

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus doctor` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus doctor` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `118` |
| `knowledge-schema-version` | `17` |
| `skill-content` | `d21462073818` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-upstream-docs-variant" id="magus-upstream-docs-tab-short" checked>
<label for="magus-upstream-docs-tab-short">Short form</label>
<input type="radio" name="magus-upstream-docs-variant" id="magus-upstream-docs-tab-full">
<label for="magus-upstream-docs-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-upstream-docs/SKILL.md
```

````markdown
# Navigating the magus docs

magus ships one official documentation site. Use it for a magus-domain fact the
workspace graph cannot give: the docs are the source of
truth for magus's behavior.

Send a docs lookup whose answer is all you need to the `magus-scout` agent, where your
harness installed one. Otherwise use a worker on your host's cheapest model.

Two places serve the same pages:

- In the magus repo (a `magusfile.buzz` at the root, a `docs/` tree): query the section
  (next), or read `docs/<name>.md` when you know the page.
- Published: `https://eli.gladman.cc/magus/`. Every page is also raw Markdown at
  `<page-url>index.md`.

## In a magus workspace, ask the graph for the passage

Every Markdown heading is a `docsection` node, so "where is this explained" is a
query, not a scan:

```sh
magus query kind=docsection "cache key"
```

Each result's id is `<path>#<anchor>`. Read that section, not the whole file. The index route below is for the PUBLISHED site,
which has no graph.

## Fast path: start from the index, do not guess URLs

Two files at the docs root turn "find the page" into a lookup:

- `llms.txt`: one titled link per page to its raw Markdown (`<url>index.md`), with a
  one-line description. Read it FIRST, then fetch the page's `index.md`.
- `search-index.json`: an array of `{url, title, text, tags, description}`, one
  record per page. Search it when you do not know the page name.

## URL scheme

Pages use extensionless directory URLs; append `index.md` for the raw source.

| You have                     | Page URL             | Raw Markdown                |
| ---------------------------- | -------------------- | --------------------------- |
| the `go` spell               | `/spells/go/`        | `/spells/go/index.md`       |
| the `magus run` command      | `/manpage/magus-run/`| `/manpage/magus-run/index.md`|
| diagnostic MGS2001           | `/codes/sandbox/MGS2001/` | `.../MGS2001/index.md` |

## Where things live (stable IDs route straight to a page)

Each stable ID maps to a fixed section:

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

Every page has three axes:

- Breadcrumb (up): the trail back to `/documentation/`.
- "In this section" (siblings + children): the other pages under this section's
  landing.
- Prev / next (pager): the adjacent pages in the section.

Land via `llms.txt`, then sweep siblings via "In this section".

## The published site follows main, not your build

The site and its source links (`/blob/main/`) are rendered from main. The binary
you run may be older. When they disagree, trust what the binary prints, such as
`magus <command> -h`.

To read a linked file as your build has it, replace `blob/main` with
`blob/<commit>`. Take the commit from `magus version -o json`. Find the symbol by
name, because line numbers move.

If the docs and the behavior still disagree after that, and you have a
reproduction, the magus-upstream-source skill traces the code at your build.

## In the magus repo

`docs/` Markdown is the source of truth; `docs/gen/` is generated (never edit it;
change the source and regenerate). MAGUS.md is a routing index for HUMAN readers;
do not answer from it: it is true only as of its last
regeneration. `magus query "kind=doc"` lists every
page from the graph.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-upstream-docs-full/SKILL.md
```

````markdown
# Navigating the magus docs

magus ships one official documentation site. It is a static site, so its
structure is fixed and machine-readable: this skill teaches HOW to move through
it; the pages themselves carry the WHAT. Use it for a magus-domain fact the
workspace graph cannot give: the docs are the source of truth for
magus's own behavior, so read them rather than guessing.

Send a docs lookup whose answer is all you need to the `magus-scout` agent, where your
harness installed one. Otherwise use a worker on your host's cheapest model.

Two places serve the same pages:

- In the magus repo (a `magusfile.buzz` at the root, a `docs/` tree): query the section
  (next), or read `docs/<name>.md` when you know the page. This is where
  the skill is dogfooded, so prefer it here.
- Published: `https://eli.gladman.cc/magus/`. Every page is also raw Markdown at
  `<page-url>index.md`.

## Contents

- In a magus workspace, ask the graph for the passage
- Fast path: start from the index, do not guess URLs
- URL scheme
- Where things live (stable IDs route straight to a page)
- Traversing within the docs
- The published site follows main, not your build
- In the magus repo

## In a magus workspace, ask the graph for the passage

Every Markdown heading is a `docsection` node, so "where is this explained" is a
query, not a scan:

```sh
magus query kind=docsection "cache key"
```

Each result's id is `<path>#<anchor>`. Read that section, not the whole file.
`project=<p>` scopes it, and `magus explain "docsection:<path>#<anchor>"` walks the
page's outline from there. The index route below is for the PUBLISHED site,
which has no graph.

## Fast path: start from the index, do not guess URLs

Two files at the docs root turn "find the page" into a lookup:

- `llms.txt`: one titled link per page to its raw Markdown (`<url>index.md`), with a
  one-line description. Read it FIRST, then fetch the page's `index.md`.
- `search-index.json`: an array of `{url, title, text, tags, description}`, one
  record per page. Grep it for a keyword when you do not know the page name.

WRONG: guess `https://.../go-spell` or grep the open web.
CORRECT: read `llms.txt` (or `docs/` locally), find the entry, fetch its Markdown.

## URL scheme

Pages use extensionless directory URLs; append `index.md` for the raw source.

| You have                     | Page URL             | Raw Markdown                |
| ---------------------------- | -------------------- | --------------------------- |
| the `go` spell               | `/spells/go/`        | `/spells/go/index.md`       |
| the `magus run` command      | `/manpage/magus-run/`| `/manpage/magus-run/index.md`|
| diagnostic MGS2001           | `/codes/sandbox/MGS2001/` | `.../MGS2001/index.md` |

## Where things live (stable IDs route straight to a page)

Each stable ID maps to a fixed section, so you jump without searching:

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

Every page has three axes, so from one page you can reach its whole area:

- Breadcrumb (up): the trail back to `/documentation/`.
- "In this section" (siblings + children): the other pages under this section's
  landing. A `page_type: overview` page IS a section landing.
- Prev / next (pager): the adjacent pages in the section.

So: land via `llms.txt`, read the page, then use "In this section" to sweep its
siblings; do not re-search for each one.

## The published site follows main, not your build

The site and its source links (`/blob/main/`) are rendered from main. The binary
you run may be older: a flag, an op or a diagnostic can differ between a page
and your build. When they disagree, trust what the binary prints, such as
`magus <command> -h`.

To read a linked file as your build has it, replace `blob/main` with
`blob/<commit>`. Take the commit from `magus version -o json`. Find the symbol by
name: line numbers move between versions, so the link's anchor points at main's line.

If the docs and the behavior still disagree after that, and you have a
reproduction, the magus-upstream-source skill traces the code at your build.

## In the magus repo

`docs/` Markdown is the source of truth; `docs/gen/` is generated (never edit it;
change the source and regenerate). MAGUS.md is a routing index for HUMAN readers;
do not answer from it: it is true only as of the
last regeneration, and every fact in it has a live command. The knowledge graph
carries every page as a `doc` node, so `magus query "kind=doc"` (see the
magus-query skill) lists them from the graph.
````


</section>

</article>
