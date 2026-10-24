---
title: The standard
order: 2
description: Wendell Berry's nine standards for a new tool, held against magus in both directions.
tags: [design, standard]
---

# The standard

Adapted from Wendell Berry's nine standards for adopting a new tool ("Why I
Am Not Going to Buy a Computer", 1987). His subject was a farm; the criteria
survive the distance because they are about what a tool owes the person who
takes it up. Held against magus in both directions: what magus asks of
itself before adding anything, and what you should ask of magus before
adopting it:

1. **Cheaper than what it replaces.** Total cost: install, learning, upkeep.
   One binary, no account, nothing to upsell, no toolchain underneath it to
   break. The honest half: the learning cost is charged up front, and the
   claim is cheaper over a year, not cheaper in week one.
2. **At least as small in scale.** The tool may not outgrow what one
   person can hold. Enforced today by judgment alone; the ledger that would
   measure it is a debt, in [Where this is strained](strained.md).
3. **Clearly and demonstrably better.** A claim of better needs an artifact
   someone else can check: the benchmark results, committed with the
   hardware, the date, and the exact competing tool versions they were
   taken on; terminal recordings that are captured bytes rather than mockups;
   the drift gate. The claim that querying the graph beats grepping is argued
   from mechanism and not yet measured; weigh it accordingly.
4. **Uses less energy.** `affected` and the cache are this criterion
   implemented: run only what a change reaches, never do the same work
   twice. The savings are real and currently invisible; a debt, in [Where this is strained](strained.md).
5. **Powered by what the work already produces.** Berry said the body's own
   energy. Here it is the [scope test](../scope.md#the-test) verbatim: every
   capability reads the model that correctness already forced the build to
   hold, and the one exception, the release registry, is named there.
6. **Repairable by its user.** Refusals carry a next step, every diagnostic
   code has a page, the host wiring is a template you own, no generated code
   sits between you and a running build, and the guard fails open with a
   notice rather than failing silent. The systematic version is missing; a
   debt, in [Where this is strained](strained.md).
7. **Repairable near home.** The cache is a plain store on your disk, the
   console is loopback-locked, no capability requires a hosted service, the
   daemon accelerates and never gates. The exceptions run on infrastructure
   we operate; [Scope names them](../scope.md#where-the-claim-is-strained).
8. **From a shop that will take it back.** One maintainer, said plainly, and
   the mitigations are structural rather than social: a sealed engine kept
   small, documentation written before announcement, GPLv3 so the tool stays
   repairable in common if the shop ever closes. Choosing Buzz for its
   maintainers was this criterion applied to a dependency before we had read
   it stated.
9. **Disrupts nothing good that already exists.** The terminal's decades of
   contract stay intact: scrollback, selection, the working directory, plain
   text, your toolchain visible and yours. The console is where this is
   under active tension, and it ships under a rule: what does not give
   genuine value over the good thing that already exists should not ship.
