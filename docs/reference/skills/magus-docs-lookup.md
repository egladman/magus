---
title: magus-docs-lookup
description: "Traverse magus's own documentation to answer a \"how does magus do X / what does Y mean / where is Z documented\" question, instead of guessing an answer or a URL."
tags: [agents, skills, magus-docs-lookup]
aliases:
  - reference/skills/magus-docs
skill_full_bytes: 3670
skill_simple_bytes: 2956
---

# magus-docs-lookup

Traverse magus's own documentation to answer a "how does magus do X / what does Y mean / where is Z documented" question, instead of guessing an answer or a URL. Use when you need authoritative magus behavior (a CLI flag, a spell op, a diagnostic code, a config key, a stdlib module) and the workspace graph cannot give it. Do NOT use for facts about THIS workspace (use magus-query) or to run work (use magus-run).

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus graph verify` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus graph verify` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `38` |
| `knowledge-schema-version` | `9` |
| `skill-content` | `9ab9d40df1dd` |
| `skill-variant` | `full` |

The `skill-content` digest is shared by both permutations below, so they version together: a magus upgrade makes both stale at once, never one silently.

## Full form

Every mechanical step spelled out, plus the rationale for each. Installed as the `<name>-full` twin: loaded by name rather than always, so a reader who needs the long form can ask for it without every session carrying it.

```markdown
# Navigating the magus docs

magus ships one official documentation corpus. It is a static site, so its
structure is fixed and machine-readable: this skill teaches HOW to move through
it; the pages themselves carry the WHAT. Reach for it when a magus-domain fact
is not derivable from the workspace graph - the docs are the source of truth for
magus's own behavior, so read them rather than guessing.

Two places serve the same pages:

- In the magus repo (a `magusfile.buzz` at the root, a `docs/` tree): read `docs/<name>.md`
  directly. This is where the skill is dogfooded, so prefer it here.
- Published: the deployed site at `https://eli.gladman.cc/magus/`. Every page is
  also emitted as raw Markdown at `<page-url>index.md` for clean fetching.

## FAST PATH: start from the index, do not guess URLs

Two files at the docs root turn "find the right page" into a lookup, not a guess:

- `llms.txt` - one titled link per page, each pointing at that page's raw
  Markdown (`<url>index.md`), with a one-line description. Read this FIRST to
  locate a page, then fetch its `index.md`.
- `search-index.json` - a flat array of `{url, title, text, tags, description}`,
  one record per page. Grep it for a keyword when you do not know the page name.

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

magus mints stable IDs; each maps to a fixed section, so you jump without searching:

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

Every page gives you three axes, so from one page you can reach its whole area:

- Breadcrumb (up): the trail back to `/documentation/`.
- "In this section" (siblings + children): the other pages under this page's
  section landing. A `page_type: overview` page IS a section landing.
- Prev / next (pager): the adjacent pages in the same section.

So: land via `llms.txt`, read the page, then use "In this section" to sweep its
siblings - do not re-search for each one.

## In the magus repo

The `docs/` Markdown is the source of truth; `docs/gen/` is generated output
(never edit it - change the source and regenerate). MAGUS.md is a routing index
generated for HUMAN readers, so do not answer from it: it is true only as of the
last regeneration, and every fact in it has a live command. The knowledge graph
carries every page as a `doc` node, so `magus query "kind:doc"` (see the
magus-query skill) lists them from the graph.
```

## Short form

The enumeration dropped, the judgment kept - for the most capable readers, not the least; the bar under the heading above shows by how much. This is the always-loaded primary. Both are hand-authored from one source body; see [Skills](../../guides/integrations/agents/skills.md) for the difference.

<details>
<summary>Show the short form</summary>

```markdown
# Navigating the magus docs

magus ships one official documentation corpus. Reach for it when a magus-domain fact
is not derivable from the workspace graph - they are the source of
truth for magus's own behavior.

Two places serve the same pages:

- In the magus repo (a `magusfile.buzz` at the root, a `docs/` tree): read `docs/<name>.md`
  directly.
- Published: the deployed site at `https://eli.gladman.cc/magus/`. Every page is
  also emitted as raw Markdown at `<page-url>index.md` for clean fetching.

## FAST PATH: start from the index, do not guess URLs

Two files at the docs root turn "find the right page" into a lookup, not a guess:

- `llms.txt` - one titled link per page, each pointing at that page's raw
  Markdown (`<url>index.md`), with a one-line description. Read this FIRST to
  locate a page, then fetch its `index.md`.
- `search-index.json` - a flat array of `{url, title, text, tags, description}`,
  one record per page. Search it when you do not know the page name.

## URL scheme

Pages use extensionless directory URLs; append `index.md` for the raw source.

| You have                     | Page URL             | Raw Markdown                |
| ---------------------------- | -------------------- | --------------------------- |
| the `go` spell               | `/spells/go/`        | `/spells/go/index.md`       |
| the `magus run` command      | `/manpage/magus-run/`| `/manpage/magus-run/index.md`|
| diagnostic MGS2001           | `/codes/sandbox/MGS2001/` | `.../MGS2001/index.md` |

## Where things live (stable IDs route straight to a page)

magus mints stable IDs; each maps to a fixed section:

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

Every page gives you three axes:

- Breadcrumb (up): the trail back to `/documentation/`.
- "In this section" (siblings + children): the other pages under this page's
  section landing.
- Prev / next (pager): the adjacent pages in the same section.

Land via `llms.txt`, then sweep siblings via "In this section".

## In the magus repo

The `docs/` Markdown is the source of truth; `docs/gen/` is generated output
(never edit it - change the source and regenerate). MAGUS.md is a routing index
generated for HUMAN readers, so do not answer from it: true only as of its last
regeneration. `magus query "kind:doc"` lists every
page from the graph.
```


</details>
