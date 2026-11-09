### Added

- **`proofread voice eval --paired` scores how a rewrite moves each shape feature.** It
  joins the owner's texts with a rewrite of each by `--id-field` and reports, per
  feature, the cut at 5% owner false alarms and the share of rewrites past it.
- **`uniform` (PRF1041) flags a text more even than its author.** With a voice file, a
  text whose list items share one length and one opener, or whose sentences open alike,
  past the author's own 5th percentile on two of those, is reported. It ships off.
- **A finding names the specifics its match holds.** A number with its unit, a name or a
  moment the author saw is listed under the finding's `protected` in `-o json`, and an
  economy rule that would cut one reports advice instead of a deny.
