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
this session. Build HEAD (`go build -o /tmp/magus ./cmd/magus`), start the
daemon, call the actual tool (over MCP HTTP as well as the CLI), and paste the
observed output into your analysis before writing a word of skill text.

Cautionary precedent, found here: the registry advertised dry_run as "print
what would run without executing" - the verified reality was zero bytes of
output AND regenerated files on disk. A skill written from the docs would
have taught agents a "safe preview" that silently mutates the tree.

## 2. Hunt the silent failure

Empty output, zero matches, and exit 1 with no text are findings, not
inconveniences. Probe every claim adversarially before teaching it: when
`project:website kind:function render` returned 0, the wrong response was a
workaround in the skill; the right response was tracing the scorer, fixing
the filter, and adding a regression test. Fix the tool before teaching the
workaround. When the fix is out of reach, teach ONLY verified idioms and file
the gap where it will be found (the plans doc, a task, magus_memory).

## 3. One source of truth, drift-gated

- The installed skills are generated: embedded in the binary, stamped with
  agentSkillVersion + knowledge schema version, verified by `magus graph
  verify`. Never hand-edit an installed copy; edit cmd/magus/skills/ and
  re-run `magus agent install <platform> --force`.
- Every platform receives identical bytes (a test asserts it). Supporting a
  new platform means adding a destination, never forking content.
- Any change to skill content or the tool surface it documents bumps
  agentSkillVersion with a changelog line.
- Skills teach the stable HOW; the workspace WHAT lives in MAGUS.md and the
  live tools. A skill that mentions this repo's specifics is a bug.

## 4. Breadcrumbs are load-bearing

magus's cross-link discipline: every surface mints a stable, resolvable ID -
tool names (toolref.go constants), CLI paths (clihint), output refs
(ref1a2b3c), diagnostics (MGSxxxx), graph node IDs (kind:name). Prose that
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
