---
title: Agent skills
description: "Every skill magus installs, in both curated forms, generated from the embedded bodies."
tags: [agents, skills, reference]
page_type: overview
---

# Agent skills

These are the skills `magus agent install` writes, reproduced verbatim from the
bodies embedded in the binary. Each ships in two hand-authored forms, and
install writes both: the short form is the always-loaded primary, and the full form
is its `<name>-full` twin, loaded by name when a reader needs the rationale.
See [Skills](../../guides/integrations/agents/skills.md) for the difference.

<div class="grid landing-cards">
  <a class="landing-card" href="magus-architecture-review/"><span class="landing-card-title">magus-architecture-review</span><span class="landing-card-body">Ground refactoring and structure proposals in the magus knowledge graph instead of intuition.</span><span class="landing-card-body"><small>5.4 KB short, 6.7 KB full - 19% shorter</small></span></a>
  <a class="landing-card" href="magus-buzz-review/"><span class="landing-card-title">magus-buzz-review</span><span class="landing-card-body">Review Buzz code - a magusfile, a spell, or a standalone .buzz script - across three lenses run in parallel: idiom/style, skeptic/correctness, and upstream-Buzz conformance.</span><span class="landing-card-body"><small>14.8 KB short, 19.8 KB full - 25% shorter</small></span></a>
  <a class="landing-card" href="magus-buzz-write/"><span class="landing-card-title">magus-buzz-write</span><span class="landing-card-body">Write and run Buzz, the language magusfiles, spells, and `magus buzz` scripts are written in.</span><span class="landing-card-body"><small>6.8 KB short, 8.2 KB full - 16% shorter</small></span></a>
  <a class="landing-card" href="magus-change-summary/"><span class="landing-card-title">magus-change-summary</span><span class="landing-card-body">Summarize what changed in a magus workspace, write it up, or answer a granular diff question.</span><span class="landing-card-body"><small>5.3 KB short, 6.9 KB full - 23% shorter</small></span></a>
  <a class="landing-card" href="magus-commit-composition/"><span class="landing-card-title">magus-commit-composition</span><span class="landing-card-body">Restructure an UNPUSHED branch so each commit is one reviewable idea, using the workspace's own boundaries (project ownership, declared outputs, blast radius) rather than guessing from paths.</span><span class="landing-card-body"><small>3.7 KB short, 4.3 KB full - 13% shorter</small></span></a>
  <a class="landing-card" href="magus-context-audit/"><span class="landing-card-title">magus-context-audit</span><span class="landing-card-body">Audit the instructions an agent was given - the repo instruction file, installed skills, memory entries, a routing index, hook-injected text, and any user-level instruction file - for statements that contradict each other or that no longer match what the tools do.</span><span class="landing-card-body"><small>4.1 KB short, 5.7 KB full - 28% shorter</small></span></a>
  <a class="landing-card" href="magus-docs-lookup/"><span class="landing-card-title">magus-docs-lookup</span><span class="landing-card-body">Traverse magus's own documentation to answer a &quot;how does magus do X / what does Y mean / where is Z documented&quot; question, instead of guessing an answer or a URL.</span><span class="landing-card-body"><small>3.4 KB short, 4.2 KB full - 19% shorter</small></span></a>
  <a class="landing-card" href="magus-memory/"><span class="landing-card-title">magus-memory</span><span class="landing-card-body">Maintain a user-owned per-repository memory through magus_memory or `magus memory`: named decisions, plans, pointers, and the hypotheses an investigation ruled out, all surviving worktrees and sessions.</span><span class="landing-card-body"><small>4.5 KB short, 5.4 KB full - 17% shorter</small></span></a>
  <a class="landing-card" href="magus-multi-agent/"><span class="landing-card-title">magus-multi-agent</span><span class="landing-card-body">Split work across agents in a magus workspace as an acceptance-criteria loop: partition by WRITE SET using graph evidence (magus refs --occurrences, explain, affected --plan --stdin), prove the leases cannot collide, bound fan-out depth, and match each lease's model to the work it needs.</span><span class="landing-card-body"><small>19.8 KB short, 27.8 KB full - 28% shorter</small></span></a>
  <a class="landing-card" href="magus-query/"><span class="landing-card-title">magus-query</span><span class="landing-card-body">Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs).</span><span class="landing-card-body"><small>10.6 KB short, 13.1 KB full - 18% shorter</small></span></a>
  <a class="landing-card" href="magus-run/"><span class="landing-card-title">magus-run</span><span class="landing-card-body">Run builds, tests, lints, and codegen through magus targets.</span><span class="landing-card-body"><small>7.6 KB short, 11.5 KB full - 33% shorter</small></span></a>
  <a class="landing-card" href="magus-sdk/"><span class="landing-card-title">magus-sdk</span><span class="landing-card-body">Help a Go developer consume magus as a library (import &quot;github.com/egladman/magus&quot;) instead of shelling out to the CLI, and audit whether the SDK actually serves them.</span><span class="landing-card-body"><small>12.6 KB short, 13.0 KB full - 3% shorter</small></span></a>
  <a class="landing-card" href="magus-test-design/"><span class="landing-card-title">magus-test-design</span><span class="landing-card-body">Choose unit, integration, or end-to-end test boundaries from the magus graph and runtime behavior.</span><span class="landing-card-body"><small>6.5 KB short, 10.3 KB full - 36% shorter</small></span></a>
  <a class="landing-card" href="magus-vcs-hygiene/"><span class="landing-card-title">magus-vcs-hygiene</span><span class="landing-card-body">Safe version-control operations in a magus workspace (any repo with magusfile.buzz at the root).</span><span class="landing-card-body"><small>6.8 KB short, 9.6 KB full - 28% shorter</small></span></a>
  <a class="landing-card" href="magus-workspace-rules/"><span class="landing-card-title">magus-workspace-rules</span><span class="landing-card-body">Adapt magus's installed agent surface to THIS workspace without breaking it.</span><span class="landing-card-body"><small>4.3 KB short, 5.3 KB full - 19% shorter</small></span></a>
</div>

All 15 together are 116.1 KB installed as the short form, against 151.6 KB for the full twins: 23% less always-loaded text.
