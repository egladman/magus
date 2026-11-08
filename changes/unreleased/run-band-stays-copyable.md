### Changed

- **The live band is plain text.** It has no frame, no rule and no padding, so
  a drag selection over it copies the words you see. The status line is its
  first row, with a spinner, a progress bar of finished targets and the slot
  count. The wait tree and the failure tree start their metadata in one column
  after the longest label, and failure output now sits under the failure list,
  where `tab` moves the rows between them. The hint row drops whole hints from
  the right on a narrow terminal and always keeps `esc done`.

### Removed

- **`cache.PoolGauge`, `tty.Span.Align` and `tty.Zone.SetTitle`.** Nothing is
  aligned to the terminal edge or captioned into a rule any more.
