package types

// The records a provider op takes and returns, mirrored under their Go names in magus/spell
// so a spell declares `export fun <op>(target: Target, req: <Req>) > <Resp>`.
// cmd/magus-utils/provider_ops.go lists which op takes which.

// Empty is the request of an op that needs nothing but its Target.
type Empty struct{}

// Ack is a provider's yes or no. What false means is the op's to say: a cache miss for
// get_artifact, an artifact already stored for put_artifact.
type Ack struct {
	OK bool
}

// SecretRef is what resolve_secret is asked for: a reference in the provider's own
// notation, passed through verbatim.
type SecretRef struct {
	Ref string
}

// GreenRun is what last_green_run answers: the provider's id for the newest run that
// passed, and the commit it ran. Either empty is "no green run".
type GreenRun struct {
	Run    string
	Commit string
}

// Strings is a list of strings returned as a record, so the op returns an object like
// every other op.
type Strings struct {
	Items []string
}

// CIGroup opens a fold in a CI log.
type CIGroup struct {
	ID        string
	Title     string
	Collapsed bool
}

// CIGroupEnd closes the fold the CIGroup with the same ID opened.
type CIGroupEnd struct {
	ID string
}

// CIAnnotation is one notice a CI provider raises against a file and line. Level is
// "error", "warning" or "notice"; a zero position is no position.
type CIAnnotation struct {
	Level   string
	Message string
	Title   string
	Code    string
	File    string
	Line    int
	EndLine int
	Col     int
	EndCol  int
}

// ArtifactRequest names one cache artifact: the project it belongs to and its key. Path is
// the local file the op reads (put_artifact) or writes (get_artifact); has_artifact gets
// none.
type ArtifactRequest struct {
	Project string
	Hash    string
	Path    string
}

// PruneRequest is a remote cache's retention policy: evict what is older than
// OlderThanSecs, keep the newest KeepLast, and change nothing when DryRun is set.
type PruneRequest struct {
	OlderThanSecs int64
	KeepLast      int64
	DryRun        bool
}

// PruneResult reports whether a prune sweep completed.
type PruneResult struct {
	Done bool
}

// LifecycleQuery is every lifecycle key list_lifecycles is asked about, in one call.
type LifecycleQuery struct {
	Keys []string
}

// FindReviewRequest is what find_review is asked: the branch and remote magus resolved, so
// the spell never rederives them. WantViewer asks the provider to also name who the
// credential belongs to, which magus sets only while it does not know yet.
type FindReviewRequest struct {
	Branch     string
	Remote     string
	WantViewer bool
}

// ReviewDraft is one remark publish_review places inline, at a path and a new-side line.
// The spell drops a draft with no line rather than guessing a position.
type ReviewDraft struct {
	Path string
	Line int
	Body string
}

// PublishReviewRequest sends a batch of drafts as one review. Verdict is the one magus
// already permitted, never the one the person asked for.
type PublishReviewRequest struct {
	Repo    string
	ID      string
	Summary string
	Verdict ReviewVerdict
	Drafts  []ReviewDraft
}

// ReplyReviewRequest answers one existing thread of a review.
type ReplyReviewRequest struct {
	Repo   string
	ID     string
	Thread string
	Body   string
}

// ReviewRef names one review in the provider's own terms.
type ReviewRef struct {
	Repo string
	ID   string
}

// ProjectQuery is what list_projects is asked: the workspace root it reports projects under.
type ProjectQuery struct {
	Root string
}

// ListPullsRequest is what list_pulls is asked.
type ListPullsRequest struct {
	// Remote is the workspace's remote, as ReviewOrigin gives it.
	Remote string
	Base   string
	// Authors are logins. Empty asks for every open pull request.
	Authors []string
}

// PullList is the open pull requests list_pulls read and what the reads cost.
type PullList struct {
	Repo   string
	Viewer string
	// Reason is non-empty when nothing could be read, such as "no credential".
	Reason      string
	Base        BaseBranch
	Pulls       []ForgePull
	Rate        RateLimit
	Requests    int // sent this call
	Revalidated int // of Requests, answered 304
}

// RateLimit is the host's request budget as of the last response.
type RateLimit struct {
	Limit     int
	Remaining int
	// Reset is RFC 3339, or empty when the host sent none.
	Reset string
}

// BaseBranch is the branch pull requests target.
type BaseBranch struct {
	Name   string
	SHA    string
	URL    string
	Checks []ForgeCheck
	// Commits are newest first, about the last 30.
	Commits []BaseCommit
}

// BaseCommit is one commit on the base branch.
type BaseCommit struct {
	SHA     string
	Subject string
	// At is RFC 3339.
	At string
	// Pull is the pull request this commit landed, or 0 when none is named.
	Pull int
}

// ForgePull is one open pull request as the forge reports it.
type ForgePull struct {
	Number  int
	Title   string
	Author  string
	URL     string
	Branch  string
	HeadSHA string
	// FetchRef fetches the head commit, as in refs/pull/<n>/head.
	FetchRef string
	Draft    bool
	// Mergeable is mergeable, conflicting or unknown.
	Mergeable string
	// BehindBy counts commits on base missing from head, -1 when unknown.
	BehindBy int
	// Review is approved, changes_requested, review_required or none.
	Review string
	// PushedAt is RFC 3339.
	PushedAt string
	Checks   []ForgeCheck
	Files    []string
}

// ForgeCheck is one check run or commit status on a commit.
type ForgeCheck struct {
	Name string
	// Kind is "check" for a check run, "status" for a commit status.
	Kind string
	// State is queued/running/passed/failed/skipped/cancelled.
	State string
	// The three times are RFC 3339.
	QueuedAt    string
	StartedAt   string
	CompletedAt string
	URL         string
	Description string
	// Lines are the first error lines, at most 5.
	Lines []string
	// OutputRef is a magus output ref found in Lines, or empty.
	OutputRef string
}
