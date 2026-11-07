### Fixed

- **`http` revalidation is GET only, and no longer returns an empty body or grows without
  bound.** `revalidate` on another method is an error rather than a shared ETag entry. A 304
  with no stored entry is an error naming the URL. The ETag cache holds at most 64 MiB of
  bodies, evicting the oldest first.
