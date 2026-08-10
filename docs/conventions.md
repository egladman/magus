---
title: Conventions
description: How to read the magus docs - placeholders, shell commands, runnable examples, admonitions, code-block titles, and auto-generated pages.
tags: [conventions, documentation, placeholders, examples, style, reference]
---

# Conventions

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

| notation     | means                                     | example                                          |
| ------------ | ----------------------------------------- | ------------------------------------------------ |
| `<value>`    | required; replace it                      | `magus run <target>`                             |
| `[thing]`    | optional; omit the brackets if you use it | `magus ls [flags]`                               |
| `<a\|b\|c>`  | required, and one of these exact words    | `magus completion <bash\|zsh\|fish\|powershell>` |
| `<value>...` | repeatable; one or more, space separated  | `magus describe file <path> [<path>...]`         |
| `word[s]`    | the `s` is optional - both spellings work | `magus describe spell[s]`                        |

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

Two tags, and the difference is whether you are meant to copy the block or read it.

A `` ```sh `` block is a **command block**. It omits the shell prompt, so you can copy the
whole thing as-is with no leading `$` or `>` to strip. A `#` comment on or after a line
shows expected output or an aside:

```sh
magus version
# magus <version> (<commit>) built <date>
```

Where the real output carries a value that changes between builds or between machines -
a version, a commit, a duration, a cache key - the comment shows the SHAPE with
placeholders in it, not one machine's answer. A pasted-in literal goes stale silently;
a shape does not.

A `` ```console `` block is a **session transcript**: a command and the output it actually
produced, with the `$` prompt kept because that is what separates the two. You read these
rather than copy them. Several are captured from real runs against a fixture workspace and
re-injected on every build, so they cannot drift from what the command prints
([`cmd/magus-examples`](https://github.com/egladman/magus/blob/main/cmd/magus-examples/main.go)).

Both rules are enforced by `magus run conventions docs`, so a prompt cannot creep into a
copyable block and a pinned version cannot creep into example output
([`docs/lib/conventions.buzz`](https://github.com/egladman/magus/blob/main/docs/lib/conventions.buzz)).

Windows examples are shown in PowerShell and labeled as such.

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
So `proc\exec` is the `exec` function the `os` module provides, while `site.docPages`
is a field on the `site` object. A module name never appears on the left of a dot,
and a variable never appears on the left of a backslash.

The full module list is the [standard library reference](reference/buzz/index.md).

## Runnable examples

Some Buzz code blocks are live. They carry a bar above (**Open in Playground**, and a
copy button) and a **Run** button below; Run executes the snippet in your browser via
the same WebAssembly build of Buzz the [playground](playground.html) uses, and the output
lands in a panel under the block. Nothing is sent anywhere - there is no server in this
loop, and no install. Blocks without the bars are illustrative only. (With JavaScript
off, every block is plain, copyable text.)

This one is live. Press Run:

<!-- magus-run -->

```buzz
import "std";
import "strings";

// Target names are written in snake_case and exposed in kebab-case, so the target
// `go_build` is the one you invoke as `magus run go-build`.
std\print(strings\kebabCase("go_build"));
std\print(strings\kebabCase("buildPlayground"));
```

An author opts a block in with an HTML comment on the line directly above the fence:

````md
<!-- magus-run -->

```buzz
std\print("hello");
```
````

There are two markers. `<!-- magus-run -->` evaluates the snippet and shows what it
printed, which suits standard-library examples. `<!-- magus-run-recorder -->` is for
magusfile and spell examples: those fork real tools, which a browser cannot do, so it
runs the snippet in dry-run and reports the tool invocations it WOULD have triggered as
a trace. Both are wired in
[`docs/lib/html.buzz`](https://github.com/egladman/magus/blob/main/docs/lib/html.buzz) (the
marker becomes a `data-magus-run` attribute at build time) and driven by
[`docs/src/site/run-example.ts`](https://github.com/egladman/magus/blob/main/docs/src/site/run-example.ts).

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

[^example]: Authored as `text[^label]` in the prose, with a matching `[^label]: note`
    line anywhere in the file.

## Cross-links

Nobody hand-maintains the links between these pages. Three passes add them while the
site is built:

- **Glossary terms.** The first linkable occurrence of each term from the
  [glossary](glossary.md) on a page becomes a link to its entry. First occurrence only,
  so a page that leans on a term gets one quiet link rather than a field of them, and
  never inside a code block or an existing link.
- **Code entities.** Inline code that names a diagnostic code, a CLI command, a config
  key, or a stdlib method (`` `MGS1002` ``, `` `magus affected` ``, `` `fs\glob` ``)
  links to its reference page.
- **Convention hints.** Each rendered convention marker - an admonition title, a
  code-block caption, the first angle-bracket placeholder - grows a small `?` that links
  back to the matching section of this page.

All three bake the target's one-line definition into the link as a `data-def` attribute.
That is what the hover popover reads: it never fetches anything, it reads the text
already in the page. On a touch device, where there is no hover, the same content opens
as a panel below the paragraph instead. With JavaScript off, every one of them is still
an ordinary link to the page that defines the thing, so nothing is lost - only the
shortcut is.

The whole-corpus view runs the other direction: the glossary page lists, per term, every
page that references it. That is an aggregate over the full corpus, so it is computed
after every page has been walked.

## What runs when

Almost everything on these pages is decided at build time and shipped as plain HTML:
footnotes and their back-links, all three kinds of cross-link and their definitions, the
table of contents, breadcrumbs, reading time, the auto-generated chip, and the
`Last updated` provenance line. There is no client-side rendering step and no API behind
this site - it is a static tree of files.

A few things are deliberately left to the browser, each for its own reason:

| feature             | why it is not precomputed                                                                                                                                                                                                           |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| syntax highlighting | highlight.js colors the fenced blocks on load so they track your light/dark theme; the markup ships uncolored and legible                                                                                                           |
| mermaid diagrams    | rendered from the fence's source on load, likewise theme-aware                                                                                                                                                                      |
| runnable examples   | the Buzz WebAssembly module is a large download, so it loads only if you press **Run**                                                                                                                                              |
| relative timestamps | `Last updated` ships as an absolute date and is swapped to "3 days ago" in the browser. A build-time relative date would change every day, which would make the rendered site differ from the committed one and trip the drift gate |

Each is additive. With JavaScript off you get uncolored code, plain fenced text where a
diagram would be, an absolute date instead of a relative one, and no Run button - never a
blank page.

## Code-block titles

A fenced block can carry a filename or label in a small caption bar above it, so you
know which file a snippet belongs in (for example a `magusfile.buzz`).

## Diffs

A `` ```diff `` block shows a change: added lines (leading `+`) render as a green band,
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
[configuration reference](reference/config.md) - lead their tag row with this chip:

<div class="post-tags" aria-label="Example chip">
  <span class="tag generated" data-tooltip="Auto-generated from source; edit the generator, not this page" title="Auto-generated from source; edit the generator, not this page">auto-generated</span>
</div>

It is filled rather than outlined so it reads as a status, not a topic, next to the
topical tags beside it. Edit the generator, not the page; a hand edit is overwritten on
the next build.

## Reading time

Longer pages show an estimated reading time near the top. Nothing is measured about you -
it is computed from the Markdown source at build time and baked into the page, so it is
the same number for every reader.

It is not a raw word count. Prose is counted at 220 words per minute, a line of code at
two seconds (code is read deliberately, not skimmed), and an image or diagram at ten
seconds, so a code-heavy page gets a truer estimate than its word count would suggest.
Pages under about 45 seconds get no chip at all. The whole calculation is
[`readingTime` in `docs/engine/meta.buzz`](https://github.com/egladman/magus/blob/main/docs/engine/meta.buzz#L49).
