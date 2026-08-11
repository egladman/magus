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
| [magus-adapt](magus-adapt.md) | 5436 | 4392 | 19% | Adapt magus's installed agent surface to THIS workspace without breaking it. |
| [magus-architecture](magus-architecture.md) | 6322 | 5123 | 18% | Ground refactoring and structure proposals in the magus knowledge graph instead of intuition. |
| [magus-buzz](magus-buzz.md) | 7631 | 6431 | 15% | Write and run Buzz, the language magusfiles, spells, and `magus buzz` scripts are written in. |
| [magus-buzz-review](magus-buzz-review.md) | 17211 | 13490 | 21% | Review Buzz code - a magusfile, a spell, or a standalone .buzz script - across three lenses run in parallel: idiom/style, skeptic/correctness, and upstream-Buzz conformance. |
| [magus-changes](magus-changes.md) | 5133 | 3911 | 23% | Summarize what changed in a magus workspace, write it up, or answer a granular diff question. |
| [magus-context-audit](magus-context-audit.md) | 5537 | 4029 | 27% | Audit the instructions an agent was given - the repo instruction file, installed skills, handoff-journal entries, a routing index, hook-injected text, and any user-level instruction file - for statements that contradict each other or that no longer match what the tools do. |
| [magus-delegate-ultra](magus-delegate-ultra.md) | 8469 | 6526 | 22% | Plan and execute potentially expensive multi-agent work in a magus workspace as an acceptance-criteria loop, using affected shard plans and knowledge-graph evidence to assign collision-resistant edit units, coordinate nested delegation, and choose cost-appropriate effort tiers. |
| [magus-docs](magus-docs.md) | 3670 | 2956 | 19% | Traverse magus's own documentation to answer a "how does magus do X / what does Y mean / where is Z documented" question, instead of guessing an answer or a URL. |
| [magus-memory](magus-memory.md) | 3831 | 3156 | 17% | Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, and pointers that survive worktrees and sessions. |
| [magus-query](magus-query.md) | 7992 | 5782 | 27% | Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs). |
| [magus-run](magus-run.md) | 9375 | 5947 | 36% | Run builds, tests, lints, and codegen through magus targets. |
| [magus-sdk](magus-sdk.md) | 13316 | 12879 | 3% | Help a Go developer consume magus as a library (import "github.com/egladman/magus") instead of shelling out to the CLI, and audit whether the SDK actually serves them. |
| [magus-vcs](magus-vcs.md) | 6848 | 5069 | 25% | Safe git operations in a magus workspace (any repo with magusfile.buzz at the root). |
| **all 13** | **100771** | **79691** | **20%** | |
