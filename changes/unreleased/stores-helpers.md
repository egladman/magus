### Fixed

- **Overlapping runs no longer erase each other's test history.** The volatility and
  duration history is shared by every workspace on the machine; a run wrote back the
  copy it loaded before it started, dropping outcomes another run recorded meanwhile.
  `magus config history passed` and `import` had the same race.
