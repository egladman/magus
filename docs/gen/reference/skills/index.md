---
title: Agent skills
description: "Every skill magus installs, in both curated permutations, generated from the embedded bodies."
tags: [agents, skills, reference]
page_type: overview
---

# Agent skills

These are the skills `magus agent install` writes, reproduced verbatim from the
bodies embedded in the binary. Each ships in two hand-authored permutations: the
default carries the rationale behind each step, and `--simple` withholds it.
See [Agents](../../guides/integrations/agents.md) for how to choose.

| skill | full | short | saved | what it is for |
| --- | --- | --- | --- | --- |
| [magus-architecture](magus-architecture.md) | 4510 | 3967 | 12% | Ground refactoring and structure proposals in the magus knowledge graph instead of intuition. |
| [magus-buzz](magus-buzz.md) | 6148 | 5321 | 13% | Write and run Buzz, the language magusfiles, spells, and `magus buzz` scripts are written in. |
| [magus-changes](magus-changes.md) | 2671 | 2446 | 8% | Summarize what merged, changed, or landed recently in a magus workspace. |
| [magus-docs](magus-docs.md) | 3670 | 3012 | 17% | Traverse magus's own documentation to answer a "how does magus do X / what does Y mean / where is Z documented" question, instead of guessing an answer or a URL. |
| [magus-memory](magus-memory.md) | 3845 | 3190 | 17% | Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, and pointers that survive worktrees and sessions. |
| [magus-query](magus-query.md) | 7785 | 6593 | 15% | Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs). |
| [magus-run](magus-run.md) | 8164 | 6630 | 18% | Run builds, tests, lints, and codegen through magus targets. |
| [magus-vcs](magus-vcs.md) | 4723 | 3097 | 34% | Safe git operations in a magus workspace (any repo with magusfile.buzz at the root). |
| **all 8** | **41516** | **34256** | **17%** | |
