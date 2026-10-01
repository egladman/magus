### Changed

- **The push rule reads only gate verdicts.** It used to decode every invocation in the
  session store on each push; it now reads the gate results through the same per-kind cache.
