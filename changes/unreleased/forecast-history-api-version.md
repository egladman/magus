### Changed

- **Breaking: the CI forecast history is stamped `magus.forecast-history/v1alpha1`.** The
  integer `version` member of `history.jsonl` is now `api_version`. A history still stamped
  with the integer is refused, naming the file; delete it and the next run rebuilds the
  history. A history from a newer version of the group is refused as newer, and one of
  another group as foreign.
