### Changed

- **Breaking: the guard never answers yes; its decisions are `defer`, `advise`, `ask` and
  `deny`.** `defer` replaces `pass` and `allow`: the host's own permission flow decides.
  The envelope is `safetyscissors.verdict/v1alpha1` and the hook templates are version 37.
- **Breaking: the hook glue writes no permission decision for a defer.** Cursor's glue
  answers it with Cursor's `ask`. On Codex a push a gate covers and a `human_only` run now
  prompt the person. A template older than 37 refuses every call until re-installed.
- **Breaking: `magus\guard.allow()` is `magus\guard.defer()`, and `GuardDecision.allow` is
  `GuardDecision.defer`.** A workspace rule may return `magus\guard.ask(text)`, which waits
  on the person through the host's prompt and is refused where the wiring cannot prompt.
- **Breaking for SDK callers: `verdict.Allow` is `verdict.Defer`, and `verdict.Ask` is
  new.** Judgments and session agent events carry the typed `verdict.Decision`;
  `session.VerdictPass`, `VerdictAdvise` and `VerdictDeny` are gone.
- **Breaking: a session or activity store holding `pass` or `allow` is refused.** The error
  names the file or store. Delete the session file and run `magus session load` again, or
  remove the activity directory; no reader translates the old names.
