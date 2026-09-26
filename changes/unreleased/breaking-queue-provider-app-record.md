### Changed

- **Breaking for provider scripts: `describe` and `merge_change` receive `app` as
  `{slug, id}`.** A `describe` that cannot name the queue's integration returns
  `{missing_app: {reason, url, slug?}}`; a setup whose credential id is empty, or
  differs from its app's, is refused. `magus queue apply --app` takes `<slug>:<id>` too.
