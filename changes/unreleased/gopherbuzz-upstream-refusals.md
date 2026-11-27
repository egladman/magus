### Changed

- **Breaking: the checker refuses five programs upstream Buzz refuses.** One example is a field read through an optional without `?.` or `!` (upstream E28). Programs that checked before now fail at the reported line; fix each there.
