{{if .Full}}# Designing tests from observable boundaries{{else}}# Test design{{end}}

{{if .Full}}Choose a test boundary from the behavior that must be proved, not from the package,
directory, or test target name. Use this skill when the tier, the real/fake/stub
choice, what the test must assert, or where coverage goes is undecided or
under review.

Do not use it:

- for routine writing of an already-scoped test;{{else}}Choose test boundary from behavior that must be proved, not package, directory, or test target name.
Use when tier, real/fake/stub choice, what test must assert, or coverage placement is undecided or under review.
Do not use:
- routine writing of already-scoped test;{{end}}
- to run or diagnose tests (`{{skill "run"}}` owns that);
{{if .Full}}- to choose a package boundary (`{{skill "architecture-review"}}` owns that). Pair it
  with architecture review only when a refactor deliberately makes code easier to test.{{else}}- to choose package boundary (`{{skill "architecture-review"}}` owns that). Pair with architecture review only when refactor deliberately makes code easier to test.{{end}}

{{if .Full}}This skill observes evidence and recommends. It does not choose the repository's
test policy, enable a service, create credentials, or decide what runs locally, on a
commit, or in CI.{{else}}Observes evidence, recommends; does not choose test policy, enable service, create credentials, or decide what runs locally, on commit, in CI.{{end}}

{{if .Full}}## Gather only the evidence the decision needs{{else}}## Only needed evidence{{end}}

{{if .Full}}1. State the behavior as an observable contract: triggering input or event,
   expected result or state, and visible side effects.{{else}}1. State behavior as observable contract: triggering input or event, expected result or state, visible side effects.{{end}}
{{if .Full}}2. Discover the workspace's tests before naming a target:{{else}}2. Discover workspace tests before naming target:{{end}}

   ```sh
   magus describe targets -o name
   magus describe target <selected-test-target> <project>
   magus explain <node>
   ```

{{if .Full}}3. Locate real collaborators and seams when that matters:{{else}}3. Real collaborators, seams, when that matters:{{end}}

   ```sh
   magus refs <symbol>
   magus path <a> <b>
   ```

{{if .Full}}4. Read the scoped code and existing tests. Identify the effects that change what a
   lower boundary can prove. Those include a live process or runtime, the filesystem,
   network, time, a scheduler, durable storage, or a language boundary.{{else}}4. Read scoped code, existing tests; find effects changing what lower boundary can prove: live process or runtime, filesystem, network, time, scheduler, durable storage, language boundary.{{end}}

{{if .Full}}Prefer `{{tool "client"}}` (`{{buzz "describe.evaluatedTarget"}}`, `{{buzz "explain"}}`, `{{buzz "refs"}}`,
`{{buzz "path"}}`); fall back to the CLI commands above. Do not start a server only to
review test design.

An `unknown` from `{{cmd "refs"}}` is missing evidence, not proof of no callers: record
it. Never infer the tier from a filename, an existing mock, or a target called
`test`.{{else}}Prefer `{{tool "client"}}` (`{{buzz "describe.evaluatedTarget"}}`, `{{buzz "explain"}}`, `{{buzz "refs"}}`, `{{buzz "path"}}`); else CLI above.
Do not start server only to review test design.
`unknown` from `{{cmd "refs"}}`: missing evidence, not proof of no callers; record it.
Never infer tier from filename, existing mock, or target called `test`.{{end}}

{{if .Full}}## Select the closest boundary that can falsify the contract{{else}}## Closest falsifying boundary{{end}}

{{if .Full}}| Tier | The contract is about | Keep real |
| --- | --- | --- |
| Unit | one owned, deterministic decision | values and local state that establish its invariant; it should not require a live external service, credentials, or ambient environment |
| Integration | a real component/service boundary, binding, or durable effect that must participate to prove the contract | the named collaborators and services, and what each needs to start and sign in |
| End-to-end | a user-visible workflow that depends on real dispatch, runtime or process behavior, a scheduler, durable state, or another boundary lower tiers cannot establish | the in-scope workflow and observable output |{{else}}Tier: contract about; keep real
- Unit: one owned, deterministic decision; values and local state establishing its invariant; no live external service, credentials, or ambient environment
- Integration: real component/service boundary, binding, or durable effect that must participate to prove contract; named collaborators, services, what each needs to start and sign in
- End-to-end: user-visible workflow depending on real dispatch, runtime or process behavior, scheduler, durable state, or other boundary lower tiers cannot establish; in-scope workflow and observable output{{end}}

{{if .Full}}A unit test that reimplements a collaborator falsely reassures. An end-to-end test
that only checks a local branch is slow and repeats a lower tier's check. State what
the test does not prove and where that coverage belongs.{{else}}Unit test reimplementing collaborator: false reassurance.
End-to-end test only checking local branch: slow, repeats lower tier's check.
State what test does not prove, and where that coverage belongs.{{end}}

{{if .Full}}## Make execution conditions visible{{else}}## Execution conditions{{end}}

{{if .Full}}For every recommended test, report the evidence; do not decide policy:
{{else}}Every recommended test: report evidence; do not decide policy:{{end}}
{{if .Full}}- real external services, credentials, login, fixtures, or environment;{{else}}- real external services, credentials, login, fixtures, environment;{{end}}
{{if .Full}}- whether a developer can run it locally from a normal checkout, and the setup it
  needs when they cannot;{{else}}- whether developer can run it locally from normal checkout; setup needed when not;{{end}}
{{if .Full}}- expected cost: time, compute, network, money, or shared-state risk;{{else}}- expected cost: time, compute, network, money, shared-state risk;{{end}}
{{if .Full}}- the declared target or explicit invocation that shows them.{{else}}- declared target or explicit invocation showing them.{{end}}

{{if .Full}}A test that needs a real service, secret, or externally prepared environment is not
a unit test: classify the boundary and state what it needs. Never assume
integration or end-to-end tests are opt-in, scheduled, or excluded from commits or
pull requests. Recommend a run scope from the observed cost and prerequisites; the
user makes the policy choice.{{else}}Test needing real service, secret, or externally prepared environment is not unit test: classify boundary, state what it needs.
Never assume integration or end-to-end tests opt-in, scheduled, or excluded from commits or pull requests.
Recommend run scope from observed cost and prerequisites; user chooses policy.{{end}}

{{if .Full}}## Decide which collaborators are real{{else}}## Which collaborators are real{{end}}

{{if .Full}}Use a real collaborator by default. A fake implements the real behavior; a stub
supplies only the response the subject needs. For every substitute, state:
{{else}}Real collaborator by default. Fake implements real behavior; stub supplies only response subject needs.
Every substitute, state:{{end}}
{{if .Full}}- why the real dependency is unsuitable at this boundary;{{else}}- why real dependency unsuitable at this boundary;{{end}}
{{if .Full}}- the contract the substitute preserves;{{else}}- contract substitute preserves;{{end}}
{{if .Full}}- the real-boundary test that checks that contract;{{else}}- real-boundary test checking that contract;{{end}}
{{if .Full}}- the failure mode the substitute cannot reveal.{{else}}- failure mode substitute cannot reveal.{{end}}

{{if .Full}}Never add an interface only to mock something. It earns its place only when the
shipped boundary is meaningful on its own.

An existing concrete collaborator or test setup may falsify the contract at the
selected tier. Then say so: **do not add or widen `<seam/interface>`; test through
`<existing boundary>`**.{{else}}Never add interface only to mock something; earns place only when shipped boundary meaningful alone.
Existing concrete collaborator or test setup may falsify contract at selected tier -> say so: do not add or widen `<seam/interface>`; test through `<existing boundary>`.{{end}}

{{if .Full}}## Assert what the caller can observe{{else}}## Assert what caller can observe{{end}}

{{if .Full}}Compare the complete normalized result or state: structured output, persisted
state, emitted events, relevant side effects. Do not stop at one field, a success
boolean, or a call count when the contract is richer.

Normalize values that vary between runs at the boundary (temporary paths, timestamps,
unordered traversal, generated IDs), and assert volatile invariants separately.
Never normalize ordering, errors, or state changes the contract includes. Internal
setup fits only a narrow owned unit invariant.{{else}}Compare complete normalized result or state: structured output, persisted state, emitted events, relevant side effects.
Do not stop at one field, success boolean, or call count when contract is richer.
Normalize values varying between runs at boundary (temporary paths, timestamps, unordered traversal, generated IDs); assert volatile invariants separately.
Never normalize ordering, errors, or state changes contract includes.
Internal setup fits only narrow owned unit invariant.{{end}}

{{if .Full}}## Name and record the proposed case{{else}}## Case name{{end}}

{{if .Full}}Propose a case name in the repository's existing idiom; never impose a universal
pattern or borrow one from another language. Record the case as:{{else}}Case name in repository's existing idiom; never impose universal pattern or borrow from another language. Record case as:{{end}}

```text
<proposed repository-idiomatic case name>
  trigger/precondition -> complete observable result/state -> visible side effects
  volatile invariants asserted separately
```

{{if .Full}}Hand language- or framework-specific mechanics to the applicable language guidance
(syntax, helpers, fixture setup, naming). This skill keeps the boundary,
collaborator, and observable-contract advice language agnostic.{{else}}Language- or framework-specific mechanics -> applicable language guidance.{{end}}

{{if .Full}}## Deliver the recommendation{{else}}## Deliver{{end}}

{{if .Full}}Report, for each behavior:

1. Observable contract and crossed system boundary.{{else}}Each behavior, report:
1. Observable contract, crossed system boundary.{{end}}
2. Evidence used, including index or runtime gaps.
{{if .Full}}3. Recommended tier and why it can falsify the contract.{{else}}3. Recommended tier, why it can falsify contract.{{end}}
4. Collaborator matrix: `collaborator -> real/fake/stub -> reason -> contract check`.
{{if .Full}}5. What the test asserts in full, after normalizing, and the volatile invariants separately.{{else}}5. What test asserts in full after normalizing; volatile invariants separately.{{end}}
{{if .Full}}6. Proposed repository-idiomatic case name and its explicit case record.{{else}}6. Proposed repository-idiomatic case name, explicit case record.{{end}}
{{if .Full}}7. Whether an existing boundary suffices, or the independent reason the shipped code
   needs a new or wider one.{{else}}7. Whether existing boundary suffices, or independent reason shipped code needs new or wider one.{{end}}
{{if .Full}}8. How it runs: prerequisites, whether a developer reproduces it locally, cost, and
   the observed target or invocation.{{else}}8. How it runs: prerequisites, whether developer reproduces it locally, cost, observed target or invocation.{{end}}
{{if .Full}}9. Owning Magus test target, final affected-CI route, and complementary coverage.{{else}}9. Owning Magus test target, final affected-CI route, complementary coverage.{{end}}

{{if .Full}}Hand the run to `{{skill "run"}}`. This skill chooses the proof; it never bypasses
Magus with raw language test commands.{{else}}Run -> `{{skill "run"}}`. Skill chooses proof; never bypasses Magus with raw language test commands.{{end}}{{if .Full}}

## Evidence gate for delegated work

Do not label a test unit, integration, or end-to-end until the behavior and at
least one system boundary are named. If a selected target, reference result, or
runtime fact is unavailable, the recommendation is **provisional**. Name the
missing evidence and one concrete next action; do not fill the gap with a
guess.{{end}}{{if .Full}}

Do not claim a fake is protected by real-boundary coverage unless you name that
test and its owning Magus target. If no such test exists, the plan
must include it as complementary coverage.{{end}}{{if .Full}}

### Preservation gate

Before proposing a seam, interface, or widened substitute point, identify the
existing concrete collaborator or test setup that was considered. If it
can falsify the contract at the selected tier, preserve it and state:
**do not add or widen `<seam/interface>`; test through `<existing boundary>`**.
Only recommend a new or wider seam when the shipped boundary has an
independent reason to exist; a test double alone is not that reason.{{end}}{{if .Full}}

### Execution-policy gate

Record the actual prerequisites and cost before suggesting a run scope. A
model may suggest an explicit target, opt-in path, or broader gate, but only with
that evidence behind it. It may not present a policy choice as settled, assume
credentials are available, or create/modify external configuration to make a test
run.{{end}}{{if .Full}}

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
```{{end}}{{if .Full}}

The tiers are not a speed ranking. Move outward only when the inner boundary
cannot produce the failure the contract describes.{{end}}{{if .Full}}

### Common false confidence{{end}}{{if .Full}}

| Shape | Why it misleads | Better proof |
| --- | --- | --- |
| Mock asserts a call count | proves a call pattern the test chose, not the result a caller receives | assert the complete result; cover the real call path at integration scope |
| Snapshot hides volatile data | can bless a change without saying which values matter | normalize only what legitimately varies and assert the remaining structure |
| Fake service mirrors shipped logic | duplicates the system under test and drifts | use the real local component or add a named contract/integration test |
| E2E checks only success | proves the workflow exited, not that it produced the required state | assert the full observable result and durable effects |{{end}}{{if .Full}}

### Fill-in report{{end}}{{if .Full}}

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
{{end}}
