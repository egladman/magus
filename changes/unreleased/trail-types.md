### Changed

- **Breaking: the guard trail record types drop the `Feedback` prefix.** In Go, package
  `types` renames `FeedbackTrail` to `Trail`, `FeedbackObservation` to `TrailObservation`,
  `FeedbackSpawn` to `TrailSpawn`, `FeedbackMark` to `TrailMark`, `FeedbackSection` to
  `TrailSection`, `FeedbackVerdict` to `TrailVerdict`, `FeedbackSections` to
  `TrailSections` and `FeedbackVerdicts` to `TrailVerdicts`. The section constants
  `FeedbackRefused`, `FeedbackAdvised`, `FeedbackUnguarded` and `FeedbackNextNotTaken`
  become `TrailRefused`, `TrailAdvised`, `TrailUnguarded` and `TrailNextNotTaken`; the
  verdict constants `FeedbackShouldDeny`, `FeedbackShouldAdvise`, `FeedbackWrongDeny` and
  `FeedbackFine` become `TrailShouldDeny`, `TrailShouldAdvise`, `TrailWrongDeny` and
  `TrailFine`. In Buzz, the objects `magus\FeedbackTrail`, `FeedbackObservation`,
  `FeedbackSpawn` and `FeedbackMark` become `Trail`, `TrailObservation`, `TrailSpawn` and
  `TrailMark`. JSON keys and the stored mark format are unchanged. No alias is kept for
  the old names.
