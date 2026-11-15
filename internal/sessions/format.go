package sessions

import (
	"fmt"
	"strings"
	"time"
)

// OrDash renders an empty or zero column as a dash, so a table reads "nothing here"
// rather than "a producer wrote nothing".
func OrDash(s string) string {
	if strings.TrimSpace(s) == "" || s == "0" {
		return "-"
	}
	return s
}

// CountNoun renders "1 write" / "4 writes". Distinct from a plural helper that picks
// between two words a caller supplies; this one owns the count as well, because every
// use is a number followed by a regular noun.
func CountNoun(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// HumanAge renders how long ago something happened, coarsely. The reader's question is
// "is this current work or history", which days answer and minutes do not.
func HumanAge(t time.Time) string {
	if t.IsZero() {
		return "at an unrecorded time"
	}
	switch d := time.Since(t); {
	case d < time.Hour:
		return "within the hour"
	case d < 24*time.Hour:
		return CountNoun(int(d/time.Hour), "hour") + " ago"
	default:
		return CountNoun(int(d/(24*time.Hour)), "day") + " ago"
	}
}

// formatDur renders an elapsed time in the two coarsest units that hold it, or "" for
// none. status.go's formatDur is the same function.
func formatDur(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	switch {
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		return fmt.Sprintf("%dh%dm", h, m)
	}
}
