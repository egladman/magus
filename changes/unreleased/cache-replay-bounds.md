### Security

- **Cache replay and fetched output bundles stay inside what they were asked for.** A hit
  now fails naming any manifest path no output glob of the step claims (a remote or
  imported manifest could name a hook or `magusfile.buzz`), and the step rebuilds. A
  fetched output bundle must carry the ref requested, and its key and attempt must be
  single path segments, so a store cannot swap in another run's bundle or file a log
  outside the outputs directory.
