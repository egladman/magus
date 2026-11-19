### Changed

- **Breaking: the CI forecast history is stamped `magus.forecast-history/v1alpha1`.** The
  integer `version` member is now `api_version`. A history with the integer or another group
  is set aside with a warning naming the file, rebuilt from empty and replaced on the next
  write. A newer version of the group is still refused.
