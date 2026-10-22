### Security

- **The agent guard refuses a command it cannot read.** A program or VCS subcommand the
  shell computes first (`\git`, `$'g\x69t'`, `{git,push}`, `gi?`, `$x`, `$(echo git)`)
  ran unjudged; `unread-command` now asks for it spelled plainly. Quotes that change
  nothing and expansions in arguments still pass.
