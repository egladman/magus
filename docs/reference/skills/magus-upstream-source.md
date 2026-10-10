---
title: magus-upstream-source
generated_from: internal/agent/skills/magus-upstream-source/SKILL.md
description: "Last resort: read magus's OWN source code at the exact commit of the magus binary in use, to trace behavior its docs cannot explain."
tags: [agents, skills, magus-upstream-source]
skill_full_bytes: 3184
skill_short_bytes: 2353
---

# magus-upstream-source

Last resort: read magus's OWN source code at the exact commit of the magus binary in use, to trace behavior its docs cannot explain. Use only with a reproduction in hand, after magus crashed with a Go panic or after magus-upstream-docs could not reconcile the docs with what the binary does. Do NOT use to read THIS workspace's code (magus-query), to learn how to use magus (magus-upstream-docs), or before ruling out the workspace as the cause.

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
| `agent-skill-version` | `117` |
| `knowledge-schema-version` | `17` |
| `skill-content` | `1865a18b4fde` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-upstream-source-variant" id="magus-upstream-source-tab-short" checked>
<label for="magus-upstream-source-tab-short">Short form</label>
<input type="radio" name="magus-upstream-source-variant" id="magus-upstream-source-tab-full">
<label for="magus-upstream-source-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-upstream-source/SKILL.md
```

````markdown
# Reading magus's own source at your build

This is a last resort. Assume the workspace is wrong before you assume magus is.

Read magus's source only when both hold:

- magus crashed (a Go panic and trace), or the magus-upstream-docs skill could
  not reconcile the docs with what the binary does.
- You have a reproduction: one command and the output that shows the problem.

## Pin the source to the binary

```sh
magus version -o json
```

Read `commit`, `dirty` and `repository`. Use the full `commit`.

Stop when `dirty` is true or `commit` is `unknown`. That build's source exists
nowhere you can fetch, so say so and work from the reproduction alone.

Never read main or the latest release instead. Another version explains
behavior your binary lacks.

## Fetch it outside the workspace

Keep one clone per commit in the user cache. Never clone into the workspace,
where its VCS status and graph would pick it up.

```sh
dir="${XDG_CACHE_HOME:-$HOME/.cache}/magus-upstream/<commit>"
git init -q "$dir"
git -C "$dir" remote add origin <repository>
git -C "$dir" fetch --depth 1 origin <commit>
git -C "$dir" checkout -q FETCH_HEAD
```

Reuse the directory when it already exists. `couldn't find remote ref` means the
commit was never pushed. Stop, as for a dirty build.

## Read it without changing it

Point every command at the clone with `--root "$dir"`, placed before the verb.

- `magus --root "$dir" query "<terms>"` finds doc sections, spells, targets and
  Buzz functions. It needs no build step.
- `magus --root "$dir" refs --text "<pattern>"` searches the clone's files.
- `magus --root "$dir" refs <symbol>` needs a symbol index. Run
  `magus graph build` with the same `--root` once. When refs
  still answers "unknown, not absent", use `refs --text`.
- Read files with your ordinary file tools.

Never run targets in the clone, build it, test it or edit it. The question is
what the code says, and reading answers it.

## Report what the code says

Cite each claim as `<repository>/blob/<commit>/<path>#L<line>`.

Conclude one of two things:

- The workspace does X and the code requires Y. Fix the workspace. This is the usual result.
- magus has a defect. Give the reproduction and the code path that produces it.

Never patch the clone to work around a defect. Never file an issue or post
anything. Hand the finding to the person.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-upstream-source-full/SKILL.md
```

````markdown
# Reading magus's own source at your build

This is a last resort. Assume the workspace is wrong before you assume magus is:
most behavior that looks like a magus bug is a magusfile, a config key or an
input that magus rejects as documented. Reading magus's code is how you find
the line that rejects it, not a hunt for upstream defects.

Read magus's source only when both hold:

- magus crashed (a Go panic and trace), or the magus-upstream-docs skill could
  not reconcile the docs with what the binary does.
- You have a reproduction: one command and the output that shows the problem.

## Contents

- Pin the source to the binary
- Fetch it outside the workspace
- Read it without changing it
- Report what the code says

## Pin the source to the binary

```sh
magus version -o json
```

Read `commit`, `dirty` and `repository`. Use the full `commit`: a forge
refuses a shallow fetch by an abbreviated revision.

Stop when `dirty` is true or `commit` is `unknown`. That build's source exists
nowhere you can fetch, so say so and work from the reproduction alone.

Never read main or the latest release instead. Another version explains
behavior your binary lacks, and it reads as authoritative while doing
it.

## Fetch it outside the workspace

Keep one clone per commit in the user cache. Never clone into the workspace:
it would show up in the workspace's VCS status and in its knowledge graph.

```sh
dir="${XDG_CACHE_HOME:-$HOME/.cache}/magus-upstream/<commit>"
git init -q "$dir"
git -C "$dir" remote add origin <repository>
git -C "$dir" fetch --depth 1 origin <commit>
git -C "$dir" checkout -q FETCH_HEAD
```

Reuse the directory when it already exists. `couldn't find remote ref` means the
commit was never pushed: a local build of an unpublished commit, whose
source lives only on the machine that built it. Stop, as for a dirty build.

## Read it without changing it

Point every command at the clone with `--root "$dir"`, placed before the verb.

- `magus --root "$dir" query "<terms>"` finds doc sections, spells, targets and
  Buzz functions. It needs no build step.
- `magus --root "$dir" refs --text "<pattern>"` searches the clone's files.
- `magus --root "$dir" refs <symbol>` needs a symbol index. Run
  `magus graph build` with the same `--root` once. It runs each
  language's indexer, so it needs that language's toolchain. When refs
  still answers "unknown, not absent", use `refs --text`.
- Read files with your ordinary file tools.

Never run targets in the clone, build it, test it or edit it. Each of
those executes upstream code on your machine for no gain. The question is
what the code says, and reading answers it.

## Report what the code says

Cite each claim as `<repository>/blob/<commit>/<path>#L<line>`.

Conclude one of two things:

- The workspace does X and the code requires Y. Fix the workspace. This is the usual result.
- magus has a defect. Give the reproduction and the code path that produces it.

Never patch the clone to work around a defect. Never file an issue or post
anything. Hand the finding to the person: whether and where to report
it is their decision, and the reproduction and citations are what they need to
make it.
````


</section>

</article>
