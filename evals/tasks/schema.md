# Task schema

Each task file describes one realistic user turn. Required fields are `id`,
`skill`, `smoke`, `prompt`, and `constraints`. `constraints` is a list of
predicate names from `evals/lib/constraints.mjs` with their arguments.

```yaml
id: vcs-classify-before-reading
skill: magus-vcs-hygiene
smoke: true
prompt: |
  Tell me what changed on this branch.
constraints:
  - fn: usedSkill
    args: [magus-vcs-hygiene]
  - fn: classifiedBeforeReadingDiff
    args: []
  - fn: neverRan
    args: [STAGE_EVERYTHING, stage-everything]
```
