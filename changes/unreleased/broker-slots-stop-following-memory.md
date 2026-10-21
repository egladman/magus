### Changed

- **Under the machine broker a claim's slots no longer grow with its memory.** The broker
  holds megabytes and slots apart, so a brokered claim keeps its declared or measured
  width as its slots. With no broker, memory still converts to local slots, now at the
  same usable memory budget the broker plans against rather than the host's whole
  memory.
