# Skill evals

This harness measures whether the full and simple permutations of a Magus skill
change agent behaviour. The `ci` target is lint-only by design, so it runs
offline without credentials.

Everything runs through magus targets; there is no tool here that wants to be
invoked by hand:

- `magus run lint evals` validates every task file and runs the node tests.
- `magus run smoke evals` runs the small live sample; `magus run grid evals`
  runs the full repeated matrix. Both are muted right now (see magusfile.buzz)
  and both resolve `ANTHROPIC_API_KEY` through `magus\secret.read`, so in CI the
  workflow maps the repository secret onto that variable and on a laptop you
  export it (or select a provider spell).
- `magus run analyze evals -- results/smoke.json` reduces a results file to the
  paired full-vs-simple comparison table.
- `magus run measure evals` reports compression stats for the embedded skills;
  `magus run measure evals -- --json` regenerates the committed baseline
  snapshot (`baseline-<date>.json`).
