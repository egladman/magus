### Added

- **Buzz scripts can confirm, multi-select, search live and show a spinner.** The `term`
  module gains `term\confirm`, `term\pickMany` and `term\spin`, and `term\pick` takes a
  `query` called on every keystroke. Each raises when standard input and standard error
  are not both terminals, and a cancel raises. `libs/termkit` gains `pick.Many`,
  `pick.RenderMany` and `progress.StartSpinner`.
  [Terminal](https://eli.gladman.cc/magus/concepts/terminal/).
- **`hack/lib/prompt.buzz`** takes a flag's value, else prompts on a terminal, else fails
  naming the flag, and the lint `prompts-stay-interactive` keeps every prompt behind a
  `term\isInteractive()` guard in a script's `main`.
- **The `term\pick` example names the flag it needs.** With no terminal, or on a cancel,
  it says `--project` is required where it used to fall back to the first item.
