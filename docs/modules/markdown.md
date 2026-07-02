---
title: markdown module
description: Render GitHub-Flavored Markdown to semantic HTML, extract or strip YAML frontmatter, and auto-rewrite relative .md links to .html equivalents.
tags: [markdown, gfm, html, render, frontmatter, docs, magus stdlib]
---

# markdown

GitHub-Flavored Markdown to semantic HTML.

> **Naming convention:** import the module under its bare name (`import "markdown"`) and call methods in `camelCase` (`markdown.someMethod`).

## Methods

### to_html

Render GitHub-Flavored Markdown to semantic HTML. Strips a leading YAML frontmatter block (a "---" fenced header at the top of the document) before rendering. Auto-IDs every heading so #fragment links resolve, and rewrites relative .md links (incl. README.md → index.html) to their generated .html equivalents. Raw HTML in the source is passed through (intended for trusted, first-party docs).

**Signature:** `markdown.toHtml(source) → string` · [source](https://github.com/egladman/magus/blob/main/std/markdown.go#L76)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `source` | `string` |  | |

**Returns:** string

### frontmatter

Parse the leading YAML frontmatter block (a "---" fenced header at the top of the document) and return it as a JSON object string; decode with serialize.jsonDecode. Returns "{}" when no frontmatter is present.

**Signature:** `markdown.frontmatter(source) → string` · [source](https://github.com/egladman/magus/blob/main/std/markdown.go#L90)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `source` | `string` |  | |

**Returns:** string

### strip_frontmatter

Return the Markdown body with any leading YAML frontmatter block removed (the source unchanged when none is present).

**Signature:** `markdown.stripFrontmatter(source) → string` · [source](https://github.com/egladman/magus/blob/main/std/markdown.go#L113)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `source` | `string` |  | |

**Returns:** string

