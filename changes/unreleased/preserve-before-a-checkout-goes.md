### Changed

- **`magus vcs checkout --remove` preserves a dirty checkout first.** The uncommitted work is
  captured under `refs/magus/preserved/`, the handle is printed, and the capture is recorded in
  `captures.jsonl` under the state directory. A tree already captured unchanged is not captured
  again. A capture that fails stops the removal.
- **BREAKING: a preserved capture no longer expires after 30 days.** `prune-preserved` drops a
  git capture only once every change in it reads the same in the base. Mercurial and Sapling
  captures are never pruned, since neither can prove that.
