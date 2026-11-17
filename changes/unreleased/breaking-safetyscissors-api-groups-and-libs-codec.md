### Changed

- **Breaking: the records `libs/safetyscissors` writes carry its own API groups.** A job
  row is `safetyscissors.job/v1alpha1`, a filed result `safetyscissors.job-result/v1alpha1`,
  a step `safetyscissors.step/v1alpha1`, the guard's judgment envelope
  `safetyscissors.verdict/v1alpha1` and a session store record
  `safetyscissors.session/v1alpha1`, where they were `magus.job`, `magus.job-result`,
  `magus.step`, `magus.guard/v1alpha2` and `magus.sessions`. Nothing reads the old groups.
  A job store holding one is refused naming the store and saying to move it aside and fork
  the jobs again; a step is refused naming the version to send; a session store holding
  one is a `*session.RetiredGroupError` naming the file, which says to delete it or clear
  the store and start a fresh session. An OpenCode plugin from before this release meets
  the new envelope as a version it does not know and refuses every call until it is
  re-installed.
- **Breaking for `magus session -o json` readers: an invocation's model is keyed
  `model`.** `session.Summary.Producer` is `Summary.Model`, and its key `producer` is
  `model`.
- **Breaking: Cursor's glue asks where it allowed.** Every gating event no guard judged,
  an empty command, a `Task` with no prompt, an MCP call left to `beforeMCPExecution`, a
  hook event that is not JSON and the repair magus names while it cannot judge, answers
  Cursor's `ask`. Only `subagentStart`, which only records a spawn, still answers `allow`.
- **Breaking for SDK callers: `libs/codec` replaces `internal/json`**, as the codec note
  says, and a build of the magus package without `GOEXPERIMENT=jsonv2` fails to compile
  with an undefined name that says to set it. `libs/codec` itself still builds either way.

### Fixed

- A request blob in the activity store that does not decode is named on `magus diff`'s
  notes instead of dropping out of the agent record unsaid, and a session store the
  review cannot read is named there too.
- Every magus JSON Lines store reports a line that does not decode as MGS3050 and a store
  an older magus left beside it as MGS3049, not only the job, forecast and log stores.
