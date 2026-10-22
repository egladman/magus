### Security

- **Cache replay and fetched output bundles stay inside what they were asked for.** A hit that names a manifest path no output glob of the step claims now fails and the step rebuilds. A fetched bundle must carry the requested ref, and its key and attempt must be single path segments.
