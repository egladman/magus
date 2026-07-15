---
title: Documentation conventions
description: How to read the magus docs - placeholders, shell commands, runnable examples, admonitions, code-block titles, and auto-generated pages.
tags: [conventions, documentation, placeholders, examples, style, reference]
---

# Documentation conventions

A few conventions run through every page on this site. This page is the key.

## Placeholders

Angle brackets mark a value you replace with your own - never type the brackets:

```sh
magus run <target>
magus completion <shell>    # e.g. bash, zsh, fish
```

`<target>`, `<path>`, `<shell>`, `<name>` and the like are stand-ins, not literal text.

## Shell commands

Command blocks omit the shell prompt - copy the whole block as-is, no leading `$` or
`>` to strip. A `#` comment on or after a line shows expected output or an aside:

```sh
magus version
# magus 0.4.2
```

Windows examples are shown in PowerShell and labelled as such.

## Runnable examples

Some Buzz code blocks are live: a **Run** button appears in the corner and executes
the snippet in the in-browser [playground](playground.html) via WebAssembly - no
install needed. Blocks without the button are illustrative only. (With JavaScript
off, every block is plain, copyable text.)

## Admonitions

Call-outs are rendered from GitHub-style alert blockquotes and carry a colored accent
per type:

> [!NOTE]
> Context worth knowing, but not a warning.

<!-- -->

> [!WARNING]
> Something that can bite you if ignored.

The types are `NOTE`, `TIP`, `IMPORTANT`, `WARNING`, and `CAUTION`.

## Footnotes

An aside that would break the flow inline is written as a footnote: a bracketed
superscript like this[^example] links to a short note at the foot of the page, which
links back. The generated module reference uses them to flag methods that also exist
in Buzz's own standard library without cluttering each signature.

[^example]:
    Authored as `text[^label]` in the prose, with a matching `[^label]: note`
    line anywhere in the file.

## Code-block titles

A fenced block can carry a filename or label in a small caption bar above it, so you
know which file a snippet belongs in (for example a `magusfile.buzz`).

## Diffs

A ` ```diff ` block shows a change: added lines (leading `+`) render as a green band,
removed lines (leading `-`) as a red one.

```diff
 export fun ci(args: [str]) > void {
-    magus.needs(magus.target.literal("lint"));
+    magus.needs(magus.target.literal("lint"), magus.target.literal("test"));
 }
```

## Auto-generated pages

Pages built from source - the [module reference](buzz/modules/index.md), the
[spell reference](spells.md), the [man pages](manpage/magus.md), and the
[configuration reference](config.md) - carry an **auto-generated** chip. Edit the
generator, not the page; a hand edit is overwritten on the next build.

## Reading time

Longer pages show an estimated reading time near the top. It is a word count of the
source, not a tracker - nothing is measured about you.
