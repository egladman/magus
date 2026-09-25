### Changed

- **The guard judges a script by its content.** `bash x.sh`, `python3 x.py` or `./x.sh`,
  and a write of such a file, get the busy-wait, scripted-rewrite, cd, output-pipe,
  output-redirect, capture-filter and unknown-env verdicts the script's lines would get
  typed inline. The refusal says the script was read.
