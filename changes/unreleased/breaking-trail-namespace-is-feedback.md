### Changed

- **Breaking: the `magus\trail` namespace is `magus\feedback`.** A script imports
  `"magus/feedback"` instead of `"magus/trail"`. `magus\trail\Trail` is
  `magus\feedback\Report`, and `Section`, `Verdict`, `ReadOptions`, `Observation`,
  `Spawn` and `Mark` keep their names under `magus\feedback`. `magus\session().trail()`
  keeps its name and returns a `magus\feedback\Report`.
- **Breaking for SDK callers: `types/trail` is `types/feedback`.** `trail.Trail` is
  `feedback.Report`, and `std.MagusTrailRead`, `MagusTrailMarks` and `MagusTrailMark`
  are `MagusFeedbackRead`, `MagusFeedbackMarks` and `MagusFeedbackMark`. Their errors
  name `Session.trail`, `Session.marks` and `Session.mark`, the members a script calls.
