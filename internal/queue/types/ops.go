package types

// The records a provider script's ops take and return beyond the queue's own types. Their
// mirrors carry a Queue prefix, since Change, Check and Setting already name other mirrors;
// cmd/magus-utils/provider_ops.go lists which op takes which.

// DescribeRequest is what describe is asked: the [ListQuery] fields it reads.
type DescribeRequest struct {
	Base          string
	RemoteURL     string
	StatusContext string
	App           string
	SetupSteps    bool
}

// DescribeResult is what describe answers: exactly one of the capabilities, or Refused
// when it cannot describe a setup for the app it was asked about.
type DescribeResult struct {
	Capabilities *Capabilities
	Refused      *SetupRefusedError
}

// ListChangesRequest is what list_changes is asked.
type ListChangesRequest struct {
	Base      string
	RemoteURL string
	Only      string
}

// ApprovalRequest asks approval_at about Change at Commit.
type ApprovalRequest struct {
	Change
	Commit string
}

// RequiredChecksRequest asks required_checks for the checks reported on Change at Commit.
type RequiredChecksRequest struct {
	Change
	Commit string
}

// ListGreenRequest asks list_green for the changes whose head carries a success status
// under Context.
type ListGreenRequest struct {
	Base      string
	RemoteURL string
	Context   string
}

// StatusRequest posts Status on Change at Commit.
type StatusRequest struct {
	Change
	CommitStatus
	Commit string
}

// RetargetRequest moves Change onto the base branch To.
type RetargetRequest struct {
	Change
	To string
}

// MergeRequest merges Change as the options say.
type MergeRequest struct {
	Change
	MergeOptions
}

// MergeAnswer is what merge_change answers. Merged false is a refusal Reason explains;
// ByProvider says the provider merged the change itself rather than the queue's call.
type MergeAnswer struct {
	Merged     bool
	ByProvider bool
	Reason     string
}

// KickBackRequest sends Change at Commit back to its author.
type KickBackRequest struct {
	Change
	Kick
	Commit string
}

// MarkRequest sets Change's mark.
type MarkRequest struct {
	Change
	Mark Mark
}

// ListArtifactsRequest names the validation run whose artifacts list_artifacts lists.
type ListArtifactsRequest struct {
	Source string
}

// ReviewsRequest asks reviews about Change.
type ReviewsRequest struct {
	Change
}

// DismissReviewRequest dismisses Review on Change, telling its reviewer Message.
type DismissReviewRequest struct {
	Change
	Review  Review
	Message string
}
