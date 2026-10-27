---
title: Corrective competence
order: 12
description: The skill this section protects is your ability to fix the thing yourself when the agent is wrong, or gone.
tags: [design, judgment, agents, learning]
---

# Corrective competence

The skill this section protects is narrow: your ability to fix the thing yourself
when the agent is wrong, or gone. Not speed, and not thinking in general.

Two studies set the target. Bastani et al. measured nearly a thousand high school
students with a tutor that handed over answers: 48% better on practice while it was
available and 17% worse on the exam once it was taken away, against 127% better and
no measurable residual harm for a version prompted to give hints instead.[^bastani-2025]
Sankaranarayanan found that 77% of novice programmers given unrestricted AI failed a
30-minute maintenance task once the assistant was withdrawn, against 39% of a group
whose tool made them explain generated code's causal logic before it would integrate
it. The two groups produced equally functional work; the explaining group took about
a third longer to do it.[^sankaranarayanan-2026]

This repository is built with agents at scale: 113,430 agent-run shell commands
in twenty-one days. Our own oversight lapsed too. In 22 of 203 recent sessions
the guard was not running, and those sessions carried 17% of all commands. The
only reason that number exists is that we went looking for it.[^agent-commands-2026-09]
Whether the agent integration earns its cost is measured the same way rather than
asserted: [Benchmarks](../../benchmarks.md) publishes every run beside the
environment that produced it, controls first.

## Enforced by

- Notes are human-authored by construction.
- `--ack` refuses without a terminal, agent hosts are denied it outright, and
  the count is shown to nobody but the reader.
- The guard denies with a remedy the caller can run, since advice that only
  prints is routed around ([ADR 0013](../../decisions/0013-the-guard-denies.md)).
- magus never calls a model, which is what makes a Friday without an agent an
  option rather than a slogan.

## Good

The tool hands back the toolchain call it made, so the person can run and read
it without magus:

```sh
magus query output <ref>
```

## Bad

```text
Fixed 3 failing tests. All checks pass.
```

The person learns that something was fixed and nothing about what broke, so
the next time the agent is wrong or gone, they start from zero.

## See also

- [The loop keeps you able to notice](the-loop-keeps-you-able-to-notice.md)
- [Automation you can interrogate](automation-you-can-interrogate.md)
- [Benchmarks](../../benchmarks.md)

[^bastani-2025]: Hamsa Bastani, Osbert Bastani, Alp Sungu, Haosen Ge, Ozge Kabakci, Rei Mariman,
    "Generative AI without guardrails can harm learning: Evidence from high school
    mathematics", PNAS 122(26), e2422633122, 2025, <https://doi.org/10.1073/pnas.2422633122>.
    Figures are from the abstract: GPT Base and GPT Tutor practice gains of 48% and 127%
    over control, a 17% exam reduction for GPT Base, and an effect the authors describe
    as essentially eradicated for GPT Tutor. Checked 2026-09-10.

[^sankaranarayanan-2026]: Sreecharan Sankaranarayanan, "Mitigating Epistemic Debt in
    Generative AI-Scaffolded Novice Programming using Metacognitive Scripts", arXiv
    2602.20206 (preprint, v2 2026-03-31), <https://arxiv.org/abs/2602.20206>. N=78
    novices (CS undergraduates and recent bootcamp graduates), Cursor with Claude 3.5
    Sonnet. Failure rates on the AI-blackout maintenance task were 77% unrestricted and
    39% with the Explanation Gate; functional utility did not differ (p=.64); the gated
    group was slower (about 64.6 versus 48.2 minutes, d=1.52). Checked 2026-09-10.

[^agent-commands-2026-09]: Measured in this repository on 2026-09-09 over the Claude Code,
    Codex and OpenCode transcripts on one machine, using the session-load adapters in
    [the agents guide](../../guides/integrations/agents/session-load.md): 113,430 Bash tool
    calls in the 21 days to that date; 22 of 203 sessions carried the guard-not-running
    notice and accounted for 17% of those calls. Reproducible with `magus session load`
    over the same transcripts.
