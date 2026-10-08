---
name: magus-skill-authoring
description: "The working method for building and maintaining magus's agent surface in THIS repo: the embedded skills, MCP tools, hints, and MAGUS.md routing. Use when editing anything under internal/agent/skills/, the MCP registry, agent install, or when evaluating what agents can and cannot learn from magus. This skill is hand-authored and committed; it is NOT part of the installed set and never ships in the binary."
---

# Authoring the agent surface

This is the working method behind the magus skills, so any model, strong or weak,
maintains them the same way. The skills exist to stop agents guessing, so the
process that writes them cannot guess either.

## 1. Empiricism before documentation

Never teach behavior you have not executed against a freshly built binary in this
session:

1. Build HEAD (`magus run go-build .`).
2. Start the server.
3. Call the actual tool, over MCP HTTP and the CLI.
4. Paste the observed output into your analysis before writing any skill text.

Precedent: the registry advertised dry_run as "print what would run without
executing". The verified reality was zero bytes of output AND regenerated files on
disk. A skill written from the docs would have taught a "safe preview" that
silently mutates the tree.

## 2. Hunt the silent failure

Empty output, zero matches, and exit 1 with no text are findings, not
inconveniences. Probe every claim adversarially before teaching it.

When `project:docs kind:function render` returned 0, the wrong response was a
workaround in the skill. The right one traced the scorer, fixed the filter, and
added a regression test. Fix the tool before teaching the workaround. When the fix
is out of reach, teach ONLY verified idioms and file the gap where a reader finds
it (the plans doc, a task, the harness memory).

## 3. One source of truth, drift-gated

- The installed skills are generated: embedded in the binary, stamped with
  agentSkillVersion + knowledge schema version, graded by `magus doctor`'s
  agent-skills check. Never hand-edit an installed copy; edit
  internal/agent/skills/ and re-run `magus agent install <dest-dir> --force`
  (here: .claude/skills).
- Every destination receives identical bytes (a test asserts it). magus is
  agent-host agnostic: no host name appears in code. Host-specific glue (hook
  event shapes, config dialects) is documentation over the neutral surfaces:
  explicit install destinations, the agent hook verdict, --from-json
  extraction, -o template rendering. Never a per-host code path.
- Any change to skill content or the tool surface it documents bumps
  agentSkillVersion with a changelog line.
- Skills teach the stable HOW; the workspace WHAT lives in MAGUS.md and the live
  tools. A skill that mentions this repo's specifics is a bug.

## 3b. Two forms from one body: mark the why, then shorten the rest

A skill body is a `text/template` rendered against the variant, so a form is an
ordinary `if` action. The whole vocabulary is three branching constructs plus the
registry lookups in section 3c:

```markdown
Run the target first{{if .Full}}, because a raw tool bypasses the cache{{end}}.

{{if .Short}}Full explains this at length below.{{end}}

Read `llms.txt` first{{if .Full}}, because guessing a URL wastes a fetch and the
index is authoritative{{else}}: it is the index{{end}}.
```

Unconditional text is in both forms.

- No else arm means "full says more here".
- An else arm means "both forms say this, at different lengths". It is the ONLY
  construct that shortens something both must express.
- A bare `.Short` arm means "short says this and full says nothing". That is
  almost always a mistake worth catching in review.

Measured 2026-07-31: the ten shipped skills had 137 full-only branches and only 28
else arms. Most distinctions are still deletion, not rewording. Reach for an else
arm whenever a passage survives into short at full length.

A third form costs a constant, not a new markup convention:

```markdown
{{if .Is "minimal"}}bare imperative{{else if .Full}}the long version{{else}}the short one{{end}}
```

### 3c. Never type a command path; resolve it

A skill is read in someone else's repo, where nobody can check that a named command
still exists. A retyped path is the one error that reaches its reader intact and
stays wrong. Resolve it instead:

```markdown
Run `{{cmd "agent harness verify"}}`, then read what it reports.
```

`cmd` looks the path up in `internal/hint`'s `AllCommands` and fails the INSTALL on
a miss, naming the path. A renamed verb either updates every skill that mentions it
or stops the build. The failure lands on whoever moved the command, not on an agent
a month later.

Register the command in `internal/hint/cli_command.go` first; an unregistered path
is a lookup failure, not a silent pass. It always renders the PATH spelling
(`magus ...`), never this process's `./magus`: the reader's checkout is not ours.

The whole vocabulary, each resolving against the registry that defines it:

| write | renders | resolves against |
| --- | --- | --- |
| `{{cmd "agent harness verify"}}` | `magus agent harness verify` | `hint.AllCommands` |
| `{{tool "client"}}` | `client` | `hint.AllToolNames` (a `magus_` prefix is tried when the bare name misses) |
| `{{skill "vcs-hygiene"}}` | `magus-vcs-hygiene` | the shipped catalog |
| `{{buzz "harness.provider"}}` | `magus\harness.provider` | the magus host module |
| `{{mgs "MGS2001"}}` | `MGS2001` | the diagnostic registry |
| `{{mgslink "MGS2001"}}` | a markdown link to its docs | the diagnostic registry |

Each key is the SHORT form and each output the full one, so the call is never the
answer retyped. The two diagnostic functions split by job:

- A code inside a graph node id, a URL pattern, or a quoted literal is DATA: use
  `mgs`.
- A code a sentence cites takes `mgslink`. The URL's category segment follows the
  code's range, so no reader can derive it from the digits.

The `skill` lookup resolves only skills magus SHIPS. A local skill name
(`magus-local-development`) stays literal on purpose: magus does not install it,
so there is nothing to check it against.

A new function in `skillFuncs` inherits the obligation `validateActionPipe`
documents: it renders the same text in both forms. Otherwise the two forms stop
describing one behavior and nothing catches it.

### Showing template syntax inside a skill

The body IS a template, fenced code blocks included. A skill that documents template
syntax must escape it as a string constant. magus-run documents `-o template` and
magus-buzz-lang documents mustache; both hit this:

```markdown
`-o template='{{"{{.Field}}"}}'`
```

Getting it wrong fails loudly at install (a parse error for an unknown function, an
execute error for an unknown field), never as a silently mangled file.

### Who the short form is for, and therefore what it cuts

Short is not the beginner form. It is installed for the most capable readers,
which can re-derive an imperative from the tool surface. So **short sheds
ENUMERATION and keeps JUDGMENT.** It is not "the steps without the why". Dropping
the why hands the strongest reader the half it could reconstruct and takes away
the half it could not.

Ask of every branch: could a capable reader work this out from `magus describe`,
`-h`, or the docs? Then it is enumeration, and short can lose it. Could they only
learn it by making the mistake? Then it is judgment, and it stays.

Not every rule tolerates losing its rationale:

- MECHANICAL rules are enumerable and self-justifying. `run magus affected ci
  before calling the work done` determines the action alone. Mark the why freely.
- JUDGMENT rules ask the reader to recognize an instance nobody enumerated.
  `never a whole-tree git op to verify a build` is one: its why (a concurrent
  agent's untracked work dies) lets a reader generalize. Keep a terse why in short
  via an else arm instead of dropping it.

The sharpest test is silence. A failure that ANNOUNCES itself teaches the reader and
needs no rationale in short. A silent failure can only arrive as text: an edit that
stops existing, a guard that fails open, a pipe that turns a failing gate into exit
0. Nothing in the session ever says it.

The evidence: an ablation of repository context files (arXiv:2602.11988) found
imperative instructions followed well, while background and overview prose is not
worth its tokens. That licenses cutting BACKGROUND (what magus is, why it exists),
not the why of a judgment rule. Short-context compression studies
(arXiv:2505.00019, arXiv:2502.14255) found terse rewrites degrade short instruction
text, so keep the grammar of what survives.

### Write the short form terse, not de-grammared

Shorten by saying less, not by writing badly. Dropping articles and connectives to
save bytes measurably hurts weaker models. Write plain sentences with ordinary
punctuation in both arms.

The prose judge holds what the short form shows to the terse rules. Its `skill-source`
and `skill` surfaces, run by hack/lint/markdown-prose.buzz, refuse a sentence over 25
words, a paragraph or list item over 60, and a wordy phrase (`in order to`, `is able
to`). Write short declarative sentences and imperative steps. Prefer a list
when steps are a sequence. Never restate what a heading says.

Rules:

- Never put the LOAD-BEARING instruction inside a `.Full` arm: the one command or
  path short cannot act without, or the CORRECT half of a WRONG/CORRECT pair.
  Short must still do the thing.
- An EXHAUSTIVE enumeration is exactly what short sheds: every flag of a command,
  every kind in a table, every variant of a form. Put it in a `.Full` arm and have
  short name where to get it (`-h`, `magus describe <thing>`, a docs URL). That is
  progressive disclosure: a capable reader fetches an enumeration far more cheaply
  than it recovers a judgment.
- War stories, "otherwise X" clauses, and illustrative examples go in a `.Full`
  arm. The why of a judgment rule does NOT: shorten it into an else arm.
- Keep the imperative grammatical after the cut. `foo{{if .Full}}, because
  bar{{end}}.` reads as `foo.` in short; a mid-clause cut reads as damage.
- A malformed template is a parse or execute error at install, which also catches
  typos the old scheme let through as literal text.
- A passage that survives into short at full length is a candidate for an else
  arm, not proof the ceiling is reached.
- `TestEveryEmbeddedSkillHasBothForms` fails for any skill whose two forms are
  byte-identical, so a skill with no marked rationale is caught. `--skill-form`
  picks what an install writes: `both` (the default: the short body under each
  skill's own name plus a `<name>-full` twin), `short`, or `full`.

## 4. Breadcrumbs are load-bearing

Every surface mints a stable, resolvable ID: tool names (internal/hint ToolName
constants), CLI paths (internal/hint Command values), output refs (out1a2b3c),
diagnostics (MGSxxxx), graph node IDs (kind:name). Prose that points at another
surface goes through one of those IDs, so a rename breaks the build or a test,
never an agent at 2am.

Hints stay terse and earned: one line, only on an error or on a result that mints
something chainable. A weaker model follows breadcrumbs it could never have
planned; leave them.

## 5. Write for the weakest reader

- Frontmatter descriptions carry the triggers ("Use when...", "Do NOT use
  for...").
- Bodies use imperative fast paths, WRONG/CORRECT pairs, and tables over prose.
- Defer to `-h` and live tools for anything versionable.
- Plain ASCII, no emojis (tests enforce it).
- Spell every rule out: a rule the reader must infer is inferred differently by
  every model.

## 5b. Phrase verification as proof, not as care

An instruction phrased as care is satisfiable by prose: an agent asserts it was
careful and the sentence is met. Phrased as a proof obligation, it can only be met
by evidence, because it names the artifact that settles it. When the evidence is
cheap, write the obligation.

Worked example, from magus-vcs-hygiene:

```markdown
WRONG: Distinguish real drift from environmental noise before you act.
CORRECT: Prove drift by regenerating a SECOND time, never by reading the diff
and judging it.
```

Both point at the same procedure. Only the second fails visibly when nobody runs
it: a second regeneration is in the transcript or it is not. Apply it to:

- gates: "show a gate you added FAILING before you trust its green";
- collision claims: "the check REPORTS the write sets disjoint", not "the leases
  are genuinely independent";
- reported findings: "carries the command that reproduces it".

The limit is cost. A judgment rule with no cheap proof keeps its judgment framing.
`never a whole-tree git op to verify a build` has nothing to run; a fake obligation
would trade a rule the reader can generalize for a ritual.

## 6. Record the why, then verify the whole

- Decisions with a why go to the harness memory, so the next session, possibly a
  lesser model, inherits them. Read them before re-litigating anything.
- After editing skills, in this order:
  1. `magus run go-build .`: the bodies are go:embed'd, so nothing below reads
     your edit until the binary carries it.
  2. `magus run go::go-test . --silent -- -run 'TestAgent|TestSkill' ./cmd/magus/`
     (frontmatter, ASCII, byte-identity, install/verify testscripts). The raw
     `go test ./cmd/magus/` is guard-denied. magus flags go BEFORE the `--`;
     everything after it forwards to the test binary.
  3. `./magus agent install .claude/skills --force`: reinstall the dogfooded
     copies, which are stamped and otherwise read as drift.
  4. Refresh the AGENTS.md managed block. `./magus agent starter` prints the
     current block (so does `agent install`). Replace everything between the
     `magus:skills:begin` and `magus:skills:end` markers with it and leave the rest
     alone. magus never writes AGENTS.md; `magus doctor`'s agent-skills advice
     names the stale block.
  5. `./magus doctor` says up to date. `magus doctor --fix` runs the remedy each
     finding names, where one exists.
  6. `magus affected ci --no-default-charms` before calling the work done.
