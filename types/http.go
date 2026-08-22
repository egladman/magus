package types

// HTTPRetry is the retry policy an http\get / post / request / download call
// applies. It mirrors the Buzz `HttpRetry` object.
//
// WHY IT IS A DECLARED OBJECT and not five keys in the opts map: retrying is the
// setting most likely to be wrong in a way nothing reports. A misspelled
// "retry_delayy" in an untyped map is silently ignored, and the only symptom is a
// build that hammers a flaky endpoint at the wrong cadence - or does not retry at
// all when the author believed it would. As a declared object the checker catches
// the typo at load.
//
// WHY THE ZERO VALUE MEANS NO RETRYING: an omitted policy has to be the safe
// reading, and for a build tool the safe reading is "run the request once". magus
// retried three times by default for a long while, which is wrong in two
// directions at once - it silently triples the cost of a genuinely failing request
// (and the wait before the failure is reported), and it can mask a real outage
// long enough that a build looks merely slow. Retrying is a decision about a
// specific endpoint's behavior, so the caller states it.
type HTTPRetry struct {
	// Attempts is the TOTAL number of tries, not the number of retries after the
	// first. 0 and 1 both mean "run it once"; 3 means one attempt plus two
	// retries. Counting total attempts rather than curl's --retry N avoids the
	// off-by-one that spelling invites.
	Attempts int
	// DelayMs is the pause before the second attempt, in milliseconds. It doubles
	// per attempt unless Fixed is set. Zero uses a 500ms default whenever
	// Attempts is greater than 1.
	DelayMs float64 `buzz:"delay_ms"`
	// MaxDelayMs caps the exponential growth, so a long backoff cannot stretch a
	// single request past anything useful. Zero means uncapped.
	MaxDelayMs float64 `buzz:"max_delay_ms"`
	// MaxElapsedMs is a wall-clock ceiling across all attempts including the
	// waits. It is the honest way to bound retrying: a count alone says nothing
	// about how long a build will sit there. Zero means no ceiling beyond the
	// per-request timeout.
	MaxElapsedMs float64 `buzz:"max_elapsed_ms"`
	// Fixed keeps the delay constant instead of doubling it, matching curl's
	// --retry-delay. Use it when an endpoint documents a fixed cooldown.
	Fixed bool
	// AllErrors retries every failure, including 4xx statuses that normally mean
	// the request itself is wrong. Off by default because retrying a 401 or a 404
	// cannot succeed and only delays the report.
	AllErrors bool `buzz:"all_errors"`
	// ConnRefused treats a refused connection as retryable, which it is not by
	// default. Set it when waiting for a service that is still coming up.
	ConnRefused bool `buzz:"conn_refused"`
}
