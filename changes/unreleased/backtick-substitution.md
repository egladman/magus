### Added

- **The agent guard refuses a backtick command substitution.** Inside double quotes a
  backtick runs a command, so a literal backtick in a pattern pairs with the next one and
  swallows everything between, file operands included. The deny names the fixes: `$(...)`
  for a substitution, single quotes for a literal backtick.
