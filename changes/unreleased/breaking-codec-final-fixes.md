### Changed

- **Breaking for `libs/codec` callers: a value its Go target cannot hold is a
  `*codec.TypeError` in both builds.** It names the JSON kind and the Go type in the same
  words under `encoding/json` and `encoding/json/v2`, and wraps the library's own error,
  so `errors.As` to `*json.UnmarshalTypeError` or `*jsonv2.SemanticError` still reaches it.
  An array of another length than its Go array's is a `*codec.TypeError` in both builds,
  no longer a `*codec.SyntaxError` under `encoding/json` alone.
- **Breaking: `codec.Decoder` refuses in the codec's own words.** A malformed stream is a
  `*codec.SyntaxError` whose offset counts from the start of the stream, a repeated key
  and invalid UTF-8 included, and stays malformed for every later `Decode`. A value that
  follows the last one with no whitespace between them is refused as
  `codec.ErrTrailingData`.
- **Breaking: `codec.UnmarshalLossless` reads a typed target as `codec.Unmarshal` does.**
  Under `encoding/json/v2` it uses v2's own duplicate and number handling: names match
  exactly, null zeroes a value, and only a number bound for an interface stays a
  `json.Number`. The `encoding/json` build refuses what `Unmarshal` refuses there.
- **Breaking: a `codec.JSONL` with neither `Log` nor the new `OnDrop` refuses to drop a
  line no writer finished.** Its `Update` fails with a `*codec.UnfinishedLineError`
  (`codec.ErrUnfinishedLine`) naming the file and line, and writes nothing. `OnDrop` is
  told of each line a rewrite drops. `codec.Lines.PartialLine` numbers the unfinished
  line.
- **Breaking: a store's directory mode follows its file mode.** `AppendLines`,
  `RewriteLines` and `JSONL` create a directory searchable by whoever may read the file:
  0644 files get 0755, and 0600 files get 0700 where they got 0755.

### Fixed

- The `encoding/json` build writes a U+FFFD a `RawMessage` or a `MarshalJSON` result spells
  as `�`, as v2 does, and refuses only an invalid byte.
- `codec.UnmarshalWithUnknown` keeps a member whose name matches a declared field only when
  case is ignored, in both builds, where the `encoding/json` build refused it.
