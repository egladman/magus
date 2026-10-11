### Added

- **A Claude Code mod, installed from this repository's plugin marketplace.**
  `/plugin install magus --marketplace egladman/magus` adds a status line and band
  naming magus problems, a `/magus` pane of jobs, runs and subagents read over the
  daemon's Connect API, and prompt-cache expiry warnings priced from an editable rates
  table.
- **Cost controls in the Claude Code mod.** A price step gate warns or holds a prompt
  when the context nears a model's prompt-size price step, a subagent gate warns or
  refuses a spawn whose model costs more per input token than a cap (2 USD per MTok
  by default) and names `magus-scout` or `model: haiku`, and the session's spend
  shows in the status line and pane against an optional `sessionBudgetUsd`.
