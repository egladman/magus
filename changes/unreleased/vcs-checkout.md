### Changed

- **Breaking: `job fork --checkout` runs the `checkout.bootstrap` hook from `magus.yaml`.**
  It no longer runs `hack/dev/bootstrap-checkout.buzz` by name. A workspace that relied on
  that path sets `checkout.bootstrap: hack/dev/bootstrap-checkout.buzz`. Without it a new
  checkout takes a copy of the forker's `./magus`, and the guard names no build line.
- **`magus vcs checkout` checks out one revision for review.** It makes an isolated
  checkout under `$XDG_CACHE_HOME/magus/tmp/checkout-<short revision>`, fetching a ref such
  as `refs/pull/<n>/head` first with `--fetch`. It then runs each project's `install`
  target, `magus agent harness install` and the `checkout.bootstrap` hook, and prints the
  path and the `--remove` command. `--no-bootstrap` skips all three.
- **The guard reads a `magus.yaml` key its binary does not know as an out-of-date binary.**
  It refuses what changes state, says the binary predates `magus.yaml`, and lets the rebuild run.
