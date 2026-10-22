---
title: magus-test-design
generated_from: internal/agent/skills/magus-test-design/SKILL.md
description: "Choose unit, integration, or end-to-end test boundaries from the magus graph and runtime behavior."
tags: [agents, skills, magus-test-design]
skill_full_bytes: 10491
skill_short_bytes: 5390
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
| `agent-skill-version` | `147` |
| `knowledge-schema-version` | `23` |
| `skill-content` | `ac16ac3c57a3` |
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
# Test design

Choose test boundary from behavior that must be proved, not package, directory, or test target name.
Use when tier, real/fake/stub choice, what test must assert, or coverage placement is undecided or under review.
Do not use:
- routine writing of already-scoped test;
- to run or diagnose tests (`magus-run` owns that);
- to choose package boundary (`magus-architecture-review` owns that). Pair with architecture review only when refactor deliberately makes code easier to test.

Observes evidence, recommends; does not choose test policy, enable service, create credentials, or decide what runs locally, on commit, in CI.

## Only needed evidence

1. State behavior as observable contract: triggering input or event, expected result or state, visible side effects.
2. Discover workspace tests before naming target:

   ```sh
   magus describe targets -o name
   magus describe target <selected-test-target> <project>
   magus explain <node>
   ```

3. Real collaborators, seams, when that matters:

   ```sh
   magus refs <symbol>
   magus path <a> <b>
   ```

4. Read scoped code, existing tests; find effects changing what lower boundary can prove: live process or runtime, filesystem, network, time, scheduler, durable storage, language boundary.

Prefer `client` (`magus\describe.evaluatedTarget`, `magus\explain`, `magus\refs`, `magus\path`); else CLI above.
Do not start server only to review test design.
`unknown` from `magus refs`: missing evidence, not proof of no callers; record it.
Never infer tier from filename, existing mock, or target called `test`.

## Closest falsifying boundary

Tier: contract about; keep real
- Unit: one owned, deterministic decision; values and local state establishing its invariant; no live external service, credentials, or ambient environment
- Integration: real component/service boundary, binding, or durable effect that must participate to prove contract; named collaborators, services, what each needs to start and sign in
- End-to-end: user-visible workflow depending on real dispatch, runtime or process behavior, scheduler, durable state, or other boundary lower tiers cannot establish; in-scope workflow and observable output

Unit test reimplementing collaborator: false reassurance.
End-to-end test only checking local branch: slow, repeats lower tier's check.
State what test does not prove, and where that coverage belongs.

## Execution conditions

Every recommended test: report evidence; do not decide policy:
- real external services, credentials, login, fixtures, environment;
- whether developer can run it locally from normal checkout; setup needed when not;
- expected cost: time, compute, network, money, shared-state risk;
- declared target or explicit invocation showing them.

Test needing real service, secret, or externally prepared environment is not unit test: classify boundary, state what it needs.
Never assume integration or end-to-end tests opt-in, scheduled, or excluded from commits or pull requests.
Recommend run scope from observed cost and prerequisites; user chooses policy.

## Which collaborators are real

Real collaborator by default. Fake implements real behavior; stub supplies only response subject needs.
Every substitute, state:
- why real dependency unsuitable at this boundary;
- contract substitute preserves;
- real-boundary test checking that contract;
- failure mode substitute cannot reveal.

Never add interface only to mock something; earns place only when shipped boundary meaningful alone.
Existing concrete collaborator or test setup may falsify contract at selected tier -> say so: do not add or widen `<seam/interface>`; test through `<existing boundary>`.

## Assert what caller can observe

Compare complete normalized result or state: structured output, persisted state, emitted events, relevant side effects.
Do not stop at one field, success boolean, or call count when contract is richer.
Normalize values varying between runs at boundary (temporary paths, timestamps, unordered traversal, generated IDs); assert volatile invariants separately.
Never normalize ordering, errors, or state changes contract includes.
Internal setup fits only narrow owned unit invariant.

## Case name

Case name in repository's existing idiom; never impose universal pattern or borrow from another language. Record case as:

```text
<proposed repository-idiomatic case name>
  trigger/precondition -> complete observable result/state -> visible side effects
  volatile invariants asserted separately
```

Language- or framework-specific mechanics -> applicable language guidance.

## Deliver

Each behavior, report:
1. Observable contract, crossed system boundary.
2. Evidence used, including index or runtime gaps.
3. Recommended tier, why it can falsify contract.
4. Collaborator matrix: `collaborator -> real/fake/stub -> reason -> contract check`.
5. What test asserts in full after normalizing; volatile invariants separately.
6. Proposed repository-idiomatic case name, explicit case record.
7. Whether existing boundary suffices, or independent reason shipped code needs new or wider one.
8. How it runs: prerequisites, whether developer reproduces it locally, cost, observed target or invocation.
9. Owning Magus test target, final affected-CI route, complementary coverage.

Run -> `magus-run`. Skill chooses proof; never bypasses Magus with raw language test commands.
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
choice, what the test must assert, or where coverage goes is undecided or
under review.

Do not use it:

- for routine writing of an already-scoped test;
- to run or diagnose tests (`magus-run` owns that);
- to choose a package boundary (`magus-architecture-review` owns that). Pair it
  with architecture review only when a refactor deliberately makes code easier to test.

This skill observes evidence and recommends. It does not choose the repository's
test policy, enable a service, create credentials, or decide what runs locally, on a
commit, or in CI.

## Contents

- Gather only the evidence the decision needs
- Select the closest boundary that can falsify the contract
- Make execution conditions visible
- Decide which collaborators are real
- Assert what the caller can observe
- Name and record the proposed case
- Deliver the recommendation
- Evidence gate for delegated work

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

4. Read the scoped code and existing tests. Identify the effects that change what a
   lower boundary can prove. Those include a live process or runtime, the filesystem,
   network, time, a scheduler, durable storage, or a language boundary.

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
| Integration | a real component/service boundary, binding, or durable effect that must participate to prove the contract | the named collaborators and services, and what each needs to start and sign in |
| End-to-end | a user-visible workflow that depends on real dispatch, runtime or process behavior, a scheduler, durable state, or another boundary lower tiers cannot establish | the in-scope workflow and observable output |

A unit test that reimplements a collaborator falsely reassures. An end-to-end test
that only checks a local branch is slow and repeats a lower tier's check. State what
the test does not prove and where that coverage belongs.

## Make execution conditions visible

For every recommended test, report the evidence; do not decide policy:

- real external services, credentials, login, fixtures, or environment;
- whether a developer can run it locally from a normal checkout, and the setup it
  needs when they cannot;
- expected cost: time, compute, network, money, or shared-state risk;
- the declared target or explicit invocation that shows them.

A test that needs a real service, secret, or externally prepared environment is not
a unit test: classify the boundary and state what it needs. Never assume
integration or end-to-end tests are opt-in, scheduled, or excluded from commits or
pull requests. Recommend a run scope from the observed cost and prerequisites; the
user makes the policy choice.

## Decide which collaborators are real

Use a real collaborator by default. A fake implements the real behavior; a stub
supplies only the response the subject needs. For every substitute, state:

- why the real dependency is unsuitable at this boundary;
- the contract the substitute preserves;
- the real-boundary test that checks that contract;
- the failure mode the substitute cannot reveal.

Never add an interface only to mock something. It earns its place only when the
shipped boundary is meaningful on its own.

An existing concrete collaborator or test setup may falsify the contract at the
selected tier. Then say so: **do not add or widen `<seam/interface>`; test through
`<existing boundary>`**.

## Assert what the caller can observe

Compare the complete normalized result or state: structured output, persisted
state, emitted events, relevant side effects. Do not stop at one field, a success
boolean, or a call count when the contract is richer.

Normalize values that vary between runs at the boundary (temporary paths, timestamps,
unordered traversal, generated IDs), and assert volatile invariants separately.
Never normalize ordering, errors, or state changes the contract includes. Internal
setup fits only a narrow owned unit invariant.

## Name and record the proposed case

Propose a case name in the repository's existing idiom; never impose a universal
pattern or borrow one from another language. Record the case as:

```text
<proposed repository-idiomatic case name>
  trigger/precondition -> complete observable result/state -> visible side effects
  volatile invariants asserted separately
```

Hand language- or framework-specific mechanics to the applicable language guidance
(syntax, helpers, fixture setup, naming). This skill keeps the boundary,
collaborator, and observable-contract advice language agnostic.

## Deliver the recommendation

Report, for each behavior:

1. Observable contract and crossed system boundary.
2. Evidence used, including index or runtime gaps.
3. Recommended tier and why it can falsify the contract.
4. Collaborator matrix: `collaborator -> real/fake/stub -> reason -> contract check`.
5. What the test asserts in full, after normalizing, and the volatile invariants separately.
6. Proposed repository-idiomatic case name and its explicit case record.
7. Whether an existing boundary suffices, or the independent reason the shipped code
   needs a new or wider one.
8. How it runs: prerequisites, whether a developer reproduces it locally, cost, and
   the observed target or invocation.
9. Owning Magus test target, final affected-CI route, and complementary coverage.

Hand the run to `magus-run`. This skill chooses the proof; it never bypasses
Magus with raw language test commands.

## Evidence gate for delegated work

Do not label a test unit, integration, or end-to-end until the behavior and at
least one system boundary are named. If a selected target, reference result, or
runtime fact is unavailable, the recommendation is **provisional**. Name the
missing evidence and one concrete next action; do not fill the gap with a
guess.

Do not claim a fake is protected by real-boundary coverage unless you name that
test and its owning Magus target. If no such test exists, the plan
must include it as complementary coverage.

### Preservation gate

Before proposing a seam, interface, or widened substitute point, identify the
existing concrete collaborator or test setup that was considered. If it
can falsify the contract at the selected tier, preserve it and state:
**do not add or widen `<seam/interface>`; test through `<existing boundary>`**.
Only recommend a new or wider seam when the shipped boundary has an
independent reason to exist; a test double alone is not that reason.

### Execution-policy gate

Record the actual prerequisites and cost before suggesting a run scope. A
model may suggest an explicit target, opt-in path, or broader gate, but only with
that evidence behind it. It may not present a policy choice as settled, assume
credentials are available, or create/modify external configuration to make a test
run.

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
| Mock asserts a call count | proves a call pattern the test chose, not the result a caller receives | assert the complete result; cover the real call path at integration scope |
| Snapshot hides volatile data | can bless a change without saying which values matter | normalize only what legitimately varies and assert the remaining structure |
| Fake service mirrors shipped logic | duplicates the system under test and drifts | use the real local component or add a named contract/integration test |
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
