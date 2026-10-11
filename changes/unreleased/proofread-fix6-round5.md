### Changed

- **proofread blanks the prompt a coding agent quotes into its pull request.** The original
  prompt Copilot's coding agent folds under its suffix comment, the prompt from Copilot chat,
  a pasted Copilot summary citing diff hunks, the Ellipsis summary, and a template's category
  checkbox (`- [ ] Public API: Breaking change (...)`) are blanked, words and all, before any
  rule reads the text.
- **`participles` counts clauses only.** An -ing word opening a bare item of a series, the
  subject of a verb (`caching is`) or a bold list label is no longer counted.
- **`nonspecific` takes a failure you can find as the reason.** A review comment naming
  failing tests, a failed CI job, a command that fails or code that does not compile is left
  alone; one that names nothing ("something went wrong, please try again") is still reported.
