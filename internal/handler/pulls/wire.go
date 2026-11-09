package pulls

import (
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
	"github.com/egladman/magus/types"
)

// parseMergeable reports false for a value outside the provider contract,
// which reads as UNSPECIFIED: UNKNOWN means the forge is still computing.
func parseMergeable(s string) (pullsv1.Mergeable, bool) {
	switch s {
	case "mergeable":
		return pullsv1.Mergeable_MERGEABLE_MERGEABLE, true
	case "conflicting":
		return pullsv1.Mergeable_MERGEABLE_CONFLICTING, true
	case "unknown":
		return pullsv1.Mergeable_MERGEABLE_UNKNOWN, true
	default:
		return pullsv1.Mergeable_MERGEABLE_UNSPECIFIED, false
	}
}

// parseReview reports false for a value outside the provider contract,
// which reads as UNSPECIFIED.
func parseReview(s string) (pullsv1.Review, bool) {
	switch s {
	case "approved":
		return pullsv1.Review_REVIEW_APPROVED, true
	case "changes_requested":
		return pullsv1.Review_REVIEW_CHANGES_REQUESTED, true
	case "review_required":
		return pullsv1.Review_REVIEW_REQUIRED, true
	case "none":
		return pullsv1.Review_REVIEW_NONE, true
	default:
		return pullsv1.Review_REVIEW_UNSPECIFIED, false
	}
}

func failedJob(c types.ForgeCheck) *pullsv1.FailedJob {
	return &pullsv1.FailedJob{Name: c.Name, Url: c.URL, Lines: c.Lines, OutputRef: c.OutputRef}
}

// parseTime returns the zero time for an instant the forge did not send.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func toTimestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func toDuration(d time.Duration) *durationpb.Duration { return durationpb.New(d) }

// formatDuration drops the zero units: 1m, not 1m0s.
func formatDuration(d time.Duration) string {
	s := d.Round(time.Second).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
