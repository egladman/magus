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

## Command synopsis notation

Every synopsis on this site and in `magus <verb> -h` and the manpages uses the
same five marks. This is the whole vocabulary:

| notation | means | example |
| --- | --- | --- |
| `<value>` | required; replace it | `magus run <target>` |
| `[thing]` | optional; omit the brackets if you use it | `magus ls [flags]` |
| `<a\|b\|c>` | required, and one of these exact words | `magus completion <bash\|zsh\|fish\|powershell>` |
| `<value>...` | repeatable; one or more, space separated | `magus describe file <path> [<path>...]` |
| `word[s]` | the `s` is optional - both spellings work | `magus describe spell[s]` |

The last one is the only place square brackets do NOT mean "optional argument":
`spell[s]` means `magus describe spell` and `magus describe spells` are the same
command, not that `s` is a separate thing you can pass.

Combining them reads left to right, so `[<path>...]` is "optional, and if you give
it, one or more paths":

```sh
magus run <target> [flags] [project...]
magus describe file <path> [<path>...] [flags]
```

`[flags]` and `[args]` are categories rather than placeholders - there is nothing
called "flags" to substitute. Run the command with `-h` to see which it accepts.

A bare `--` ends magus's own arguments; everything after it is passed through
untouched to whatever the target runs:

```sh
magus run test libs/foo -- -run TestX
```

Values are written `--flag <value>` in synopses, but every magus flag also accepts
`--flag=<value>`, `-flag <value>` and `-flag=<value>`. Pick whichever reads
better; they parse identically.

Some flags take a comma-separated list, which is written as one value. Spaces
around the commas are trimmed and empty entries are ignored:

```sh
magus status --probe=mcp,liveness
```

A few take a structured value spelled `key=<value>` pairs, comma separated. Where
a pattern is accepted it is always the same three types:

```sh
magus watch --ignore type=glob,pattern='**/node_modules/**'
magus where --filter type=regex,pattern='^libs/'
```

## Shell commands

Command blocks omit the shell prompt - copy the whole block as-is, no leading `$` or
`>` to strip. A `#` comment on or after a line shows expected output or an aside:

```sh
magus version
# magus 0.4.2
```

Windows examples are shown in PowerShell and labelled as such.

## Reading Buzz: the backslash

Buzz code on this site is full of names like `fs\readFile` and `magus\project`. The
backslash is namespace access - it reaches into a module. Most languages spell this
with a dot, so it is the one piece of syntax worth knowing before you read anything
else here.

Buzz uses both separators, and the distinction is what they reach into:

```buzz
final body = fs\readFile("VERSION");   // backslash: a function IN the fs module
ctx.needs(build);                      // dot: a method ON the ctx value
```

Backslash reaches into a **module**; dot reaches into a **value** you already have.
So `os\exec` is the `exec` function the `os` module provides, while `site.docPages`
is a field on the `site` object. A module name never appears on the left of a dot,
and a variable never appears on the left of a backslash.

The full module list is the [standard library reference](reference/buzz/index.md).

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

Reach for a footnote when a sentence needs a source, a caveat, or a pointer that
would derail it inline: a citation or external reference, an edge case that qualifies
the claim, or a "see also" that is worth keeping but not worth interrupting the
thought. Prefer a footnote over a parenthetical that runs long, and over dropping the
detail entirely.

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
 export fun ci(ctx: magus\Context, args: [str]) > void {
-    ctx.needs(lint);
+    ctx.needs(lint, test);
 }
```

## Auto-generated pages

Pages built from source - the [module reference](reference/buzz/index.md), the
[spell reference](concepts/spells.md), the [man pages](reference/manpage/magus.md), and the
[configuration reference](reference/config.md) - carry an **auto-generated** chip. Edit the
generator, not the page; a hand edit is overwritten on the next build.

## Reading time

Longer pages show an estimated reading time near the top. It is a word count of the
source, not a tracker - nothing is measured about you.
