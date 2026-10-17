### Removed

- **Breaking: `magus session show` drops the past price step section.** It compared the spawning thread's context with the child's price step: the trail records no child's context, so the count measured nothing. `past_price_step` leaves the JSON, and `trail.SessionSpawn.ContextTokens`, which read the spawner's context as the child's, goes with it.
