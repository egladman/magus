---
name: idiomatic-adrs
description: "Write and consult architecture decision records (ADRs): what was built, what was not, and why. Use when a design decision lands or a plan becomes one. Use before reopening a settled design. Use when an ADR's State rows drift from the code. Fire on plan, design doc or write this up. Repo-only; its template and lint win."
metadata:
  source: workspace
---

# Writing and consulting decision records

The template, front matter, section order, State vocabulary and filenames belong to
[docs/conventions.md](../../../docs/conventions.md#architecture-decision-records) and the
`adr-template` lint (`magus run conventions docs`). Follow them and let the lint teach
the form. This skill covers what a lint cannot see: whether the record is true and
whether a later reader can trust it.

## Consult before you propose

Settled decisions are only read when someone looks. Before proposing a flag, lint, check
or feature, or reopening a removed one:

1. Search the records: `magus query "kind=docsection id=~decisions/ <terms>"`.
2. Read the matching Alternatives and `not built` rows, and the refusal rows in
   [docs/design/refusals.md](../../../docs/design/refusals.md).
3. Cite the number in your plan. Reopen a decision only with evidence it did not have.

## Decide whether to write one

- Write one when a decision lands that someone may be tempted to reverse.
- Write one when a plan or research thread reaches a decision.
- Write one when a pull request description would otherwise carry the rationale.
- Do not write one for research with no decision, or a choice that lives and dies in one
  pull request.
- A measured decision not to build, with no built consequence, is one row in
  the record of refusals. Link it to an ADR when one decision has several.

## What a record owes its reader

- **Context** states the forces with measurements from this tree, not adjectives.
- **Decision** says what was built and in what state, one State per item.
- Every rejection carries four things: the option, why it lost, the evidence, and what
  would reopen it. A pointer to a table is not an Alternatives section.
- **Quote the evidence.** A number needs its sample and its date. "Slow" is not evidence.
- **Name the unknowns** under Open questions instead of deciding them quietly.

## Check the decision against the design principles

Hold every record's decision against the principles in
[the design principles](../../../docs/design.md) before accepting it. This step is
required. Read the principles, then add a `### Principles checked` H3 under Decision.
Link each principle the decision relies on or departs from, with one sentence on how.

- A departure names the principle, what bends and why, and who decided. A decision that
  contradicts a principle without saying so is the defect this step exists to catch.
- A record that touches an agent integration cites
  [The outer harness stays outside](../../../docs/design.md). Magus judges and informs.
  It never transforms what the host's inner loop sends, and agent-loaded text stays static
  and version-controlled. That principle is on an unmerged branch
  (`feat/proofread-library-improvements-87c508`, commit 94344b71c): cite it as pending until
  it lands in docs/design/, then link its page.
- Principles the decision does not touch need no mention.

## Keep it true

- Amend, do not rewrite. A correction is a dated H3 under Amendments, and the original
  text stays so a reader sees what changed and why.
- Flip the State in the same commit that lands the code. When you touch code an ADR
  describes, re-read its State rows and fix the ones that rotted.
- Prove a State by the artifact: the path exists, the command runs, the test passes.
- Take the number at merge time. Check the base branch for the next free one, because
  parallel branches both take the same number. Renumber the file, title, `order` and every
  link to it.
