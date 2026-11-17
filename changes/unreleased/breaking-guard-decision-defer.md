### Changed

- **Breaking: the guard never answers yes; its decisions are `defer`, `advise`, `ask` and
  `deny`.** `defer` replaces both `pass` and `allow`, and means no objection: the host's
  own permission flow decides the call. The envelope is `magus.guard/v1alpha2`,
  `magus shell` prints `defer` where it printed `pass`, and the hook templates are
  version 37. Claude Code, Codex and OpenCode glue write no permission decision for a
  defer, and Cursor's glue answers it with Cursor's `ask`, never `allow`. On Codex a
  push a gate covers and a run of a `human_only` target now prompt the person, where
  the template used to approve them. A template older than 37 meets `defer` as an
  unknown decision and refuses every call until it is re-installed.
- **Breaking for Buzz rules: `magus\guard.allow()` is `magus\guard.defer()`, and
  `GuardDecision.allow` is `GuardDecision.defer`.** A workspace rule may now return
  `magus\guard.ask(text)` or `GuardDecision.ask`, which waits on the person through the
  host's prompt and is refused where the wiring cannot prompt.
- **Breaking for SDK callers: `verdict.Allow` is `verdict.Defer`, and `verdict.Ask` is
  new.** `Judgment.Decision`, `session.AgentEvent.Verdict` and the session cost and
  activity readers carry the typed `verdict.Decision`; `session.VerdictPass`,
  `VerdictAdvise` and `VerdictDeny` are gone. `trail.ForSession` returns an error.
- **Breaking for stored records: a session store or an activity store holding `pass` or
  `allow` is refused** with an error naming the file or the store. Delete that session
  file, or clear the store, and run `magus session load` again; for the activity store,
  remove its directory or start a fresh session. No reader translates the old names.
