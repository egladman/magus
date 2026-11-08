### Changed

- **Breaking: the trail records move to `types/trail`, the stream events to
  `types/events`.** `types.TrailReadOptions` is `trail.ReadOptions`, `types.StreamEvent`
  is `events.Event`, and the rest drop their `Trail` or `Stream` prefix.
  `types.Session.TrailOptions` is gone; convert with `trail.ReadOptions(s)`. Buzz names
  and the JSON shapes are unchanged.
