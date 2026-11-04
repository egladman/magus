### Changed

- **The push gate counts the run that wrote a generated output.** A changed file no
  target reads, such as a regenerated `.jsonl` or man page, is covered by a passing run of
  a target whose declared outputs hold it, when the file matches the pushed commit. A
  refusal names that target's run instead of sending you to a full `affected ci`.
