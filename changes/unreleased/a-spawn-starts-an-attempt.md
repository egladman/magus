### Changed

- **Breaking: a job's tries live in `attempts.jsonl`, beside `jobs.jsonl`.** Store
  migration 3 moves a row's `result`, `prior` and `attempts` there. `magus ls jobs -o
  json`, `magus describe job -o json` and the Buzz `job.list` value carry the tries as
  `attempts`. An older magus cannot read a store this one migrated.
- **Breaking: a spawn naming a row starts its try.** The guard records the start as it
  lets the spawn or continuation through. The handback, the host's `SubagentStop` and
  `magus job exit` end it, and the row's deadline bounds it. A try with no end observed
  reads as "started, end unobserved", never as nothing running.
- **Breaking: `StopRequest.Tasks` and `TasksUnknown` are gone.** The stop rule no longer
  reads the host's transcript for the subagents it launched. A stop rule reads the tries
  that started and have not ended.
  [ADR 0044](https://eli.gladman.cc/magus/decisions/0044-a-job-may-carry-an-agent/).
