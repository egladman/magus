### Security

- **A merge queue kick-back shows what validation reported as text.** The claim, file
  names and commits render as code spans or fences no backtick run can close, and the
  facts line escapes every character that could end its HTML comment. The reproduce
  lines come from `magus queue apply --reproduce-gate` and `--reproduce-regenerate`,
  never from a verdict.
