package pulls

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
	"github.com/egladman/magus/types"
)

func mergeableOf(s string) pullsv1.Mergeable {
	switch s {
	case "mergeable":
		return pullsv1.Mergeable_MERGEABLE_MERGEABLE
	case "conflicting":
		return pullsv1.Mergeable_MERGEABLE_CONFLICTING
	default:
		return pullsv1.Mergeable_MERGEABLE_UNKNOWN
	}
}

func reviewOf(s string) pullsv1.Review {
	switch s {
	case "approved":
		return pullsv1.Review_REVIEW_APPROVED
	case "changes_requested":
		return pullsv1.Review_REVIEW_CHANGES_REQUESTED
	case "review_required":
		return pullsv1.Review_REVIEW_REQUIRED
	default:
		return pullsv1.Review_REVIEW_NONE
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

func timestampOf(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func durationOf(d time.Duration) *durationpb.Duration { return durationpb.New(d) }

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

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
