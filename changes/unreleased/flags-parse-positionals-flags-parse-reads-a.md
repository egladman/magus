### Changed

- **`flags.parse` reads a bare word as the first positional.** A script run as `magus buzz x.buzz -- -o json a b` gets `a` and `b` as positionals with no second `--`. A bare word ends the flags, a lone `--` still does, and an undeclared dashed word or an empty argument stays in `unknown`.
