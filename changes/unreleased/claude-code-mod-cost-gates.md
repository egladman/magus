### Added

- **Cost controls in the Claude Code mod.** A price step gate warns or holds a prompt
  nearing a model's prompt-size price step; a subagent gate warns or refuses a spawn
  whose model costs more per input token than a cap and names `magus-scout`; session
  spend shows in the status line against an optional `sessionBudgetUsd`.
