---
name: magus-skill-authoring
description: The working method for building and maintaining magus's agent surface in THIS repo - the embedded skills, MCP tools, hints, and MAGUS.md routing. Use when editing anything under cmd/magus/skills/, the MCP registry, agent install, or when evaluating what agents can and cannot learn from magus. This skill is hand-authored and committed; it is NOT part of the installed set and never ships in the binary.
---

# Authoring the agent surface

This file encodes the working method behind the magus skills so that any
model, strong or weak, maintains them the same way. The skills exist to stop
agents from guessing, so the process that writes them cannot rest on guesses
either.

## 1. Empiricism before documentation

Never teach behavior you have not executed against a freshly built binary in
this session. Build HEAD (`magus run build .`), start the
daemon, call the actual tool (over MCP HTTP as well as the CLI), and paste the
observed output into your analysis before writing a word of skill text.

Cautionary precedent, found here: the registry advertised dry_run as "print
what would run without executing" - the verified reality was zero bytes of
output AND regenerated files on disk. A skill written from the docs would
have taught agents a "safe preview" that silently mutates the tree.

## 2. Hunt the silent failure

Empty output, zero matches, and exit 1 with no text are findings, not
inconveniences. Probe every claim adversarially before teaching it: when
`project:docs kind:function render` returned 0, the wrong response was a
workaround in the skill; the right response was tracing the scorer, fixing
the filter, and adding a regression test. Fix the tool before teaching the
workaround. When the fix is out of reach, teach ONLY verified idioms and file
the gap where it will be found (the plans doc, a task, magus_memory).

## 3. One source of truth, drift-gated

- The installed skills are generated: embedded in the binary, stamped with
  agentSkillVersion + knowledge schema version, verified by `magus graph
  verify`. Never hand-edit an installed copy; edit cmd/magus/skills/ and
  re-run `magus agent install <dest-dir> --force` (here: .claude/skills).
- Every destination receives identical bytes (a test asserts it). magus is
  agent-host agnostic: no host name appears in code. Host-specific glue (hook
  event shapes, config dialects) is documentation over the neutral surfaces -
  explicit install destinations, the agent hook verdict, --from-json
  extraction, -o template rendering - never a per-host code path.
- Any change to skill content or the tool surface it documents bumps
  agentSkillVersion with a changelog line.
- Skills teach the stable HOW; the workspace WHAT lives in MAGUS.md and the
  live tools. A skill that mentions this repo's specifics is a bug.

## 3b. Two permutations from one body: mark the why, then shorten the rest

A skill body is a `text/template` rendered against the variant, so a permutation
is an ordinary `{{if}}`. Three forms, and that is the whole vocabulary:

```markdown
Run the target first{{if .Full}}, because a raw tool bypasses the cache{{end}}.

{{if .Simple}}Full explains this at length below.{{end}}

Read `llms.txt` first{{if .Full}}, because guessing a URL wastes a fetch and the
index is authoritative{{else}} - it is the index{{end}}.
```

Unconditional text is in both permutations.

Dropping the `{{else}}` means "full says more here". Reaching for it means "both
permutations say this, at different lengths", and that is the ONLY construct that
can shorten something both must express. A bare `{{if .Simple}}` means "simple
says this and full says nothing", which is almost always a mistake worth catching
in review.

Measured 2026-07-31: the ten shipped skills have 137 full-only branches and only
28 `{{else}}` arms. Most distinctions are still deletion rather than re-wording;
reach for `{{else}}` whenever a passage survives into simple at full length.

A third permutation costs a constant, not a new markup convention:

```markdown
{{if .Is "minimal"}}bare imperative{{else if .Full}}the long version{{else}}the short one{{end}}
```

### Showing template syntax inside a skill

The body IS a template, including inside fenced code blocks, so a skill that
documents `{{ }}` syntax must escape it as a string constant. magus-run
documents the `-o template` flag and magus-buzz documents mustache; both hit
this:

```markdown
`-o template='{{"{{.Field}}"}}'`
```

Getting it wrong is a loud failure at install (a parse error for an unknown
function, an execute error for an unknown field), never a silently mangled file.

### Who simple is FOR, and therefore what it cuts

Simple is not the beginner permutation. It is installed for the most capable
readers - the models that can re-derive an imperative from the tool surface and
do not need it spelled out. That inverts the obvious instinct, so state the
consequence plainly: **simple sheds ENUMERATION and keeps JUDGMENT.** It is not
"the steps without the why". A permutation that keeps the steps and drops the
why hands its strongest reader the half it could have reconstructed and takes
away the half it could not.

Ask of every branch: could a capable reader work this out from `magus describe`,
`-h`, or the docs? Then it is enumeration, and simple can lose it. Could they
only learn it by making the mistake? Then it is judgment, and it stays.

Not every rule tolerates losing its rationale, and the split is not stylistic.

- MECHANICAL rules are fully enumerable and self-justifying. `run magus affected
  ci before calling the work done` determines the action on its own. Mark the
  why freely.
- JUDGMENT rules ask the reader to recognize an instance nobody enumerated.
  `never a whole-tree git op to verify a build` is one: the why (a concurrent
  agent's untracked work dies) is what lets a reader generalize to a case the
  rule never listed. Keep a short form of the why in simple via an `{{else}}`
  arm rather than dropping it.

The sharpest test is silence. A failure that ANNOUNCES itself teaches the reader
on its own and needs no rationale in simple; a failure that is silent - an edit
that stops existing, a guard that fails open, a pipe that turns a failing gate
into exit 0 - can only arrive as text, because nothing in the session will ever
say it.

The evidence, for the record: an ablation of repository context files
(arXiv:2602.11988) found imperative instructions are followed well while
background and overview prose is not worth its tokens. That licenses cutting
BACKGROUND - what magus is, why it exists - and it is not a license to cut the
why of a judgment rule. Short-context compression studies (arXiv:2505.00019,
arXiv:2502.14255) found terse rewrites degrade short instruction text, so do not
crush the grammar of what survives.

### Do not de-grammar the core

Dropping articles and connectives to save bytes is NOT the intended use of these
branches, and the measured effect on weaker models is negative. Shorten by saying
less, not by writing badly. Plain sentences, ordinary punctuation, in both arms.

Rules:

- Never put the LOAD-BEARING instruction inside `{{if .Full}}` - the one command
  or path without which simple cannot act, or the CORRECT half of a
  WRONG/CORRECT pair. Simple must still be able to do the thing.
- An EXHAUSTIVE enumeration is different, and it is exactly what simple sheds:
  every flag of a command, every kind in a table, every variant of a form. Put
  it in `{{if .Full}}` and have simple name where to get it (`-h`, `magus
  describe <thing>`, a docs URL) rather than carrying the list. That is
  progressive disclosure, and it is the intended shape - a capable reader
  fetches an enumeration far more cheaply than it recovers a judgment.
- War stories, "otherwise X" clauses, and examples that only illustrate go in
  `{{if .Full}}`. The why of a judgment rule does NOT: shorten it into an
  `{{else}}` arm instead.
- Keep the imperative grammatical after the cut. `foo{{if .Full}} - because
  bar{{end}}.` reads as `foo.` in simple; a mid-clause cut reads as damage.
- A malformed template is a parse or execute error at install, which also catches
  typos the old scheme let through as literal text.
- A passage that survives into simple at full length is a candidate for an
  `{{else}}` arm, not evidence the ceiling has been reached.
- `TestEveryEmbeddedSkillHasBothPermutations` fails for any skill whose
  permutations are byte-identical, so a skill with no marked rationale is
  caught rather than silently making `--simple` a lie for that one.

## 4. Breadcrumbs are load-bearing

magus's cross-link discipline: every surface mints a stable, resolvable ID -
tool names (toolref.go constants), CLI paths (clihint), output refs
(out1a2b3c), diagnostics (MGSxxxx), graph node IDs (kind:name). Prose that
points at another surface goes through one of those IDs so a rename breaks
the build or a test, never an agent at 2am. Hints stay terse and earned: one
line, only on an error or a result that mints something chainable. A weaker
model follows breadcrumbs it could never have planned; leave them.

## 5. Write for the weakest reader

Frontmatter descriptions carry the triggers ("Use when...", "Do NOT use
for..."). Bodies use imperative fast paths, WRONG/CORRECT pairs, and tables
over prose. Defer to `-h` and live tools for anything versionable. Plain
ASCII, no emojis (tests enforce it). Spell every rule out; a rule the reader
has to infer will be inferred differently by every model that reads it.

## 6. Record the why, then verify the whole

- Decisions with a why go to magus_memory (file=decisions) so the next
  session - possibly a lesser model - inherits them instead of re-deriving.
  Read status and decisions before re-litigating anything.
- After editing skills: `go test ./cmd/magus/` (frontmatter, ASCII,
  byte-identity, install/verify testscripts), reinstall the dogfooded copy,
  confirm `magus graph verify` says up to date, and run `magus affected ci`
  before calling the work done.
