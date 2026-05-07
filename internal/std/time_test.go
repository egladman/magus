package std

import (
	"context"
	"testing"
)

func TestTimeFormat(t *testing.T) {
	// 1780833600000 ms = 2026-06-07T12:00:00Z.
	got, err := TimeFormat(context.Background(), "2006-01-02T15:04:05Z07:00", 1780833600000)
	if err != nil {
		t.Fatal(err)
	}
	if want := "2026-06-07T12:00:00Z"; got != want {
		t.Fatalf("TimeFormat = %q, want %q", got, want)
	}
}

func TestTimeParseS3Timestamp(t *testing.T) {
	// S3 LastModified carries millisecond fractional seconds and a literal Z; the
	// RFC 3339 layout parses it without an explicit fractional field.
	got, err := TimeParse(context.Background(), "2006-01-02T15:04:05Z07:00", "2026-06-07T12:00:00.000Z")
	if err != nil {
		t.Fatal(err)
	}
	if want := float64(1780833600000); got != want {
		t.Fatalf("TimeParse = %v, want %v", got, want)
	}
}

func TestTimeParseRoundTrip(t *testing.T) {
	const layout = "20060102T150405Z"
	const ms = float64(1780833600000)
	s, err := TimeFormat(context.Background(), layout, ms)
	if err != nil {
		t.Fatal(err)
	}
	back, err := TimeParse(context.Background(), layout, s)
	if err != nil {
		t.Fatal(err)
	}
	if back != ms {
		t.Fatalf("round-trip = %v, want %v (via %q)", back, ms, s)
	}
}

// TestTimeFarFuture proves float64-millis carries timestamps past 2262 (where an
// int64 of nanoseconds would overflow, and a 32-bit int of millis would have long
// since truncated) without loss — the reason the interchange is float64, not int.
func TestTimeFarFuture(t *testing.T) {
	const layout = "2006-01-02T15:04:05Z07:00"
	const want = "9999-12-31T23:59:59Z"
	ms, err := TimeParse(context.Background(), layout, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := TimeFormat(context.Background(), layout, ms)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("far-future round-trip = %q, want %q (ms=%v)", got, want, ms)
	}
}

func TestTimeParseError(t *testing.T) {
	if _, err := TimeParse(context.Background(), "2006-01-02", "not-a-date"); err == nil {
		t.Fatal("TimeParse of a malformed value should error")
	}
}

func TestTimeParseDuration(t *testing.T) {
	got, err := TimeParseDuration(context.Background(), "168h")
	if err != nil {
		t.Fatal(err)
	}
	if want := float64(168 * 60 * 60 * 1000); got != want {
		t.Fatalf("TimeParseDuration(168h) = %v ms, want %v", got, want)
	}
	if _, err := TimeParseDuration(context.Background(), "banana"); err == nil {
		t.Fatal("TimeParseDuration of garbage should error")
	}
}
