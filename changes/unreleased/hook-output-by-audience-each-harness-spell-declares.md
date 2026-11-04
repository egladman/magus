### Changed

- **Each harness spell declares its reply channels.** `mgs_getLimits` returns `channels`, in
  the logger's words: `display` (the person and the agent read it), `capture` (the agent
  alone) and `notice` (the person alone). A harness with hooks that declares none, or names
  another, fails to load. A host with no `capture` channel, OpenCode today, gets each deny as
  its verdict line alone.
