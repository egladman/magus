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
  <a class="landing-card" href="magus-architecture-review/"><span class="landing-card-title">magus-architecture-review</span><span class="landing-card-body">Gather architecture evidence from the magus knowledge graph (package imports, layers, symbol callers, churn, ownership) instead of reading files.</span><span class="landing-card-body"><small>9.0 KB short, 12.2 KB full - 25% shorter</small></span></a>
  <a class="landing-card" href="magus-buzz-lang/"><span class="landing-card-title">magus-buzz-lang</span><span class="landing-card-body">Write, fix and debug Buzz, the statically typed language of magusfile.buzz, spells and `magus buzz` scripts.</span><span class="landing-card-body"><small>9.6 KB short, 12.5 KB full - 23% shorter</small></span></a>
  <a class="landing-card" href="magus-buzz-review/"><span class="landing-card-title">magus-buzz-review</span><span class="landing-card-body">Review Buzz code - a magusfile, a spell, or a standalone .buzz script - across three lenses run in parallel: idiom/style, skeptic/correctness, and upstream-Buzz conformance.</span><span class="landing-card-body"><small>11.3 KB short, 18.7 KB full - 39% shorter</small></span></a>
  <a class="landing-card" href="magus-change-summary/"><span class="landing-card-title">magus-change-summary</span><span class="landing-card-body">Summarize what changed in a magus workspace, write it up, or answer a granular diff question.</span><span class="landing-card-body"><small>3.5 KB short, 6.4 KB full - 45% shorter</small></span></a>
  <a class="landing-card" href="magus-commit-composition/"><span class="landing-card-title">magus-commit-composition</span><span class="landing-card-body">Restructure an UNPUSHED branch so each commit is one reviewable idea, using the workspace's own boundaries (project ownership, declared outputs, blast radius) rather than guessing from paths.</span><span class="landing-card-body"><small>2.8 KB short, 4.3 KB full - 35% shorter</small></span></a>
  <a class="landing-card" href="magus-context-audit/"><span class="landing-card-title">magus-context-audit</span><span class="landing-card-body">Audit the instructions an agent was given for statements that contradict each other or no longer match the tools.</span><span class="landing-card-body"><small>3.4 KB short, 5.8 KB full - 41% shorter</small></span></a>
  <a class="landing-card" href="magus-diagram/"><span class="landing-card-title">magus-diagram</span><span class="landing-card-body">Write, check and view an architecture figure with magus/figure, the embedded Buzz module.</span><span class="landing-card-body"><small>5.6 KB short, 8.0 KB full - 29% shorter</small></span></a>
  <a class="landing-card" href="magus-multi-agent/"><span class="landing-card-title">magus-multi-agent</span><span class="landing-card-body">Load BEFORE your first subagent spawn in a magus workspace: an Agent or Task tool call, a background worker, parallel workers, fanning out, or delegating part of a task.</span><span class="landing-card-body"><small>20.8 KB short, 27.7 KB full - 24% shorter</small></span></a>
  <a class="landing-card" href="magus-query/"><span class="landing-card-title">magus-query</span><span class="landing-card-body">Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs).</span><span class="landing-card-body"><small>9.0 KB short, 15.4 KB full - 41% shorter</small></span></a>
  <a class="landing-card" href="magus-run/"><span class="landing-card-title">magus-run</span><span class="landing-card-body">Run builds, tests, lints, and codegen through magus targets.</span><span class="landing-card-body"><small>7.7 KB short, 14.2 KB full - 45% shorter</small></span></a>
  <a class="landing-card" href="magus-sdk/"><span class="landing-card-title">magus-sdk</span><span class="landing-card-body">Help a Go developer consume magus as a library (import &quot;github.com/egladman/magus&quot;) instead of shelling out to the CLI, and audit whether the SDK actually serves them.</span><span class="landing-card-body"><small>9.9 KB short, 13.7 KB full - 27% shorter</small></span></a>
  <a class="landing-card" href="magus-test-design/"><span class="landing-card-title">magus-test-design</span><span class="landing-card-body">Choose unit, integration, or end-to-end test boundaries from the magus graph and runtime behavior.</span><span class="landing-card-body"><small>5.3 KB short, 10.2 KB full - 48% shorter</small></span></a>
  <a class="landing-card" href="magus-upstream-docs/"><span class="landing-card-title">magus-upstream-docs</span><span class="landing-card-body">Traverse magus's own documentation to answer a &quot;how does magus do X / what does Y mean / where is Z documented&quot; question, instead of guessing an answer or a URL.</span><span class="landing-card-body"><small>2.9 KB short, 5.0 KB full - 43% shorter</small></span></a>
  <a class="landing-card" href="magus-upstream-source/"><span class="landing-card-title">magus-upstream-source</span><span class="landing-card-body">Last resort: read magus's OWN source code at the exact commit of the magus binary in use, to trace behavior its docs cannot explain.</span><span class="landing-card-body"><small>1.8 KB short, 3.1 KB full - 42% shorter</small></span></a>
  <a class="landing-card" href="magus-vcs-hygiene/"><span class="landing-card-title">magus-vcs-hygiene</span><span class="landing-card-body">Safe version-control operations in a magus workspace (any repo with magusfile.buzz at the root).</span><span class="landing-card-body"><small>6.1 KB short, 10.9 KB full - 43% shorter</small></span></a>
  <a class="landing-card" href="magus-workspace-rules/"><span class="landing-card-title">magus-workspace-rules</span><span class="landing-card-body">Adapt magus's installed agent integration to THIS workspace without breaking it.</span><span class="landing-card-body"><small>8.3 KB short, 12.3 KB full - 32% shorter</small></span></a>
</div>

All 16 together are 117.0 KB installed as the short form, against 180.3 KB for the full twins: 35% less always-loaded text.
