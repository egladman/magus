### Changed

- **Graph search keeps the Buzz error operator `!>` as a word.** A query holding `!>`
  matches the text that spells it, such as a Buzz signature's error type; every other
  punctuation is still dropped. An embedding table trained under the old tokenizer is
  refused at load, naming the tokenizer digest it expected, until it is retrained.
