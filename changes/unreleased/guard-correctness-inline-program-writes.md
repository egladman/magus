### Fixed

- **A read-only interpreter program is no longer refused as a cache-dir write.** An
  inline program such as `python3 - <<EOF` is judged on the destinations of its writes,
  and on nothing when it writes nothing. One that deletes, moves or shells out, or writes
  somewhere the guard cannot follow, is still judged on every path it names.
