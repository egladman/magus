# libs/safetyscissors

The Go harness SDK for coding agents described in
[ADR 0050](../../docs/decisions/0050-the-harness-is-a-module-named-safetyscissors.md).
No footguns: it works fully and you can't hurt yourself.

<img src="assets/gen/safetyscissors.svg" width="160" alt="Half-open safety scissors in charcoal on a caution-yellow square.">

## Use

`safetyscissors.New` opens the job store and the session writer on one directory, and
takes the rest as options. A part left out stays absent, and its accessor returns an
`*UnconfiguredError` naming the option to pass.

```go
func main() {
    // On Linux a confined child is this binary re-executed, so Relaunch runs first.
    if handled, err := sandbox.Relaunch(os.Args); handled {
        log.Fatal(err)
    }
    // ...
}

func run(ctx context.Context) error {
    ss, err := safetyscissors.New(ctx, stateDir,
        safetyscissors.WithInvocation(id, session.InvocationStart{Command: "editor"}),
        safetyscissors.WithActor(func() job.Actor { return job.Actor{} }),
        safetyscissors.WithNotices(func(n job.Notice) { log.Print(n) }),
        safetyscissors.WithSandbox(checkout, binds),
        safetyscissors.WithSlots(4),
    )
    if err != nil {
        return err
    }
    cmd, err := ss.Command(ctx, lease, "go", "test", "./...")
    if err != nil {
        return err
    }
    return cmd.Run()
}
```

`stateDir` holds `jobs/`, `sessions/` and the activity log's `activity/`; `WithDirs`
moves any of them. Every subpackage stays importable on its own.

## Mark

`assets/mark.buzz` draws the mark from geometry: kids' safety scissors in charcoal on
caution yellow, half open and leaning, flat like buzzed's pull-tab icon.
`magus run generate libs/safetyscissors` writes `assets/gen/safetyscissors.svg`, a
plain square with square corners, and `assets/gen/safetyscissors-app-icon.svg`, a
rounded app icon with its shadow.
