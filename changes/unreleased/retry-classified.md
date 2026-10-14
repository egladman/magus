### Changed

- **A retry stops on an error typed non-retryable.** `internal/retry.Do` ends the loop at
  once and reports that the error was not retried, with the attempts it took. Self-update
  fetches no longer repeat a 4xx (a missing release is a 404) or an over-limit body;
  5xx, 408, 429 and transport errors still retry. `retry_on_volatile` skips a failure
  typed non-retryable and records `non_retryable` as the reason. `WithFixedDelay` now
  applies to `Do`, and `os\retry` shares the stop and the backoff arithmetic: a host error
  raised inside its callback keeps its type, and a value the script throws, a string
  included, is retried. The queue hook's exit 75 loop, the HTTP transport and the Buzz
  `os\retry` loop stay separate: each differs in what it retries, what it returns or who
  sets the policy.
