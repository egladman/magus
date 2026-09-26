### Security

- **Breaking: every merge-queue hook keeps its caches in its candidate's own home.** A
  gate or regeneration gets `HOME`, the XDG directories, `TMPDIR` and magus's cache
  inside the candidate's box, and the queue refuses a hook whose sandbox could write
  outside it. `--scratch-env` is removed with nothing to replace it; drop the flag.
