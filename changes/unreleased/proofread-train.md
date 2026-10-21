### Added

- **`proofread-train train` retrains the word-class model.** It fetches UD English EWT by a
  pinned digest and writes the model and its test split; `magus run wordclass-generate
  libs/proofread` runs it.
