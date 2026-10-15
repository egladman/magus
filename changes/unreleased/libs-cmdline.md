### Added

- **`libs/cmdline` declares a command tree as data and renders it.** Its `generate` package writes the flag binders, man pages, Markdown pages, completion lists and API lock that magus's own tree uses, so another tool can declare its commands the same way.

### Changed

- **The man pages call `run` and `affected`'s targets OPERANDS, and `api.lock` lists them as `operand` lines.** The man page's global flags render from the same declaration as every command's options.
