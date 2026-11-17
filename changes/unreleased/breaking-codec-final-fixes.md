### Changed

- **Breaking for SDK callers: a value its Go target cannot hold is a `*codec.TypeError`
  in both builds.** It names the JSON kind and the Go type, and wraps the library's own
  error. A Go array of another length is one too, no longer a `*codec.SyntaxError`.
- **Breaking: `codec.Decoder` refuses in the codec's own words.** A malformed stream is a
  `*codec.SyntaxError` whose offset counts from the stream's start, and it stays
  malformed for every later `Decode`. A value glued to the last one is
  `codec.ErrTrailingData`.
- **Breaking: `codec.UnmarshalLossless` reads a typed target as `codec.Unmarshal` does.**
  Under `encoding/json/v2` names match exactly, null zeroes a value, and only a number
  bound for an interface stays a `json.Number`.
- **Breaking: a `codec.JSONL` with neither `Log` nor `OnDrop` refuses to drop an
  unfinished line.** `Update` fails with a `*codec.UnfinishedLineError` naming the file
  and line, and writes nothing. `OnDrop` hears each line a rewrite drops.
- **Breaking: a store's directory mode follows its file mode.** 0644 files get a 0755
  directory, and 0600 files get 0700 where they got 0755.
- **Breaking: the codec's wrapped errors read sentinel first.** A repeated key is
  `duplicate key: "a"`, and invalid UTF-8 is `reading a string: invalid UTF-8`.
