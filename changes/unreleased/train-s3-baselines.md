### Added

- **`libs/train` scores the untuned code models.** `eval-complete`, `pass-at-k` and
  `latency` fork a pinned llama-server over each base GGUF, then score FIM completion,
  Buzz pass@k and typing-order latency. Each metric lands in `libs/train/evals.jsonl` with
  a 95% interval and a clustered p-value, each run in `runs.jsonl` with its load average.
