package spells

// The review contract: the reserved function names a spell exports to connect a workspace to
// wherever its changes are discussed.
//
// A fourth contract on the same vendor spell that already carries the cache, CI and secret
// ones. That is the shape this repository settled on before this existed - "one spell per
// vendor, three contracts" is written at the top of spells/github/actions - and a review
// contract is the fourth rather than a new subsystem beside it.
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
// ThreadsContract exists so a reader never leaves for the browser to find out what a colleague
// said. It is the one contract function that makes magus depend on a host being reachable, so
// it is deliberately its own name: a workspace with no credential, or no pull request open,
// still publishes nothing and reads nothing, and every other surface works exactly as before.
const (
	// OpenReviewContract reports the review open for a branch, or nothing when there is none.
	// A branch with no pull request is the ordinary state, not an error.
	OpenReviewContract = "open_review"

	// ThreadsContract lists the comment threads already on that review, so they can be read
	// and replied to without leaving.
	ThreadsContract = "review_threads"

	// PublishReviewContract sends a batch of drafts as one review.
	PublishReviewContract = "publish_review"

	// ReplyReviewContract answers one existing thread.
	ReplyReviewContract = "reply_review"
)

// ReviewContracts is every reserved name in this contract, for a caller checking whether a
// spell implements enough of it to be useful.
//
// A spell may implement a SUBSET. Reading and publishing are genuinely separable - a host with
// no comment API can still take a review body - so a missing name is a capability this spell
// does not have rather than a broken spell.
var ReviewContracts = []string{
	OpenReviewContract,
	ThreadsContract,
	PublishReviewContract,
	ReplyReviewContract,
}
