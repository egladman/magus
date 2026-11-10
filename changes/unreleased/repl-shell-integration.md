### Added

- **`magus buzz` marks its prompts for a hosting terminal.** With
  `MAGUS_SHELL_INTEGRATION=osc133` (or `shell_integration: osc133` in magus.yaml) the REPL
  writes OSC 133 marks around each prompt (`A`, `B`) and entry (`C`, `D;<status>`, 1 on an
  error) and reports its directory with OSC 7. Unset, the output is unchanged.
