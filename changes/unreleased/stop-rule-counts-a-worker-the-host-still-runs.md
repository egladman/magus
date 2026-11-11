### Fixed

- **The idle-stop rule counts a worker the host still runs.** `StopRequest.tasks` lists the
  subagents the transcript shows still running. A task named `<parent>/<role> <job>` marks
  its row as worked, so a fresh spawn or a worker waiting on its own run no longer refuses
  the stop. `tasksUnknown` says why the list could not be read.
