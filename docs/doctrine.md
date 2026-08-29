---
title: Doctrine
description: "The standing design decisions: the principles magus is built by, the standard it is measured against, and the line between what it automates and what it leaves to your judgment."
tags: [doctrine, design, judgment, agents, philosophy, unix]
---

# Doctrine

[Scope](scope.md) records what belongs in the tool. This page records how the
tool is allowed to behave: the principles it is built by, the standard any
addition is measured against, and the line between decisions magus automates
and decisions it hands to you. Each entry names the mechanism that enforces it
and the failure it prevents; check them against the tool you are running. An
entry whose mechanism does not exist yet is listed at the bottom as a debt,
because a rule that lives only in prose is a rule with roughly even odds.

## Principles

### Unix survives at the verb, not the binary

One tool that does one thing extremely well does not survive contact with a
monorepo, and magus does not claim the lineage: a monorepo build tool has to
understand several languages, every project in the tree, caching, scheduling,
and what a change reaches. That is already many things. What survives the
translation is the discipline, relocated to the verb. Each verb answers one
question, deterministically, from declared sources, and stops. Output reads at
a terminal and parses in a pipe (`-o name`, `-o json`, `-o template=`). A
non-zero exit means what it has meant for fifty years. Every surface degrades
to plain text when there is no terminal to draw on, and the pinned band never
takes the screen, the alternate buffer, or your scrollback. Composition
happens where a monorepo needs it - in the graph, through `ctx.needs` - not by
pretending the binary can be smaller than the problem.

The scope test governs what a verb may be, and the one-vocabulary rule -
target, spell, charm, op, each named once and reused everywhere - keeps the
surface predictable. That prevents the failure in both directions: claiming a
minimalism the tool cannot carry, and the house dialect with no rule you can
hold in your head, the one that makes users look things up forever and then
teaches their agents the same confusion.

### Capability is not a reason

Building the wrong thing got cheap. Coherent, well-structured, completely
misconceived work can be generated in minutes, so a design reading well
proves less than it used to, and someone still has to say no to work that
would otherwise ship. The default answer to a new capability is no, and the
burden of proof is a failure removed, never a preference enabled. The version
window needed four dead designs before one passed that bar; the
[knobs rule](scope.md#the-knobs) is the same principle applied to options.

The mechanism is the [scope test](scope.md#the-test), applied in review by
reading the diff for what the tool had to learn. It prevents the accretion
this project was a reaction to - each addition defending itself, nobody
removing anything - now accelerated, because writing the unnecessary thing
costs nearly nothing while maintaining it costs what it always did.

### Optimize the loop that verifies, never the loop that generates

Producing code was never the bottleneck. Reviewing it and trusting it were,
and both got harder as generating got cheap. So every mechanism here attacks
the verify side: the cache makes re-checking free, `affected` makes the check
minimal, the drift gate makes generated output self-verifying, the explain
lenses make verdicts inspectable, `describe file` makes a diff triageable.
Nothing in magus exists to make producing code faster. That is a commitment,
not a gap.

The test for a proposed feature is whose loop it accelerates. A pitch of
"produce more, faster" fails by construction; "know sooner, trust cheaper" is
at least aimed at the right loop. This prevents magus from becoming a tool
whose product is throughput - volume is the one metric that gets easier to
move every year and proves less every year, and a build tool that helps ship
more unverified work faster has joined the problem it was built against.

### Human-first is the AI integration

magus was built for humans, and agents drive it well anyway, because an
interface legible to a person is legible to anything. That ordering is the
design, and the reason underneath it is structural: models learned to use
tools from decades of humans using tools, so a tool that honors the
conventions humans settled - exit codes, a working directory that means what
it means everywhere, a path as a project's name, plain text - inherits agent
competence for free. A tool that invents its own dialect fails both
audiences, then watches the models learn its workarounds forever. Buzz is the
evidence we have: a language with close to no training data produces better
agent output than the most-trained languages on earth, with explicitness the
only variable in sight. Weigh that as experience rather than benchmark; it is
still the strongest evidence on this page.

The mechanism is an ordering rule with one worked example: no surface is
designed for an agent first. The `agents` key in `affected ci --plan` is the
one agent-specific field in an otherwise human-first surface - the
skill-routing hint sits quarantined inside it, so everything around it reads
as what it is, ordinary build metadata a person wanted first. The one
deliberate exception is the delegation ledger, an agent-to-agent declaration
under [Agents propose, humans dispose](#agents-propose-humans-dispose): it
carries no CLI verb because no person is its audience. This prevents the
bolted-on AI integration, papering over a tool
people already struggle with, and the quiet inversion where a person becomes
the secondary user of their own build tool.

### Verdicts are provenance-blind; disposition is not

A verdict - the cache saying a replay is honest, the drift gate saying
generated output matches source, a diagnostic, a CI result - judges the work.
No verdict conditions on who or what produced the change: cache keys hash
content, the drift gate compares bytes, diagnostics read the tree. Whether a
change was typed by hand or generated is invisible to every gate, on purpose,
because good software is the only defensible standard at the tool layer, and
a bar that moves with the byline is theater in both directions - extra
suspicion for one author, unearned trust for another.

Disposition is the opposite case. Accepting a proposal, sending a review,
landing a change: these are accountability, and accountability needs a true
name. That is why authorship is stamped from the transport a write arrived
on, never from what the writer claims about itself - the mechanism described
under [Agents propose, humans dispose](#agents-propose-humans-dispose). The
blindness half is structural today and stated here so that a proposal to
break it, a gate keyed on how code was written, has to argue with this page
first.

Exactly one code path grades a write by who is acting, and naming it is what
keeps the rule checkable. `gradeDelegatedWrite` reads the delegation ledger to
decide whether a worker is editing outside the paths its delegation declared.
That is a concurrency-ownership question on the guard surface - who owns this
file right now - rather than a judgment about the work, and it reaches no cache
key, no drift comparison, and no diagnostic. Its uncertainties - no ledger, no
live delegation, a file that will not parse - fail open with at most an
advisory.

## The standard

Adapted from Wendell Berry's nine standards for adopting a new tool ("Why I
Am Not Going to Buy a Computer", 1987). His subject was a farm; the criteria
survive the distance because they are about what a tool owes the person who
takes it up. Held against magus in both directions - what magus asks of
itself before adding anything, and what you should ask of magus before
adopting it:

1. **Cheaper than what it replaces.** Total cost: install, learning, upkeep.
   One binary, no account, nothing to upsell, no toolchain underneath it to
   break. The honest half: the learning cost is charged up front, and the
   claim is cheaper over a year, not cheaper in week one.
2. **At least as small in scale.** The surface may not outgrow what one
   person can hold. Enforced today by judgment alone; the ledger that would
   measure it is a debt, below.
3. **Clearly and demonstrably better.** A claim of better needs an artifact
   someone else can check: the benchmark results, committed with the
   hardware, the date, and the exact competing tool versions they were
   taken on; terminal recordings that are captured bytes rather than mockups;
   the drift gate. The claim that querying the graph beats grepping is argued
   from mechanism and not yet measured; weigh it accordingly.
4. **Uses less energy.** `affected` and the cache are this criterion
   implemented: run only what a change reaches, never do the same work
   twice. The savings are real and currently invisible; a debt, below.
5. **Powered by what the work already produces.** Berry said the body's own
   energy. Here it is the [scope test](scope.md#the-test) verbatim: every
   capability reads the model that correctness already forced the build to
   hold, and the one exception, the release registry, is named there.
6. **Repairable by its user.** Refusals carry a next step, every diagnostic
   code has a page, the host wiring is a template you own, no generated code
   sits between you and a running build, and the guard fails open with a
   notice rather than failing silent. The systematic version is missing; a
   debt, below.
7. **Repairable near home.** The cache is a plain store on your disk, the
   console is loopback-locked, no capability requires a hosted service, the
   daemon accelerates and never gates. The exceptions run on infrastructure
   we operate; [Scope names them](scope.md#where-the-claim-is-strained).
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

## Judgment

The entries above say how the tool is built. The rest of the page records
when magus automates a decision and when it hands the decision to you.

### Agents propose, humans dispose

An agent surface can suggest work; it cannot accept it. magus records
authorship from the surface that performed the write, so a change made through
the agent surface carries an agent's name no matter what the writer reports
about itself. Interrupting a person costs attention, and the suggestion
operation reflects that: it requires a stated reason before the proposal
reaches anyone. The delegation ledger has no CLI verb, unlike the attention
events `notify` raises, because an attention event is addressed to a person
while the ledger is an agent-to-agent declaration read back by the guard and
the console.

Automated review is wrong at a steady rate, and wrong in a characteristic way:
the confident finding that "fixes" behavior somebody chose on purpose. The
person deciding whether a proposal lands is the one who catches those findings.
If magus applied them itself, they would ship unexamined.

### The host wiring is yours

magus owns the guard rules and the verdict. It does not own an integration with
your agent host. The rules come from one binary and are identical everywhere;
the part that knows your host - which event field carries the command, which
reply channel reaches the model, what happens when the binary cannot be found -
is a template you copy and edit. Adding a host is your change, not a magus
release.

The contract is small enough to hold in your head. `magus session hook` takes
the thing to judge on stdin, either plain text or a payload keyed by FIELD
(`tool_input.command`, `file_path`, `prompt`), never by a host name or a tool
name. `-o template=` renders the verdict into whatever shape the host's reply
takes. `TestNoHostSpecificBehaviorInCode` fails the build when a host's NAME
appears in code anywhere but a path on disk, and the `magus-guard-coverage:`
line each template carries feeds a parity gate that fails when a host was never
asked about a decision the contract grew.

The name test is one layer shallower than the rule it enforces: a branch keyed
on a host's tool vocabulary rather than its name - a switch over `Read` and
`Bash` - is a per-host branch in everything but spelling, and passes the gate
untouched. The activity-event tool labels are magus's own vocabulary for that
reason, chosen by which flag the wrapper passed.

A layer that made every host work with no friction is what is being traded away
here, and it is worth being plain about the trade. Agent hosts change their
event shapes and hook names on their own schedule. A workspace whose
integration lives inside magus waits for a magus release to get unblocked; a
workspace that owns fifteen lines of shell edits them that afternoon. The cost
is charged up front and on purpose: you read your host's hook documentation
once, and there is no zero-configuration path.

The second cost of such a layer is the one nobody sees until it matters. A
guard you did not wire is a guard whose failure mode you do not know, and this
one fails OPEN by design, so a hook that quietly stopped judging looks exactly
like a session with nothing to deny. So a shipped template announces its
fail-open arms rather than exiting quietly, and
`TestFailOpenArmsAnnounceThemselves` fails the build when one stops. One
template is exempt, and the test declares it rather than leaving it to be
discovered: the path template stays silent. An empty response on that surface
already means allow, a notice on every file edit was judged the worse noise,
and hearing about it there is opt-in. Whoever runs the guard should be
able to read what it does and repair it without us.

### A refusal carries its reason

A refusal names the mechanism and a next step. Diagnostics are coded and
[documented](reference/diagnostics.md) so you can look one up. A stale binary
can surface as a typo and a missing tool as a lint finding; where magus can
tell the difference, the message says what is happening underneath. The same
rule reaches workflow refusals: `notes edit` declines to rewrite a note's
anchors from a pipe, and the error says why anchors are not pipeable and what
to do instead.

A denial that says no and nothing else sends you around the tool, and the
workaround runs outside the cache and the sandbox, where magus can no longer
account for the work. An error that leaves you with no next step is a doctrine
bug; file it as one.

### Automation you can interrogate

Each automated verdict has a lens that shows its inputs. `affected --explain`
says why a project is in the affected set. `describe target --cache --inputs`
says what hashed into a cache key. `describe file` says whether a file is
generated and by what. `explain` gives a graph node's provenance and edges.
New behavior ships with its lens in the same change: an action magus cannot
account for is a defect in the same class as a wrong answer.

The lens rule has a second half, and it is criterion 6 pointed at the tool
itself: a step magus runs on your behalf should be expressible as the
toolchain invocation it performed, so a year of running magus leaves you
knowing more about `go build` and your own repository, not less. A spell
wraps a tool without hiding it, and there is no shortcut in there where
knowing what your toolchain does stops being your job. Helpful, but never so
helpful that nobody using the tool learns anything from it.

### Manual on purpose

magus could automate each row below, and does not.

| stays manual                | because                                                                                                                                               |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| disposing an agent request  | an event that means "blocked on input" or "blocked on approval" exists to reach a person; answering it for them removes the person it exists to reach |
| applying suggested changes  | a suggestion lands only when a person accepts it                                                                                                      |
| writing the knowledge store | notes are human-authored by construction: there is no author field to spoof, because authorship rides version control                                 |
| sending a review            | a remark reaches a colleague under your name; an agent drafts into the session and a person sends the batch                                           |

All four would be cheap to build, and cheap is not the test. The test for any
future row: does automating the step remove a repetition, or remove a rep? A
repetition - the same build run again for no new information - is the tool's
to eat, and the cache exists to eat it. A rep - reading the failure, deciding
what it means, choosing what lands - is where judgment forms, and a workflow
optimized until its operator no longer forms the judgment it depends on has
automated the wrong half. Each row above removes the person at the point
where the mechanism needs their judgment, so each stays out.

[Sending a review](concepts/review.md) is the newest of them and the one with
the most surface to give away, so its refusals are worth naming: no review
command carries a `--publish` flag, a self-review is always a comment rather
than an approval, and authorship is stamped from the transport a write arrived
on rather than from what the writer claims. A batch waits for a person because
publishing is one outward-facing act; splitting it into a call per remark would
turn the act that needs confirming into a series of small ones nobody confirms.

## Where this is strained

Friction placed wrong is bureaucracy. A refusal that teaches and one that nags
differ in nothing but their message text, and message text rots like any other
prose; the doctrine holds while error messages get the same care as code.

The explain lenses cover verdicts magus computes. magus captures and replays
the output of the tools a target drives, but it cannot make a third-party
tool's reasoning inspectable; the lens stops at the tool boundary.

The host boundary is not as clean as the entry above states it. The binary
still knows a handful of host paths: `magus doctor` inventories the config
locations it can name when it reports on guard wiring, and `agent install`
probes the conventional skill directories. Both are read-only conveniences over
locations on disk, and a host neither one names still works, but extending
either list is a magus release. The envelope field names came from one host's
payload shape rather than from a neutral design, so a host that spells them
differently reshapes its payload before piping it. And the OpenCode plugin
carries a type-check and tests because it is real TypeScript, which is more
upkeep than a shell template and more than an example should need.

Four rules on this page live only in prose today, and by this page's own
standard that makes each a debt. Every one is a read of what magus already
records, so each passes the scope test; none is built:

- **The surface ledger** (criterion 2): a generated, committed inventory of
  verbs, flags, config keys, and diagnostic codes, regenerated by `generate`
  so the drift gate lands any growth in the diff of the change that caused
  it, where the person who can say no is already looking. Not a cap - a
  number nobody can fail to notice.
- **The savings lens** (criterion 4): what the cache and the affected set
  actually bought - runs replayed against runs executed, wall time avoided -
  as arithmetic over run records that already exist, interrogable like any
  other verdict. Until it exists, "the cache is worth its complexity" is
  taken on trust, which is the one way this tool asks not to be taken.
- **The departure test** (criterion 1): [Scope](scope.md#where-the-claim-is-strained)
  claims that deleting everything magus wrote leaves `magus run build`
  working, and admits the claim is thinner than it sounds. An audit job that
  strips those artifacts in a throwaway worktree and builds would make the
  exit guarantee self-verifying instead of asserted.
- **When it breaks** (criterion 6): the diagnostics reference covers coded
  refusals; the failures that never raise a code - a daemon that did not
  bind, a watcher gone stale, a cache entry that will not replay - are
  documented where someone thought to write them down and absent where
  nobody did. The systematic version is a failure-modes section per concept
  page, enforced the way this repository already enforces document shape.

The line between automated and manual moves as a mechanism earns confidence.
To move a row out of the table above, or a debt off this list, edit this page
in the same commit that changes the behavior.
