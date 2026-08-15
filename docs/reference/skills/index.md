---
title: Agent skills
description: "Every skill magus installs, in both curated permutations, generated from the embedded bodies."
tags: [agents, skills, reference]
page_type: overview
---

# Agent skills

These are the skills `magus agent install` writes, reproduced verbatim from the
bodies embedded in the binary. Each ships in two hand-authored permutations, and
install writes both: the short form is the always-loaded primary, and the full form
is its `<name>-full` twin, loaded by name when a reader needs the rationale.
See [Skills](../../guides/integrations/agents/skills.md) for the difference.

| skill | full | short | saved | what it is for |
| --- | --- | --- | --- | --- |
| [magus-architecture-review](magus-architecture-review.md) | 6322 | 5123 | 18% | Ground refactoring and structure proposals in the magus knowledge graph instead of intuition. |
| [magus-buzz-review](magus-buzz-review.md) | 20279 | 15155 | 25% | Review Buzz code - a magusfile, a spell, or a standalone .buzz script - across three lenses run in parallel: idiom/style, skeptic/correctness, and upstream-Buzz conformance. |
| [magus-buzz-write](magus-buzz-write.md) | 8168 | 6770 | 17% | Write and run Buzz, the language magusfiles, spells, and `magus buzz` scripts are written in. |
| [magus-change-summary](magus-change-summary.md) | 5137 | 3916 | 23% | Summarize what changed in a magus workspace, write it up, or answer a granular diff question. |
| [magus-context-audit](magus-context-audit.md) | 5537 | 4029 | 27% | Audit the instructions an agent was given - the repo instruction file, installed skills, handoff-journal entries, a routing index, hook-injected text, and any user-level instruction file - for statements that contradict each other or that no longer match what the tools do. |
| [magus-delegate-multi-agent](magus-delegate-multi-agent.md) | 13707 | 10488 | 23% | Split work across agents in a magus workspace as an acceptance-criteria loop: partition by WRITE SET using graph evidence (magus refs --occurrences, explain, affected --plan --stdin), prove the units cannot collide, bound fan-out depth, and match each unit's model to the work it needs. |
| [magus-docs-lookup](magus-docs-lookup.md) | 3670 | 2956 | 19% | Traverse magus's own documentation to answer a "how does magus do X / what does Y mean / where is Z documented" question, instead of guessing an answer or a URL. |
| [magus-handoff-journal](magus-handoff-journal.md) | 4178 | 3506 | 16% | Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, and pointers that survive worktrees and sessions. |
| [magus-query](magus-query.md) | 10607 | 8397 | 20% | Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs). |
| [magus-run](magus-run.md) | 9375 | 5947 | 36% | Run builds, tests, lints, and codegen through magus targets. |
| [magus-sdk](magus-sdk.md) | 13323 | 12886 | 3% | Help a Go developer consume magus as a library (import "github.com/egladman/magus") instead of shelling out to the CLI, and audit whether the SDK actually serves them. |
| [magus-vcs-hygiene](magus-vcs-hygiene.md) | 7222 | 5265 | 27% | Safe git operations in a magus workspace (any repo with magusfile.buzz at the root). |
| [magus-workspace-rules](magus-workspace-rules.md) | 5436 | 4392 | 19% | Adapt magus's installed agent surface to THIS workspace without breaking it. |
| **all 13** | **112961** | **88830** | **21%** | |
