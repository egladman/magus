### Fixed

- **A job store still spelling goals is refused, not read without its postconditions.**
  The safetyscissors job reader refuses a row whose spec carries `goals`, or a row or
  header requiring `signature-goals`, with a `RetiredNameError` naming the retired name
  and its respelling. An SDK caller's migration respells it first; magus's own store
  migrates already.
