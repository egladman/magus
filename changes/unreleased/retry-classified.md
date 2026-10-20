### Changed

- **A retry stops on an error typed non-retryable.** `internal/retry.Do` ends the loop at once and reports that the error was not retried, with the attempts it took. `retry_on_volatile` skips a failure typed non-retryable and records `non_retryable` as the reason.
