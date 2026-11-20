### Changed

- **Breaking: installed hook edits no longer change execution.** Editing or
  symlinking a file under `.magus/harness/hooks/` no longer changes normal hook
  execution. Run the authored template's source path to test edits.
- **Breaking: harness description returns typed plans or an error.**
  `magus\describe.harness()` returns declared plans; a named provider returns a
  one-element list. No declared providers is an error. Scoped install and verify
  methods use Magus-specific options and reject `allowFailure`.
  `magus\CommandOptions.binary` selects the executable without parsing command
  output.
