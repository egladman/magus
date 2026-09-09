# Add a percentile helper to packages/platform/metrics

Add an exported function `percentile(samples, p)` to `packages/platform/metrics`,
using the nearest-rank definition:

- sort a copy of `samples` ascending; the caller's array must not be mutated
- take the element at index `Math.ceil((p / 100) * samples.length) - 1`, clamped to
  at least `0`
- return `0` when `samples` is empty

So `percentile([5, 1, 3, 2, 4], 50)` is `3`, `percentile([5, 1, 3, 2, 4], 100)` is
`5`, `percentile([5, 1, 3, 2, 4], 0)` is `1`, and `percentile([40, 10, 30, 20], 25)`
is `10`.

Several packages carry a `gen/api.md` summary listing the names they export and the
names they import from other packages. Those summaries have to end up consistent
with the source. They are produced by tooling in this repo; do not hand-edit them.

The tests in the platform packages must still pass.
