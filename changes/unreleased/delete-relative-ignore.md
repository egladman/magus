### Removed

- **`watch.RelativeIgnore` is gone.** `BuiltinIgnore` judges only the last path
  element, so the file watchers in the server, the registry and `job` use it directly.
