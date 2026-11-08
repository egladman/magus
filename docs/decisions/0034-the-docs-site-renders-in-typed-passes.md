---
title: "ADR 0034: the docs site renders in typed passes"
order: 34
description: The docs site generator stops threading one mutable Site object through forty phases. It reads its sources into an immutable corpus, has each section return the pages it renders, writes every page in one place, and checks the written site afterward, with the HTML it produces unchanged.
tags: [adr, decision, docs, ssg, buzz]
status: proposed
date: 2026-11-08
---

# ADR 0034: the docs site renders in typed passes

## Context

`site_render` in `docs/render.buzz` builds one `Site` object holding sixteen mutable
collections, then calls about forty phase functions in a fixed order. Each phase reads
some collections and writes others. Measured on 2026-11-08 at commit d0ac7477a:

- 38 functions take a `site: Site`, across 16 files in `docs/engine`, `docs/site` and
  `docs/lib`.
- 72 statements assign or append to a `site.` field outside the constructor.
- The order matters and is written only in comments: `collectSectionIndexes` must run
  after the `collect*` passes, `writeAliases` after the walk, `buildServiceWorker` after
  `search-index.json` is written.
- `docs/engine/nav.buzz` keeps three module-level `var`s as a lazy cache for the reference
  drawer.
- HTML was built from string concatenation until #712 added a typed node tree in
  `libs/ssg/dom.buzz`.

A reader who wants to know what a page depends on has to trace which earlier phase
filled which field.

## Decision

The render is four passes, each a function from typed values to typed values. No pass
mutates a value another pass reads.

1. Collect: read the docs tree, blog, glossary, conventions, releases and demos into a
   `Corpus`, built once and immutable after.
2. Plan: derive every page's URL, the aliases, the tag index and the navigation from the
   corpus, as an immutable `Plan`.
3. Render: each section of the site implements one protocol and returns its pages as
   values, reading only the corpus and the plan.
4. Write and check: one writer writes every page, prunes what no page claims, then the
   gates read the written set.

```buzz
protocol Section {
    fun pages(corpus: Corpus, plan: Plan) > [Output];
}

protocol Gate {
    fun check(site: Written) > [Problem];
}

object Output {
    path: str,
    body: str,
}
```

The landing, the docs walk, the blog, tags, development, tour, download, search, 404,
offline, feeds and the SEO files each become a `Section`. The asset, orphan, axis and
abandoned-link checks each become a `Gate`. A site adds a section or a gate by adding
one object to a list, never by editing the render function.

The layout follows Go's package nesting. `libs/ssg/` holds what any site could use: the
node tree, text, URL, date, feed and CSP helpers, plus the `Section`, `Gate`, `Corpus`
and `Output` types. `docs/site/` holds magus's own sections, one directory each, and
`docs/render.buzz` keeps only the list of sections and gates.

| Item                                                                                  | State     |
| ------------------------------------------------------------------------------------- | --------- |
| `libs/ssg/dom.buzz`: HTML as a typed node tree rendered once                          | done      |
| `libs/ssg/` holds the site-agnostic helpers moved out of `docs/lib` and `docs/engine` | done      |
| `Corpus`, built once by the collect pass and read-only after                          | not built |
| `Plan`: URLs, aliases, tag index and navigation, derived from the corpus              | not built |
| `Section` and `Gate` protocols; each site section and each check implements one       | not built |
| One writer writes every `Output` and prunes what no output claims                     | not built |
| The `Site` object and the three `var`s in `docs/engine/nav.buzz` are deleted          | not built |
| Every remaining HTML string built by concatenation is a `libs/ssg/dom.buzz` node      | not built |
| The render's wall time before and after, measured on one machine                      | not built |

Every step is proven the same way: the rendered `docs/gen` tree matches, file for file
and byte for byte, the tree rendered before the step.

### Principles checked

- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  a section is code behind a protocol; the corpus and the plan are facts it reads.
- [Clear is better than clever](../design/principles/clear-is-better-than-clever.md): a
  pass's inputs are its parameters, so what a page depends on reads off its signature.
- [Delete before you add](../design/principles/delete-before-you-add.md): the `Site`
  object, its field-by-field reset and the nav cache are deleted, not wrapped.

## Alternatives

- Keep the `Site` object and document the phase order. It lost because the order already
  lives in comments and still has to be traced by hand; 72 mutations stay 72 mutations.
  Reopen if the passes cost measurably more time than the shared object.
- Split the render into separate magus targets per section. It lost because sections share
  the corpus, so each target would re-read the whole docs tree. Reopen if one section
  becomes slow enough to want its own cache key.
- Keep the helpers under `docs/lib`. It lost because a second site cannot import them
  without importing magus's docs; `libs/ssg` makes the boundary a project.

## Consequences

- A section's output is a value, so a test renders one section without writing files.
- The writer is the only code that touches `docs/gen`, so pruning stale files reads the
  set of outputs instead of a `writtenPaths` map filled along the way.
- The migration touches every section, done one section per commit, each proven by the
  byte-for-byte comparison.

## Open questions

- Whether `Plan` needs the rendered titles of synthesized section pages, which today come
  from a pass that runs after the walk.

## Amendments
