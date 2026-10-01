### Changed

- **A stale-index search refusal says why the index fell behind.** When the guard refuses
  a symbol search because the symbol index is older than the tree, the refusal now adds
  what magus observed (no server running, a sync in progress, a hook whose binary is
  missing) and the command that keeps the index current.
- **The Claude Code harness guards its Grep and Glob tools.** `magus describe harness
  claude-code` now wires a `Grep|Glob` PreToolUse entry, so a host search is judged as
  the `rg` or `find` line it stands for. Run the printed merge command to pick it up.
- **`magus\job.list` measures overlap footprints.** The typed call now reports each
  overlapping pair's footprint verdict, the same one `magus ls jobs` prints.
- **The `proc\exec` warning names the `magus\describe` methods.** Running the magus binary
  through `proc\exec` for `describe <noun>`, `ls` or `ls targets` now points at
  `magus\describe.<noun>`, `magus\describe.project` or `magus\describe.graph`.
- **`magus lsp` completes module members after a backslash.** Completion triggers on `\`
  and `/` instead of `.` and `/`.
- **Host errors name the call the way a script writes it.** Errors from `vcs\root`,
  `vcs\cmd`, `os\retry`, `proc\exec`, `magus\raise` and `magus\diff` begin with the
  backslash form instead of `vcs.root:` and the like.

### Fixed

- **A job row no longer stores `"no_default_charms": false`.** A check that does not opt
  out of the default charms leaves the field out, as it did before the field existed, and
  the job schema reads a field tagged `omitzero` as optional.
</content>
</invoke>
