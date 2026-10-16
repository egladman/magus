### Changed

- **Every match says why it ranked.** `-o json` gives each match a `why`: the words it
  held (`matched` of `of`), each word's score per field, its rank in each ranked list,
  and its prior. The answer's `ranking` names the weight file's digest and the reader
  paths it used. `score` is the fused score times 10000; only its order means
  anything.
