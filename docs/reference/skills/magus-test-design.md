---
title: magus-test-design
generated_from: internal/agent/skills/magus-test-design/SKILL.md
description: "Choose unit, integration, or end-to-end test boundaries from the magus graph and runtime behavior."
tags: [agents, skills, magus-test-design]
skill_full_bytes: 10232
skill_short_bytes: 6230
---

# magus-test-design

Choose unit, integration, or end-to-end test boundaries from the magus graph and runtime behavior. Use when designing, writing, or reviewing tests that require a real/fake/stub decision, complete observable assertions, or a coverage-gap assessment. Do not use merely to execute or diagnose tests (magus-run), or to choose package seams (magus-architecture-review).

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus doctor` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus doctor` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `116` |
| `knowledge-schema-version` | `16` |
| `skill-content` | `6e01e650d37a` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-test-design-variant" id="magus-test-design-tab-short" checked>
<label for="magus-test-design-tab-short">Short form</label>
<input type="radio" name="magus-test-design-variant" id="magus-test-design-tab-full">
<label for="magus-test-design-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-test-design/SKILL.md
```

````markdown
# Designing tests from observable boundaries

Choose a test boundary from the behavior that must be proved, not from the package,
directory, or test target name. Use this skill when the tier, the real/fake/stub
decision, the complete observable assertion, or coverage placement is undecided or
under review.

Do not use it:

- for routine implementation of an already-scoped test;
- to run or diagnose tests (`magus-run` owns that);
- to choose a package boundary (`magus-architecture-review` owns that). Pair it
  with architecture review only when a refactor deliberately targets testability.

This skill observes evidence and recommends. It does not choose the repository's
test policy, enable a service, create credentials, or decide what runs locally, on a
commit, or in CI.

## Gather only the evidence the decision needs

1. State the behavior as an observable contract: triggering input or event,
   expected result or state, and visible side effects.
2. Discover the workspace's tests before naming a target:

   ```sh
   magus describe targets -o name
   magus describe target <selected-test-target> <project>
   magus explain <node>
   ```

3. Locate real collaborators and seams when that matters:

   ```sh
   magus refs <symbol>
   magus path <a> <b>
   ```

4. Read the scoped production code and existing tests. Identify effects that change
   what a lower boundary can prove: process or runtime execution, filesystem,
   network, time, scheduling, persistence, or a language boundary.

Prefer `client` (`magus\describe.evaluatedTarget`, `magus\explain`, `magus\refs`,
`magus\path`); fall back to the CLI commands above. Do not start a server only to
review test design.

An `unknown` from `magus refs` is missing evidence, not proof of no callers: record
it. Never infer the tier from a filename, an existing mock, or a target called
`test`.

## Select the closest boundary that can falsify the contract

| Tier | The contract is about | Keep real |
| --- | --- | --- |
| Unit | one owned, deterministic decision | values and local state that establish its invariant; it should not require a live external service, credentials, or ambient environment |
| Integration | a real component/service boundary, binding, or durable effect that must participate to prove the contract | the named collaborators and services, with their setup/authentication requirements made explicit |
| End-to-end | a user-visible workflow that depends on real dispatch, runtime/process behavior, scheduling, persistence, or another boundary lower tiers cannot establish | the in-scope workflow and observable output |

A unit test that reimplements a collaborator is false confidence. An end-to-end test
that only checks a local branch is slow duplication. State what the test does not
prove and where that coverage belongs.

## Make execution conditions visible

For every recommended test, report the evidence; do not decide policy:

- real external services, credentials, authentication, fixtures, or environment;
- whether a developer can run it locally from a normal checkout, and the setup it
  needs when they cannot;
- expected cost: time, compute, network, money, or shared-state risk;
- the declared target or explicit invocation that exposes those conditions.

A test that needs a real service, secret, or externally prepared environment is not
a unit test: classify the boundary and state the requirement. Never assume
integration or end-to-end tests are opt-in, scheduled, or excluded from commits or
pull requests. Recommend a run scope from the observed cost and prerequisites; the
user makes the policy choice.

## Decide which collaborators are real

Use a real collaborator by default. A fake is a behavioral implementation; a stub
supplies only the response the subject needs. For every substitute, state:

- why the real dependency is unsuitable at this boundary;
- the contract the substitute preserves;
- the real-boundary test that checks that contract;
- the failure mode the substitute cannot reveal.

Never add an interface only to mock something. It earns its place only when the
production boundary is independently meaningful.

An existing concrete collaborator or test arrangement may falsify the contract at the
selected tier. Then say so: **do not add or widen `<seam/interface>`; test through
`<existing boundary>`**.

## Assert what the caller can observe

Compare the complete normalized result or state: structured output, persisted
state, emitted events, relevant side effects. Do not stop at one field, a success
boolean, or a call count when the contract is richer.

Normalize genuine nondeterminism at the boundary (temporary paths, timestamps,
unordered iteration, generated IDs), and assert volatile invariants separately.
Never normalize ordering, errors, or transitions the contract includes. Internal
arrangement fits only a narrow owned unit invariant.

## Name and record the proposed case

Propose a case name in the repository's existing idiom; never impose a universal
convention or borrow one from another language. Record the case as:

```text
<proposed repository-idiomatic case name>
  trigger/precondition -> complete observable result/state -> visible side effects
  volatile invariants asserted separately
```

Hand language- or framework-specific mechanics to the applicable language guidance.

## Deliver the recommendation

Report, for each behavior:

1. Observable contract and crossed system boundary.
2. Evidence used, including index or runtime gaps.
3. Recommended tier and why it can falsify the contract.
4. Collaborator matrix: `collaborator -> real/fake/stub -> reason -> contract check`.
5. Full normalized assertion plan and separate volatile invariants.
6. Proposed repository-idiomatic case name and its explicit case record.
7. Preservation conclusion: whether an existing boundary suffices, or the
   independent production reason to introduce or widen one.
8. Execution profile: prerequisites, local reproducibility, cost, and observed
   target or invocation.
9. Owning Magus test target, final affected-CI route, and complementary coverage.

Hand execution to `magus-run`. This skill chooses the proof; it never bypasses
Magus with raw language test commands.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-test-design-full/SKILL.md
```

````markdown
# Designing tests from observable boundaries

Choose a test boundary from the behavior that must be proved, not from the package,
directory, or test target name. Use this skill when the tier, the real/fake/stub
decision, the complete observable assertion, or coverage placement is undecided or
under review.

Do not use it:

- for routine implementation of an already-scoped test;
- to run or diagnose tests (`magus-run` owns that);
- to choose a package boundary (`magus-architecture-review` owns that). Pair it
  with architecture review only when a refactor deliberately targets testability.

This skill observes evidence and recommends. It does not choose the repository's
test policy, enable a service, create credentials, or decide what runs locally, on a
commit, or in CI.

## Gather only the evidence the decision needs

1. State the behavior as an observable contract: triggering input or event,
   expected result or state, and visible side effects.
2. Discover the workspace's tests before naming a target:

   ```sh
   magus describe targets -o name
   magus describe target <selected-test-target> <project>
   magus explain <node>
   ```

3. Locate real collaborators and seams when that matters:

   ```sh
   magus refs <symbol>
   magus path <a> <b>
   ```

4. Read the scoped production code and existing tests. Identify effects that change
   what a lower boundary can prove: process or runtime execution, filesystem,
   network, time, scheduling, persistence, or a language boundary.

Prefer `client` (`magus\describe.evaluatedTarget`, `magus\explain`, `magus\refs`,
`magus\path`); fall back to the CLI commands above. Do not start a server only to
review test design.

An `unknown` from `magus refs` is missing evidence, not proof of no callers: record
it. Never infer the tier from a filename, an existing mock, or a target called
`test`.

## Select the closest boundary that can falsify the contract

| Tier | The contract is about | Keep real |
| --- | --- | --- |
| Unit | one owned, deterministic decision | values and local state that establish its invariant; it should not require a live external service, credentials, or ambient environment |
| Integration | a real component/service boundary, binding, or durable effect that must participate to prove the contract | the named collaborators and services, with their setup/authentication requirements made explicit |
| End-to-end | a user-visible workflow that depends on real dispatch, runtime/process behavior, scheduling, persistence, or another boundary lower tiers cannot establish | the in-scope workflow and observable output |

A unit test that reimplements a collaborator is false confidence. An end-to-end test
that only checks a local branch is slow duplication. State what the test does not
prove and where that coverage belongs.

## Make execution conditions visible

For every recommended test, report the evidence; do not decide policy:

- real external services, credentials, authentication, fixtures, or environment;
- whether a developer can run it locally from a normal checkout, and the setup it
  needs when they cannot;
- expected cost: time, compute, network, money, or shared-state risk;
- the declared target or explicit invocation that exposes those conditions.

A test that needs a real service, secret, or externally prepared environment is not
a unit test: classify the boundary and state the requirement. Never assume
integration or end-to-end tests are opt-in, scheduled, or excluded from commits or
pull requests. Recommend a run scope from the observed cost and prerequisites; the
user makes the policy choice.

## Decide which collaborators are real

Use a real collaborator by default. A fake is a behavioral implementation; a stub
supplies only the response the subject needs. For every substitute, state:

- why the real dependency is unsuitable at this boundary;
- the contract the substitute preserves;
- the real-boundary test that checks that contract;
- the failure mode the substitute cannot reveal.

Never add an interface only to mock something. It earns its place only when the
production boundary is independently meaningful.

An existing concrete collaborator or test arrangement may falsify the contract at the
selected tier. Then say so: **do not add or widen `<seam/interface>`; test through
`<existing boundary>`**.

## Assert what the caller can observe

Compare the complete normalized result or state: structured output, persisted
state, emitted events, relevant side effects. Do not stop at one field, a success
boolean, or a call count when the contract is richer.

Normalize genuine nondeterminism at the boundary (temporary paths, timestamps,
unordered iteration, generated IDs), and assert volatile invariants separately.
Never normalize ordering, errors, or transitions the contract includes. Internal
arrangement fits only a narrow owned unit invariant.

## Name and record the proposed case

Propose a case name in the repository's existing idiom; never impose a universal
convention or borrow one from another language. Record the case as:

```text
<proposed repository-idiomatic case name>
  trigger/precondition -> complete observable result/state -> visible side effects
  volatile invariants asserted separately
```

Hand language- or framework-specific mechanics to the applicable language guidance
(syntax, helpers, fixture setup, naming). This skill keeps the boundary,
collaborator, and observable-contract recommendation language agnostic.

## Deliver the recommendation

Report, for each behavior:

1. Observable contract and crossed system boundary.
2. Evidence used, including index or runtime gaps.
3. Recommended tier and why it can falsify the contract.
4. Collaborator matrix: `collaborator -> real/fake/stub -> reason -> contract check`.
5. Full normalized assertion plan and separate volatile invariants.
6. Proposed repository-idiomatic case name and its explicit case record.
7. Preservation conclusion: whether an existing boundary suffices, or the
   independent production reason to introduce or widen one.
8. Execution profile: prerequisites, local reproducibility, cost, and observed
   target or invocation.
9. Owning Magus test target, final affected-CI route, and complementary coverage.

Hand execution to `magus-run`. This skill chooses the proof; it never bypasses
Magus with raw language test commands.

## Evidence gate for delegated work

Do not label a test unit, integration, or end-to-end until the behavior and at
least one system boundary are named. If a selected target, reference result, or
runtime fact is unavailable, the recommendation is **provisional**. Name the
missing evidence and one concrete next action; do not fill the gap with an
assumption.

Do not claim a fake is protected by real-boundary coverage unless you name that
test and its owning Magus target. If no such test exists, the recommendation
must include it as complementary coverage.

### Preservation gate

Before proposing a seam, interface, or widened substitution point, identify the
existing concrete collaborator or test arrangement that was considered. If it
can falsify the contract at the selected tier, preserve it and state:
**do not add or widen `<seam/interface>`; test through `<existing boundary>`**.
Only recommend a new or wider seam when the production boundary has an
independent reason to exist; a test double alone is not that reason.

### Execution-policy gate

Record the actual prerequisites and cost before recommending a run scope. A
model may recommend an explicit target, opt-in path, or broader gate only as a
recommendation supported by that evidence. It may not present a policy choice
as a requirement, assume credentials are available, or create/modify external
configuration to make a test run.

### Decision procedure

```text
Can owned local state alone falsify the observable contract?
  yes -> unit
  no  -> Does the contract require collaboration of owned components or a
         durable local/binding boundary?
           yes -> integration
           no  -> Does it depend on real dispatch, process/runtime behavior,
                  scheduling, persistence, or a user-visible workflow?
                    yes -> end-to-end
                    no  -> provisional: gather the missing boundary evidence
```

The tiers are not a speed ranking. Move outward only when the inner boundary
cannot produce the failure the contract describes.

### Common false confidence

| Shape | Why it misleads | Better proof |
| --- | --- | --- |
| Mock asserts a call count | proves an interaction chosen by the test, not the result a caller receives | assert the complete result; cover the real interaction at integration scope |
| Snapshot hides volatile data | can bless a change without saying which values matter | normalize only legitimate volatility and assert the remaining structure |
| Fake service mirrors production logic | duplicates the system under test and drifts | use the real local component or add a named contract/integration test |
| E2E checks only success | proves the workflow exited, not that it produced the required state | assert the full observable result and durable effects |

### Fill-in report

```text
Behavior:
  trigger -> observable result/state -> side effects

Evidence:
  selected target / project:
  graph and source evidence:
  unknown or unavailable evidence, and next action:

Boundary:
  unit | integration | end-to-end
  reason this boundary can falsify the contract:

Case:
  proposed repository-idiomatic name:
  trigger/precondition -> complete observable result/state -> visible side effects:
  volatile invariants asserted separately:

Preservation:
  existing boundary considered:
  do not add or widen <seam/interface>; test through <existing boundary>
  or independent production reason to introduce/widen it:

Collaborators:
  <name> -> real|fake|stub -> reason -> named contract test / target

Assertions:
  complete normalized result/state:
  volatile invariant asserted separately:

Execution and gaps:
  prerequisites / local setup / cost:
  observed target or invocation -> recommended run scope (user decides):
  owning test target -> affected CI
  complementary coverage / residual risk:
```
````


</section>

</article>
