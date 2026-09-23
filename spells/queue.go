package spells

// The merge-queue contract: the reserved function names a spell exports to connect
// `magus vcs queue` to the host where changes wait to merge.
//
// It follows the remote-cache pattern. The engine in internal/queue decides what to
// stage, validate and merge, and knows nothing about any host; the spell a magusfile
// selects with magus\queue.provider(<spell>) turns five calls into that host's API.
//
// Every op receives the change record list_queue returned (id, repo, head, ...), so a
// spell never has to rediscover which repository it is talking to.
//
// Unlike the review contract, a spell must implement ALL five. The queue is mandatory
// once wired (branch protection requires its status), so a provider that can list
// changes but cannot merge them would hold every change forever; the engine refuses a
// spell missing an op rather than running half a queue.
const (
	// ListQueueContract lists the open changes carrying merge intent against a base
	// branch, in queue order. Merge intent is the host's own merge action (GitHub's
	// auto-merge, GitLab's "set to auto-merge"), never a label or command. Params:
	// {base, remote}. Returns a list of records: {id, repo, head, ref, branch, base,
	// title, author}.
	ListQueueContract = "list_queue"

	// ApprovalAtContract reports whether a change is approved AT one exact commit.
	// Params: the change plus {sha}. Returns {approved, head, required, approvals,
	// reason}: head is the change's CURRENT head, so the engine can tell an approval
	// of the tested commit from one of a commit pushed since.
	ApprovalAtContract = "approval_at"

	// PostStatusContract sets the queue's commit status on a sha. Params: the change
	// plus {sha, context, state, description}; state is pending, success or failure.
	// Returns true when the host recorded it.
	PostStatusContract = "post_status"

	// MergeChangeContract merges a change through the host's API at exactly {sha}, so
	// the host refuses when the head moved. The change author stays the author.
	// Returns {merged, reason}.
	MergeChangeContract = "merge_change"

	// KickBackContract removes a change's merge intent and tells its author why.
	// Params: the change plus {sha, body}. Returns true when both happened.
	KickBackContract = "kick_back"
)
