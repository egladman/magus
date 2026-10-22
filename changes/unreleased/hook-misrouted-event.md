### Fixed

- **`buzz-check` and `proofread-reply` name a misrouted hook event.** A host that sends
  either hook an event it does not handle now gets a message on stderr naming the event,
  instead of a silent pass.
