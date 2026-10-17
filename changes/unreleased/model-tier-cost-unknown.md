### Fixed

- **The model-tier lens reports an unrecorded cost and token count as unknown, not 0.** Nothing filled what a job used, so every job outcome carried `0` for both and the lens showed every tier as free.
