# Skill evals

This harness measures whether the full and simple permutations of a Magus skill
change agent behaviour. The `ci` target is lint-only by design, so it runs
offline without credentials. Live model runs need `ANTHROPIC_API_KEY`.

Run the small live sample with `magus run smoke evals`; run the full repeated
matrix with `magus run grid evals`.
