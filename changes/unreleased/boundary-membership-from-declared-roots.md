### Changed

- **Breaking: a job's worker type is spelled `resumed`, as the Buzz enum already was.**
  `{"worker": {"type": "resume"}}` is refused. `jobs.jsonl` migrates its stored rows
  once.
- **Breaking: a required field in a Buzz mirror has no default.** A literal of
  `magus\AgentWorker` must set `type`, one of `magus\AgentSpec` must set `worker`, and the
  queue answers, the image records and `DiscoveredProject` must set the fields their
  decoders require. A literal that leaves one out fails the check instead of decoding as
  the field's zero value.
- **Breaking: `.buzzgen.yaml` holds settings, not membership.** A Go type crosses into
  Buzz when a host signature, a provider op or a declared root reaches it. A line for a
  type nothing crosses as is an error, as are two crossing types that take one Buzz name.
