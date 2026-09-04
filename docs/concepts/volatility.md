---
title: Volatility
description: How magus tells a volatile failure from a real regression using per-target history and a Wilson-score volatility rate, when it auto-retries, and the tools for chasing down a genuine intermittent break.
tags: [volatile, volatility, retry, regression, bisect, race, ci, debugging]
aliases: [flaky-builds]
---

# Volatility

A test that fails once and passes on rerun is volatile; a test that started failing
and stays failing is a regression. Telling them apart by hand is guesswork, so
magus keeps per-target pass/fail history and decides statistically. This page is
the decision it makes and the tools for the cases it hands back to you.

## How magus decides a failure is volatile

Volatility detection is on by default (`volatility.enabled`) and applies to targets
that opt in with the `retry_on_volatile` policy. On a failure, magus looks at that
target's history:

- **Bootstrap phase.** Below `volatility.bootstrap_samples` outcomes (default 20),
  there is not enough history to judge, so magus retries every failure once.
- **Scored phase.** With enough history (`volatility.min_samples`), magus computes a
  Wilson-score volatility rate and retries when it exceeds `volatility.threshold`. A
  stable target that suddenly fails is _not_ retried - that looks like a regression.
- **Unaffected prior.** A failure in a project the diff did not touch carries a
  strong prior on volatility (its code did not change), so magus leans toward retry.

If the retry passes, the outcome is recorded as volatile and the run continues. If it
fails again, magus flags a **suspected regression** and stops treating it as noise.

<!--diagram:volatility-->

## When it is a real regression

A suspected regression means the failure survived a retry and does not look like
noise. Find the commit that introduced it with VCS bisect driven by run history:

```sh
magus affected --bisect ./apps/myapp
```

magus uses recorded outcomes to seed the known-good end (`--good` overrides it) and
bisects the target across commits until it isolates the break. See
[affected.md](workspace/affected.md#forensic-modes).

## Chasing a genuine intermittent break

When a failure is real but only sometimes, narrow the cause:

- **Data races** - `magus run test --race` enables magus's own race diagnostics
  (MGS4001-4004), not the language toolchain's race detector. It always runs the
  target fresh (never a cache replay) and watches for concurrent-write conflicts
  and non-deterministic output. A race is the most common source of "passes
  locally, fails in CI."
- **Order and isolation** - run the single target alone (`magus run test api`) and
  compare to the full run. A difference points to shared state or test ordering.
- **Under-declared inputs** - if a target passes fresh but fails from cache (or the
  reverse), its `needs`/`provides` may be wrong, so the cache replays a stale
  result. `magus describe target <path:target>` shows the declared inputs; see
  [cache.md](cache.md).
- **Disable retry to see raw behavior** - `magus run --no-volatility-retry` (and
  `magus affected --bisect` internally) runs without the retry cushion so you
  observe the failure directly.

## Configuration

The `volatility.*` keys tune detection; see the [config reference](../reference/config.md#volatility).
`retry_on_volatile` is a per-target policy declared in `magus.project` (see
[workspace.md](workspace.md)), so a workspace opts specific targets (usually `test`)
into retry rather than the whole tree, and a sibling target in the same run is
unaffected by a neighbor's opt-in:

```buzz
magus\project({
    "targets": {
        "integration": { "retry_on_volatile": "talks to a shared broker that drops a connection under load" },
    },
});
```

The value is a reason string, and a bare `true` is a load error: a retry policy claims
this target fails without the code being wrong, which is not something the next reader
can infer from a bool. Every retry is logged as a warning naming the project, target,
status, reason and score, whatever the host; `volatility.annotate_gha` additionally
surfaces retried and regression outcomes as GitHub Actions annotations.

## See also

- [affected.md](workspace/affected.md) - the `--bisect` regression hunt.
- [cache.md](cache.md) - why an under-declared input reads as volatility.
- [debugging.md](../guides/debugging.md) - the interactive REPL and `magus\pry` breakpoints.
