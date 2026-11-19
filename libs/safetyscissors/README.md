# libs/safetyscissors

<img src="assets/gen/safetyscissors.svg" width="48" alt="Half-open safety scissors in charcoal on a caution-yellow square.">

The records and refusals around a unit of work, as a Go library: who holds it, what was
declared, whether it is done and what it cost.

A unit of work is whatever your program hands out: a build step, a code change, a
ticket. The library keeps its records as JSON Lines files under one directory you name.
It needs no magus binary, no workspace and no version control.
[ADR 0050](../../docs/decisions/0050-the-harness-is-a-module-named-safetyscissors.md)
is the design. The module is pre-1.0 and breaks its API on purpose.

## Use

`safetyscissors.New` opens the job store and this invocation's session writer under
one directory. It refuses to open unless its `Config` sets every field:

- `Dir` holds the stores.
- `Invocation` names the invocation this process records sessions as, and `Start` is
  its start record. The module mints no id.
- `Actor` says who the job store writes as.
- `Notices` hears what the store did that no call asked about. The module never
  prints.
- `CheckSpec` validates, compares and renders your checks. The store keeps a check
  as opaque JSON and never reads it.
- `Retention` is how long a session file outlives its newest fact, and `PruneErrors`
  hears what the prune that opening runs could not do. A failed prune never fails the
  open.

```go
ss, err := safetyscissors.New(ctx, safetyscissors.Config{
    Dir:         dir,
    Invocation:  "inv1",
    Start:       session.InvocationStart{Command: "example"},
    Actor:       func() job.Actor { return job.Actor{} },
    Notices:     func(job.Notice) {},
    CheckSpec:   keptChecks{},
    Retention:   session.DefaultRetention,
    PruneErrors: func(ctx context.Context, err error) { slog.WarnContext(ctx, "prune", "err", err) },
})
if err != nil {
    return err
}
check, err := job.NewCheck([]string{"go", "test", "./auth/..."})
if err != nil {
    return err
}
row, err := ss.Jobs().Update(ctx, "fix-login", func(j *job.Job) {
    j.Parent = "root"
    j.Paths = job.Paths{Write: []string{"auth/**"}}
    j.Check = check
})
```

`keptChecks` is the smallest `job.CheckSpec`: a check is whatever JSON it is, compared
as written. `ExampleNew` in [safetyscissors_test.go](safetyscissors_test.go) runs this
code with its output checked.

`Dir` holds `jobs/`, `sessions/` and `activity/`; `WithDirs` moves any of them.

Every other part stays absent until you pass its option. Its accessor then returns an
`*UnconfiguredError` naming the option.

| Option        | Part                                   | Accessor                |
| ------------- | -------------------------------------- | ----------------------- |
| `WithSlots`   | a slot limiter                         | `Slots`                 |
| `WithMachine` | a machine-wide memory and slot budget  | `Machine`               |
| `WithTiers`   | the model tier set you declare         | `Tiers`, `SpawnCeiling` |
| `WithSandbox` | confinement of each child to its lease | `Sandbox`, `Command`    |

On Linux a confined child is your binary run again, so `main` calls
`sandbox.Relaunch(os.Args)` first.

## Packages

Each package imports on its own. A program that needs only verdict imports only
verdict.

| Package   | Holds                                                                                       |
| --------- | ------------------------------------------------------------------------------------------- |
| `job`     | the declared row with its `Check` and `Postcondition`s, the store, attempts and grading     |
| `budget`  | the slot limiter, machine-wide memory and slot claims, and the `Price` and `Pricer` types   |
| `verdict` | what a rule answers about a call (defer, advise, ask, deny) and the `Stricter` merge        |
| `step`    | one thing a program is about to do, as data, and the spec of each kind                      |
| `tier`    | the model tier set, a job's assignment and the spawn ceiling                                |
| `session` | the append-only record of what each invocation did, its fold and its pruning                |
| `journal` | the activity log of calls, one invocation's records, and the events in `journal/stream`     |
| `sandbox` | read, write and exec confinement narrowed to a lease, refusing a host that cannot confine   |

The store grades a postcondition from a run it witnessed, a run a signed payload
attests, or a run the caller reports.

## What it refuses

- No driver. The module has no agent loop, no model client and no scheduler. It records
  and grades; your program decides what runs next and starts it.
- No default allow. No `verdict.Decision` says yes. A store with no actor, notices or
  check spec refuses every write, and a sandbox in its default mode refuses to start a
  child the host cannot confine.
- No prices. `budget.Price` and `budget.Pricer` carry rates your program supplies. The
  module ships none, and a rate of 0 means unknown, not free.
- No shipped catalogs. You declare the tiers and the checks; the module ships no model
  list, tier set or rule set.

ADR 0050 part 7 has the full list.

## Not yet

- A rule table and a judge. `verdict` holds the answers and `step` the calls, but the
  module judges nothing; the caller writes its own rules.
- Lifecycle verbs. The store has `Update`, `Mutate`, `Grade`, `Witness`, `Attest` and
  `ReportRun`; taking and ending a job stay in magus.
- A spend limit. Nothing in the module consumes a `Price` yet.

## Mark

`assets/mark.buzz` draws the mark, and `magus run generate libs/safetyscissors` writes
it to `assets/gen/`.
