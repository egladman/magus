### Added

- **proofread judges code comments.** The `code-comment` kind budgets each Go,
  TypeScript and Buzz comment by its place and denies history, dates, doc paths,
  shouting and first person. The edit hook judges the lines a write adds, and a
  lint holds whole files to per-project baselines in `analyzers.yaml`.
