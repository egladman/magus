package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimeFormat(t *testing.T) {
	// 1780833600000 ms = 2026-06-07T12:00:00Z.
	got, err := TimeFormat(context.Background(), "2006-01-02T15:04:05Z07:00", 1780833600000)
	require.NoError(t, err)
	assert.Equal(t, "2026-06-07T12:00:00Z", got)
}

func TestTimeParseS3Timestamp(t *testing.T) {
	// S3 LastModified carries millisecond fractional seconds and a literal Z; the
	// RFC 3339 layout parses it without an explicit fractional field.
	got, err := TimeParse(context.Background(), "2006-01-02T15:04:05Z07:00", "2026-06-07T12:00:00.000Z")
	require.NoError(t, err)
	assert.Equal(t, float64(1780833600000), got)
}

func TestTimeParseRoundTrip(t *testing.T) {
	const layout = "20060102T150405Z"
	const ms = float64(1780833600000)
	s, err := TimeFormat(context.Background(), layout, ms)
	require.NoError(t, err)
	back, err := TimeParse(context.Background(), layout, s)
	require.NoError(t, err)
	assert.Equal(t, ms, back)
}

// TestTimeFarFuture proves float64-millis carries timestamps past 2262 (where an
// int64 of nanoseconds would overflow, and a 32-bit int of millis would have long
// since truncated) without loss — the reason the interchange is float64, not int.
func TestTimeFarFuture(t *testing.T) {
	const layout = "2006-01-02T15:04:05Z07:00"
	const want = "9999-12-31T23:59:59Z"
	ms, err := TimeParse(context.Background(), layout, want)
	require.NoError(t, err)
	got, err := TimeFormat(context.Background(), layout, ms)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestTimeParseError(t *testing.T) {
	_, err := TimeParse(context.Background(), "2006-01-02", "not-a-date")
	assert.Error(t, err, "TimeParse of a malformed value should error")
}

func TestTimeParseDuration(t *testing.T) {
	got, err := TimeParseDuration(context.Background(), "168h")
	require.NoError(t, err)
	assert.Equal(t, float64(168*60*60*1000), got)

	_, err = TimeParseDuration(context.Background(), "banana")
	assert.Error(t, err, "TimeParseDuration of garbage should error")
}
