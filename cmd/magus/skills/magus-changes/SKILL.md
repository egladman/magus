# Recent changes in a magus workspace

Turn a large workspace's recent change history into a short, evidence-backed
brief. The output is a decision aid, not a chronological commit dump.

## Gather evidence

1. Get the project map and target vocabulary from the workspace: `magus ls`
   for projects, `magus describe targets` for the target vocabulary. Do not
   read `MAGUS.md` for this - it is a generated index for human readers, and
   a history brief that describes stale structure is worse than none.
2. Establish the requested time boundary. On Git, inspect merge commits first:

   ```sh
   git log --first-parent --merges --since="<window>" --format='%h %ad %s' --date=short
   ```

   If no VCS merge history is available, say so. Use `magus insight trend` and
   `magus insight hotspots --files` for activity, but do not call that a merge summary.
3. For each candidate change, list its files, then classify them before reading:

   ```sh
   git show --format= --name-only <commit>
   magus describe file <paths...>
   ```

   Ignore generated outputs when identifying the change; trace them to their
   declared source and generator instead.
4. Map the source files to projects and graph entities. Prefer MCP
   `magus_query`, `magus_explain`, and `magus_describe_file`; otherwise use:

   ```sh
   magus query "<project or feature terms>"
   magus explain <node>
   magus graph diff --rev <base> -o markdown
   ```

5. Use `magus insight affinity`, `ownership`, and `trend` only to add context:
   hidden coupling, ownership risk, or unusually rising activity. They do not
   prove that a feature landed.

## Write the brief

Lead with three to seven grouped changes, not every commit. For each item state:

- **What landed** - a plain-language feature or behavioral change.
- **Where** - projects and graph entities affected.
- **Evidence** - merge commit(s), source files, and the relevant graph relation.
- **Why it matters** - user impact, dependency impact, or an explicit uncertainty.
- **Follow-up** - a concrete next command when more detail is useful.

Use this shape:

```markdown
## Recent changes since <boundary>

### <feature or change>

<one-sentence outcome>

- Projects: `<project>`
- Evidence: `<commit>`; `<graph node or relation>`
- Follow up: `magus explain <node>`

## Watch items

- <hidden affinity, ownership, or trend signal - or "None found.">
```

Do not label a refactor, generated-output refresh, dependency bump, or failed
experiment as a landed feature unless the source and graph evidence support it.
Link to the relevant documentation page or generated manpage when it explains a
new command, target, diagnostic, or workflow.
