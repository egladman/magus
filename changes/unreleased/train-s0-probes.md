### Added

- **`libs/train` pins the candidate code models and measures them on this machine.**
  `inputs.jsonl` pins six bases and their GGUFs by revision and sha256. The `models`,
  `fertility`, `pilot` and `gguf-bench` targets fetch them, count tokens per byte, run
  200-step mlx-lm pilots and llama-bench. Each run appends a row to `runs.jsonl` with peak
  memory and load average.
