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
| [magus-architecture](magus-architecture.md) | 6278 | 5031 | 19% | Ground refactoring and structure proposals in the magus knowledge graph instead of intuition. |
| [magus-buzz](magus-buzz.md) | 6233 | 5265 | 15% | Write and run Buzz, the language magusfiles, spells, and `magus buzz` scripts are written in. |
| [magus-changes](magus-changes.md) | 5134 | 3864 | 24% | Summarize what changed in a magus workspace, write it up, or answer a granular diff question. |
| [magus-context-audit](magus-context-audit.md) | 5165 | 3681 | 28% | Audit the instructions an agent was given - the repo instruction file, installed skills, handoff-journal entries, a routing index, hook-injected text, and any user-level instruction file - for statements that contradict each other or that no longer match what the tools do. |
| [magus-delegate-ultra](magus-delegate-ultra.md) | 8447 | 6404 | 24% | Plan and execute potentially expensive multi-agent work in a magus workspace as an acceptance-criteria loop, using affected shard plans and knowledge-graph evidence to assign collision-resistant edit units, coordinate nested delegation, and choose cost-appropriate effort tiers. |
| [magus-docs](magus-docs.md) | 3670 | 2917 | 20% | Traverse magus's own documentation to answer a "how does magus do X / what does Y mean / where is Z documented" question, instead of guessing an answer or a URL. |
| [magus-memory](magus-memory.md) | 3843 | 3165 | 17% | Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, and pointers that survive worktrees and sessions. |
| [magus-query](magus-query.md) | 7785 | 5576 | 28% | Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs). |
| [magus-run](magus-run.md) | 8912 | 5141 | 42% | Run builds, tests, lints, and codegen through magus targets. |
| [magus-sdk](magus-sdk.md) | 13316 | 12650 | 5% | Help a Go developer consume magus as a library (import "github.com/egladman/magus") instead of shelling out to the CLI, and audit whether the SDK actually serves them. |
| [magus-vcs](magus-vcs.md) | 5371 | 3447 | 35% | Safe git operations in a magus workspace (any repo with magusfile.buzz at the root). |
| **all 11** | **74154** | **57141** | **22%** | |
