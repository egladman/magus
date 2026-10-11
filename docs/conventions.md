---
title: Conventions
description: "How to read the magus docs: placeholders, shell commands, runnable examples, admonitions, code-block titles, and auto-generated pages."
tags: [conventions, documentation, placeholders, examples, style, reference]
---

# Conventions

A few conventions run through every page on this site. This page is the key.

## Placeholders

Angle brackets mark a value you replace with your own; never type the brackets:

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
| `word[s]`    | the `s` is optional; both spellings work  | `magus describe spell[s]`                        |

The last one is the only place square brackets do NOT mean "optional argument":
`spell[s]` means `magus describe spell` and `magus describe spells` are the same
command, not that `s` is a separate thing you can pass.

Combining them reads left to right, so `[<path>...]` is "optional, and if you give
it, one or more paths":

```sh
magus run <target> [flags] [project...]
magus describe file <path> [<path>...] [flags]
```

`[flags]` and `[args]` are categories rather than placeholders: there is nothing
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

Where the real output carries a value that changes between builds or between machines
(a version, a commit, a duration, a cache key), the comment shows the SHAPE with
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
backslash is namespace access: it reaches into a module. Most languages spell this
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
lands in a panel under the block. Nothing is sent anywhere; there is no server in this
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
- **Convention hints.** Each rendered convention marker (an admonition title, a
  code-block caption, the first angle-bracket placeholder) grows a small `?` that links
  back to the matching section of this page.

All three bake the target's one-line definition into the link as a `data-def` attribute.
That is what the hover popover reads: it never fetches anything, it reads the text
already in the page. On a touch device, where there is no hover, the same content opens
as a panel below the paragraph instead. With JavaScript off, every one of them is still
an ordinary link to the page that defines the thing, so nothing is lost but the
shortcut.

The whole-docs view runs the other direction: the glossary page lists, per term, every
page that references it. That is an aggregate over every page, so it is computed
after every page has been walked.

## What runs when

Almost everything on these pages is decided at build time and shipped as plain HTML:
footnotes and their back-links, all three kinds of cross-link and their definitions, the
table of contents, breadcrumbs, reading time, the auto-generated chip, and the
`Last updated` provenance line. There is no client-side rendering step and no API behind
this site; it is a static tree of files.

A few things are deliberately left to the browser, each for its own reason:

| feature             | why it is not precomputed                                                                                                                                                                                                           |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| syntax highlighting | highlight.js colors the fenced blocks on load so they track your light/dark theme; the markup ships uncolored and legible                                                                                                           |
| runnable examples   | the Buzz WebAssembly module is a large download, so it loads only if you press **Run**                                                                                                                                              |
| relative timestamps | `Last updated` ships as an absolute date and is swapped to "3 days ago" in the browser. A build-time relative date would change every day, which would make the rendered site differ from the committed one and trip the drift gate |

Each is additive. With JavaScript off you get uncolored code, plain fenced text where a
diagram would be, an absolute date instead of a relative one, and no Run button, but never a
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

Pages built from source (the [module reference](reference/buzz/index.md), the
[spell reference](concepts/spells.md), the [man pages](reference/manpage/magus.md), and the
[configuration reference](reference/config.md)) lead their tag row with this chip:

<div class="post-tags" aria-label="Example chip">
  <span class="tag generated" data-tooltip="Auto-generated from source; edit the generator, not this page" title="Auto-generated from source; edit the generator, not this page">auto-generated</span>
</div>

It is filled rather than outlined so it reads as a status, not a topic, next to the
topical tags beside it. Edit the generator, not the page; a hand edit is overwritten on
the next build.

## Page shape

Every hand-written page opens the same way, so a reader and a script each find the same
thing in the same place. `magus run lint docs` refuses a page that departs from it:

```md
---
title: <the page's name>
order: <a number, optional; or page_type: overview>
description: <one or two sentences, ending with a period.>
tags: [<topics>]
aliases: [<old paths>, optional]
---

# <the title, word for word>
```

- **Keys in that order.** The description sits where every other page keeps it, and
  `aliases` trails because only a page that moved has any.
- **The description ends with a period.** Search results and link previews show it as a
  sentence.
- **The H1 is the title.** The title names the page in the browser tab, search and every
  index; the H1 names it on the page. A reader who followed one should land on the other.
- **Pages link pages by relative `.md` path**, never by their published URL. A relative
  link is checked by this lint and the build, works in a local preview and on GitHub, and
  points at the same version of the docs the reader is on. The hosted console is an app,
  not a page, so a link to it stays absolute.

Generated pages are their generator's to fix, and skip these checks.

## Prose punctuation and headings

Hand-written Markdown anywhere in the repository keeps to plain punctuation, and
`magus run lint-rules .` refuses a file that departs from it
([`hack/lint/markdown-proofread.buzz`](https://github.com/egladman/magus/blob/main/hack/lint/markdown-proofread.buzz)):

- **No typographic characters outside code.** No em or en dash, curly quote or ellipsis
  character: each has a plain spelling anyone can type and search for.
- **No hyphen standing in for a dash.** A hyphen spaced between words (`a - b`), or a
  doubled one (`a -- b`), leaves the reader to guess how the two halves relate. Name it: a
  colon before an explanation or a list, a semicolon between two related clauses, commas
  or parentheses around an aside, or two sentences. A list marker, a table cell holding
  only `-`, and anything in code are not dashes, so a command's `--` goes in a code span.
- **Headings in sentence case.** After the first word, only proper nouns, acronyms and
  identifiers are capitalized.

Generated Markdown is its generator's to fix, and skips these checks too.

## Architecture decision records

Every page in `docs/decisions/` named `NNNN-<slug>.md` is an ADR, and every ADR follows
one template, so a reader finds the same thing in the same place on each:

```md
---
title: "ADR NNNN: <lowercase title>"
order: <NNNN as a number>
description: <one paragraph>
tags: [adr, decision, <topics>]
status: <proposed, accepted, rejected, superseded or deprecated>
date: <YYYY-MM-DD>
supersedes: <what this replaces, optional>
---

# ADR NNNN: <lowercase title>

## Context

## Decision

## Alternatives

## Consequences

## Open questions

## Amendments

### <YYYY-MM-DD>: <what changed>
```

- The front matter's `title` carries the file's number and a title that starts lowercase;
  `order` is that number, so the pages sort as they were written; `tags` include `adr`.
- The H1 repeats the title.
- `status`, `date` and `supersedes` follow `tags`, before `aliases`. `status` is one of
  _proposed_, _accepted_, _rejected_, _superseded_ and _deprecated_, optionally followed by
  a parenthetical, such as `accepted (partly implemented)`. `date` is the day the ADR was
  written, as a real `YYYY-MM-DD`. `supersedes` is there only when the ADR replaces
  something, and names it the way a sentence would. The site prints all three under the
  title, so the page body carries no Status, Date or Supersedes bullet.
- The H2 sections are exactly Context, Decision, Alternatives and Consequences, in that
  order, then Open questions if any remain, then Amendments last. Any further structure is
  an H3 under one of them (numbered parts of a decision, each option weighed, when to
  revisit), so the H2 list is the same on every page.
- Each amendment is an H3 under Amendments that opens with its date:
  `### 2026-09-29: <what changed>`.
- A table whose last column is `State` uses only _done_, _in progress_, _planned_,
  _proposed_ and _not built_, each optionally followed by a parenthetical, such as
  `in progress (#550)`.

`magus run conventions docs` enforces all of it as the `adr-template` rule
([`docs/lib/adr.buzz`](https://github.com/egladman/magus/blob/main/docs/lib/adr.buzz)).

## Writing rules

Hand-written Markdown, every pull request's title and description, review replies, doc
comments and the text an agent loads read as plain technical writing: a teammate
understands each sentence without the request, the conversation or the tool behind it.
The checks are a Go package, proofread, in
[`libs/conventions/proofread`](https://github.com/egladman/magus/blob/main/libs/conventions/proofread);
the `proofread` command runs it. The guard's `pull-request-text` rule runs it on a
pull request description and on a reply an agent posts, and the `pr-description` check
runs it on every pull request, so the two cannot disagree. A person runs it on a draft
before posting. [ADR 0008](decisions/0008-writing-a-teammate-reads.md) holds the
design.

[Each rule has a page](reference/proofread/index.md): what it catches, why, its default
decision on each kind of text, and its `PRF` code. A finding names the rule and the code
and links to that page.

### Kinds

A rule judges text by what the text is for.

| Kind                          | Text                                                            |
| ----------------------------- | --------------------------------------------------------------- |
| `reference`                   | a hand-written page a reader looks things up in                 |
| `guide`                       | a procedural page, under `docs/guides/`                         |
| `change-description`          | a pull request: its title on the first line, its description    |
| `review-reply`                | a review comment, a review's body or a reply in a thread        |
| `agent-instructions`          | Markdown an agent loads as written, such as a SKILL.md          |
| `agent-instructions-template` | a template that renders a short and a full form of such a skill |
| `doc-comment`                 | one symbol's doc comment, read from the symbol index            |

### Decisions

Each rule ships a default decision per kind, `deny` or `advise`, the two words a guard
rule's default takes. A caller refuses text on a `deny`, reports an `advise` once and
lets the text through, and never sees a rule that is `off`.

A repository changes any of them with a decisions table: a file that sets a rule
`off`, `advise` or `deny` for everything judged, or for the files a glob matches, the
way `magus\guard.builtins` sets a built-in guard rule. A glob entry is an exemption and
carries its reason, as a guard override does. There are no named profiles; a table is
the one spelling.

House style ships `off`. It is the set of rules that encode one repository's
conventions rather than writing a teammate reads: the glossary (`terms`), the present
tense with no author (`tense`), no credit to a tool (`attribution`), plain ASCII
typography (`dash`, `ascii`), the doc-comment budgets, and the agent-instruction budgets.
This repository turns them on in `hack/policy/proofread.buzz`, with a reason beside every
entry. With no table, proofread holds text to shape, tone, claims and the
generated-writing tells alone, which suits a repository that has not chosen a house
style.

The findings are a contract: `{source, kind, rule, code, decision, message, match, url}`.
Any command that writes the same array, such as a team's own style checker, feeds the
same guard rule and the same decisions table.

### The shape of a description

A change description is read by a reviewer now and by whoever follows the squash commit
to it later, so it opens with what changed for a reader and keeps the rest in named
sections.

| Part              | Required                                   | What it holds                                                                                                      |
| ----------------- | ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------ |
| Lead (no heading) | yes                                        | The outcome first: what a reader can now do or no longer has to do. Then how the work came up and why it mattered. |
| What changes      | yes                                        | One bullet per change, in the present tense, with the code as the subject.                                         |
| Why this approach | when an alternative was weighed            | The alternative and the reason it was set aside.                                                                   |
| Evidence          | when the description claims a measurement  | The numbers, how they were taken and where the run is.                                                             |
| How we got here   | when the origin needs more than a sentence | The earlier design, what it was for and what changed around it.                                                    |
| Not verified      | when something was not tested              | What was not checked, and why.                                                                                     |
| Reading guide     | optional                                   | Where to start reading.                                                                                            |

The lead is at least 12 words and names a result, not the defect: the defect is the
reason (`lead-context`). A change in a series names the destination and its own place in
it. Headings after the lead are sentence case. The stock labels a reply generator emits
(`Summary`, `Test plan`, `Overview`), a bold label opening a bullet, an opener that
answers a request (`This PR`, `Here's`, `I've`) and a reference to a conversation
(`as discussed`) stay refused, because they answer a prompt the reader never saw
(`reply-voice`). The word budget applies to everything after the lead, so cutting words
never removes the origin or the reason.

### Claims and evidence

A sentence that states a measurement (`410ms to 260ms`, `7 of 50`), a comparison
(`faster`, `fewer`), a completion (`fixes`, `no longer flakes`) or an absolute about
behavior carries its evidence in the same sentence or bullet: a code span naming the
command, test or benchmark, a link, or an output ref (`claim`). A claim covers exactly
what was measured. A judgment call is labeled as one, with its reason. What is not known
goes under Not verified, or opens a sentence with `Not measured`, `Not tested` or
`Untested`.

A softener on a claim (`might fix`, `may help`, `probably`, `aims to`) stays refused
(`hedge`): it lets a claim stand with no evidence. A `may` that grants a permission or
states a contract (`a workspace may declare`) is not a hedge. In this repository the
guard also resolves every output ref a description cites, and refuses one that does not
exist or names a failed run.

### Tone

The tone rules describe a situation, never a person or the earlier code's character:
"the cache kept the old key after a rename", not "the cache was broken". Each names the
posture it catches and how to say the same fact.

- `condescension`: no word that tells the reader how hard a step should feel or what
  they should already know (`obviously`, `of course`, `simply`, `easy`).
- `blame`: no person or past work as the subject of a fault (`should have`, `forgot to`,
  `sloppy`).
- `verdict`, `absolute`, `intent` and `credit` advise: a judgment word standing in for
  the behavior (`messy`), `never` or `nobody` about the past, a motive given to a tool
  (`pretends`), and a removal that names nothing the old design was for.

`blame` denies and the other four advise, because the words they catch also state
contracts elsewhere ("never returns nil"); each moves to `deny` only after it has fired
on real text and every firing was right.

### Review replies

A reply takes the tone rules and four of its own. A reply does not open by
contradicting (`No,`, `Actually,`, `As I said`; `reply-opener`), labels a
recommendation as a judgment and gives its reason (`judgment-as-fact`), states a doubt
once instead of stacking softeners or apologizing before a valid point
(`stacked-hedge`), and offers a call by the fourth reply of one author in a thread
(`long-thread`, which needs the thread length the caller passes in). Reading a thread is
provider I/O, so the guard passes none and `long-thread` stays silent there.

### Generated-writing tells

The deterministic checks of the `stop-slop` and `humanizer` skills run in proofread, so
a skill spends no tokens on them. They refuse chatbot residue (`leak`, `chatbot`),
announcements standing where the point should be (`signpost`, `closer`), weight asserted
with nothing named (`vague`), words chosen to sound significant (`buzzword`, with
`buzzword-weak` advising on those that have an ordinary sense), throat-clearing and
filler (`filler`, `wordy`), a claim made by denying its opposite (`contrast`), runs of
sentences of six words or fewer (`staccato`) and a participle clause that claims significance
(`ing-tail`). What stays in a skill is the judgment no rule sees: a claim with no
specific behind it, and padding.

### Other rules

- `terms`: one spelling per glossary term, such as `subagent`.
- `attribution`: no credit to a tool and no account of how the work was made
  (`Co-Authored-By`, `Generated with`, `after several iterations`). Agents, subagents,
  prompts and sessions are what magus is about, so a page names them freely; in a pull
  request an agent that found or fixed something, or `this session`, is the story of the
  work and is refused.
- `tense`: the present tense, describing the code after the change, with no `will`. A page
  may speak as the project (`we believe`), but no author is the actor of a change
  (`we added`), and a pull request names no author at all.

A skill is loaded into an agent's context every session, so what a skill's short form
shows meets three more rules in the `agent-instructions` kinds: no sentence over 25 words
(`terse-sentence`), no paragraph or list item over 60 (`terse-paragraph`), and no phrase
with a shorter equivalent (`wordy`, such as `in order to` for `to`). In a skill body
under `internal/agent/skills/`, text inside an `{{if .Full}}` arm is in the full form only
and meets the rules above alone, plus `bare-rule`. In a skill a rule is only what magus
enforces (a guard, workspace or lint rule, a diagnostic, or a rule id) and everything
else a skill asks of an agent is an instruction, so `bare-rule` refuses `rule` in either
form unless a qualifier or a rule id in code names the mechanism.

A guide is a procedure the reader follows with a terminal open: every page under
`docs/guides/`, which holds the setup and migrating pages too. The `guide` kind holds it
to rules on top of the reference ones: it addresses the reader as you, with no `we`,
`us` or `our` (`second-person`); each step of a numbered procedure opens with its verb,
never an article, a pronoun, `You` or a bare code span (`step-verb`), while a numbered
list with no imperative step is ordered facts and is left alone; and no word tells the
reader a step is easy (`condescension`: `easy`, `simple`, `obviously`, `of course`,
`clearly`, `please`, and `just` before a verb).

The `banned-words` lint rule holds hand-written Go, Buzz, Markdown, TypeScript, CSS,
HTML, YAML, proto and txtar files to three bans: `surface` as a noun, where the verb in
`surfaces an error` passes, `lane` and `corpus`. Each finding names what to write instead:
what the thing is, `write paths`, or `the cases`.

Quoted text and code are mentions, not use, so a page can name the words a rule refuses.
The guard and the check also ask, as advice that fails nothing, why a pull request touches
a project or top-level package its description never names.

## Reading time

Longer pages show an estimated reading time near the top. Nothing is measured about you:
it is computed from the Markdown source at build time and baked into the page, so it is
the same number for every reader.

It is not a raw word count. Prose is counted at 220 words per minute, a line of code at
two seconds (code is read deliberately, not skimmed), and an image or diagram at ten
seconds, so a code-heavy page gets a truer estimate than its word count would suggest.
Pages under about 45 seconds get no chip at all. The whole calculation is
[`readingTime` in `docs/engine/meta.buzz`](https://github.com/egladman/magus/blob/main/docs/engine/meta.buzz#L49).
