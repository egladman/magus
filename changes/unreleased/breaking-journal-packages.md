### Changed

- **Breaking for SDK callers: the journal is one package,
  `libs/safetyscissors/journal`.** It holds `Record`, one line of an invocation's
  journal, and `Call`, one line of the activity log, with their readers `Records`,
  `RecordsSince`, `ReadInvocation` and `Calls`. Each reader yields
  `iter.Seq2[T, error]`, and a `*codec.LineError` marks one line that did not decode.
- **Breaking for SDK callers: `libs/safetyscissors/trail` is gone for `journal`.**
  `trail.Event` is `journal.Call`, `Kind` is `CallKind`, `ReadRecent` is `Calls` over the
  `activity` directory itself, `ReadBlob` is `Payload.Read` and `ValidRef` is
  `ValidPayloadRef`. `InvalidReadError` and `InvalidRefError` are gone.
- **Breaking for SDK callers: `types/events` and `internal/eventstream` are
  `libs/safetyscissors/journal/stream`.** The names are kept, except
  `Body.StreamType` is `EventType`, `ParseStreamEventType` is `ParseEventType`,
  `(*Writer).Emit` is `Write` and `Replay` takes a context. The wire is unchanged.
- **Breaking for SDK callers: `internal/journal` and `internal/trail` are gone.** The
  writers are in `internal/recorder`: `Append` is `AppendCall`, `WriteBlob` is
  `WritePayload`, `Rotate`, `Stat` and `LastRun` are `RotateCalls`, `StatCalls` and
  `LastJob`, and `Emit` hands each `Sink` a `journal.Record`. `recorder.Calls` and
  `ReadPayload` read the activity log under a cache base. The session views are in
  `internal/sessions`: `SessionTrail` is `Activity`, `ForSession` is `ActivityHere`,
  `SessionCost` is `Cost` and `WithInvocationJournal` is `WithInvocationFacts`.
- **Breaking for SDK callers: `(*SafetyScissors).Trail` is `Calls`.** It returns
  `iter.Seq2[journal.Call, error]` over `journal.Calls`, and `Dirs.Trail` is
  `Dirs.Activity`, the `activity/` directory itself rather than the one that holds it.
  `New` still places it at `<dir>/activity`.
- **Breaking for SDK callers: `internal/handler/trailrpc` is `activityrpc`.**
- **The activity readers return what they cannot read.** `sessions.ActivityHere`,
  `recorder.Consulted`, `ObservedCounts` and the review's replay answer a store read
  error instead of an empty result, and `ActivityHere` reports absence as `false`.
