### Changed

- **Breaking: the CI forecast history is stamped `magus.forecast-history/v1alpha1`.** The
  integer `version` member of the history file is now `api_version`. A history stamped with
  the integer, or with another group, is set aside with a warning naming the file: the run
  rebuilds the history from empty and the next write replaces it. A history from a newer
  version of the group is still refused, since rewriting it would drop what this build
  cannot read.
