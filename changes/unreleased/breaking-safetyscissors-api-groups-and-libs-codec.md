### Changed

- **Breaking: the records `libs/safetyscissors` writes carry its own API groups.** Job
  rows, filed results, steps, the guard's envelope and session records are
  `safetyscissors.job`, `job-result`, `step`, `verdict` and `session`, each
  `v1alpha1`. Nothing reads the `magus.*` groups they replace.
- **Breaking: a store holding a retired group is refused.** A job store says to move it
  aside and fork again; a session store is a `*session.RetiredGroupError` saying to
  delete the file. An older OpenCode plugin refuses every call until re-installed.
- **Breaking: `magus session -o json` keys an invocation's model `model`.**
  `session.Summary.Producer` is `Summary.Model`.
- **Breaking: Cursor's glue asks where it allowed.** Every gating event no guard judged
  answers Cursor's `ask`. Only `subagentStart`, which only records a spawn, still answers
  `allow`.
- **Breaking for SDK callers: `libs/codec` replaces `internal/json`.** A build of the magus
  package without `GOEXPERIMENT=jsonv2` fails with an undefined name that says to set it;
  `libs/codec` builds either way.
