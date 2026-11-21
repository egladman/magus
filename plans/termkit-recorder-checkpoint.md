# TermKit recorder checkpoint

The recorder belongs in Go at `libs/termkit/recorder`, with generated Buzz bindings.
The implementation uses `termkit/screen`; no handwritten Buzz renderer is included.

This checkpoint includes capture splitting, frame timing and SVG rendering. The Go
parity test compares both captures and both palettes with the original CLI renderer.
The generic library emitter attaches record receiver methods to Buzz objects and
rejects unknown keys in its map decoders. Library metadata is generated before the
boundary registry reflects over it.

Remaining work before this draft is ready:

- Give record arguments and return values typed Buzz declarations throughout the
  library generator. The current map boundary still requires runtime decoding.
- Convert fallible generated library calls to the shared `result\Result::<T>`
  contract. They currently expose the existing raising host contract.
- Finish the generated Buzz byte-for-byte parity check and wire the CLI to the
  shared recorder before deleting its original rendering implementation.
- Review the complete generated API and its language idioms.

The broader recording freshness work remains preserved in the
`complete-diagnostic-hints` worktree. It includes refresh commands, input digests,
capture generation and their tests. It is outside this checkpoint.

This draft is based on main. The harness readiness and CI repairs remain in
PR #879, and the filename word-boundary work remains in PR #878.
