### Added

- **proofread's `triad` counts a series of three verb phrases** ("reviewed the plan,
  rewrote the parser and shipped the fix"), with or without the Oxford comma, beside
  the noun series it already counted.
- **proofread's `list-parallel` reads an irregular past opener ("Cut", "Rewrote") as
  past** by tagging each item after a subject, so a past-tense list no longer reports
  it as a base form.
- **proofread adds `repeated-opener` (PRF1062), off by default**: a bulleted list of 5
  or more items where one opening word starts 70% of them.
