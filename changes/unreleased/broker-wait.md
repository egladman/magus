### Added

- **`--capacity-wait DURATION` waits in the broker's line for host capacity.** Refusal
  stays the default. With a bound, a step waits oldest first, prints a line per holder
  change and exits 75 naming the holder at expiry. The Go SDK gets
  `broker.Client.Acquire` with `WithWait` and `WithWaitFunc`.
