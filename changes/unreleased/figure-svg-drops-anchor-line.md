### Changed

- **A `magus/figure` drawing carries an anchor's line only when it links.** `draw` without
  `anchorHref` no longer paints `data-line`, so a committed figure stays byte-identical
  when an edit moves the anchored symbol down its file. Pass `anchorHref` to keep the line.
