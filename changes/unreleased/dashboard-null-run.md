### Fixed

- **The queue dashboard renders when main is red.** A main-run record built from another module
  read every field as null, so the history reader died with `cannot iterate over null`. The
  record is now `CiRun`. A history that cannot be read makes the page say the culprit is
  unknown, with the error, instead of failing the render.
