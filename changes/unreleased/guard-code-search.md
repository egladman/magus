### Changed

- **A search for a symbol is refused even when the symbol index is behind the tree.** The
  guard classifies each alternative of a `grep`, `rg` or `git grep` pattern and refuses
  when any is a name the index defines, serving `magus refs` per name and `magus refs
  --text` per literal alternative. Every refusal names what it classified and why.
