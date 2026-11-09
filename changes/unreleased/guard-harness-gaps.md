### Fixed

- **The guard records target policy when it opens the workspace.** A checkout no sync has
  reached opened its whole workspace on every `magus run` judgment, and most opens ran past
  the time budget. The guard now writes what it read to `target-policies.jsonl` in the
  knowledge store, and later judgments answer from it until the sources move.
- **The stale-binary deny names the magus.yaml key it predates.** It says which file sets which
  key and the judging binary's version. A key in the user-level magus.yaml is named as such, and
  the deny says to remove it or judge with a newer magus, since rebuilding the checkout does not
  change that file.
- **An init whose `--root` names another workspace is not judged by this one's binary.**
