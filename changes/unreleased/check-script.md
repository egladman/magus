### Added

- **A job's check can name a script.** `"check": {"script": "probes/key.buzz"}` on a job
  or a check goal is satisfied by a passing `magus buzz --record probes/key.buzz` run, so a
  fact no target checks can still close a job with evidence anyone can reopen.
