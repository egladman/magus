package spells

// The review contract: the reserved function names a spell exports to connect a workspace to
// wherever its changes are discussed.
//
// A fourth CONTRACT beside the cache, CI and secret ones, detected the same way - by reserved
// function name on a spell a magusfile selected. It is not a new subsystem.
//
// Whether it rides on the same spell as the other three is up to the vendor. For GitHub it does
// not: spells/github/actions is inert outside a CI runner by design, and a review happens on a
// laptop, so the review ops live in spells/github/review and that workspace imports two spells.
// A vendor whose contracts share a runtime would carry all four in one.
//
// magus knows nothing about GitHub. It calls these four names, and the spell talks to whatever
// host it was written for over ordinary HTTP: `spells/github/actions` already does exactly
// that for the Actions cache, with `import "http"` and a bearer token. No vendor CLI has to be
// installed for a review to work, which is the difference between a token and a binary.
//
// # Why publishing is a batch
//
// PublishReviewContract takes every draft at once rather than one comment per call. Self-review
// is a pass: you read, you accumulate remarks, and only then do you decide the whole thing is
// worth sending. A per-comment call would publish the first thought before the fifth one had
// changed your mind about it, and it would turn one outward-facing act - which is what needs
// confirming - into a series of small ones nobody confirms individually.
//
// # Why reading is separate from publishing
//
// ReviewThreadsContract exists so a reader never leaves for the browser to find out what a colleague
// said. It is the one contract function that makes magus depend on a host being reachable, so
// it is deliberately its own name: a workspace with no credential, or no pull request open,
// still publishes nothing and reads nothing, and every other surface works exactly as before.
//
// A spell may implement a SUBSET. Each op is looked up by name at the moment it is called, so a
// missing one is a capability that provider lacks rather than a broken spell, with nothing to
// declare either way.
const (
	// OpenReviewContract reports the review open for a branch, or nothing when there is none.
	// A branch with no pull request is the ordinary state, not an error.
	OpenReviewContract = "open_review"

	// ReviewThreadsContract lists the comment threads already on that review, so they can be
	// read and replied to without leaving. It returns a list of records; nothing at all means
	// no threads, and is not an error.
	ReviewThreadsContract = "review_threads"

	// PublishReviewContract sends a batch of drafts as one review.
	PublishReviewContract = "publish_review"

	// ReplyReviewContract answers one existing thread. It returns a BOOL: true when the host
	// took the reply. Stated because a spell that answered with anything else is read as a
	// refusal, and a refusal reported after the reply already posted is how the same sentence
	// reaches a colleague twice.
	ReplyReviewContract = "reply_review"
)
