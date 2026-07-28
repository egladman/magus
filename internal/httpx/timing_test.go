package httpx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A nil Recorder is the uninstrumented path, which is most call sites. It has to be
// usable without a guard, or every caller grows one.
func TestRecorderNilIsUsable(t *testing.T) {
	t.Parallel()
	var rec *Recorder
	require.NotPanics(t, func() { rec.Add(time.Second) })
	require.Zero(t, rec.Elapsed())
	require.Zero(t, rec.Calls())
	require.Nil(t, RecorderFrom(context.Background()), "no recorder installed is nil, not a zero value")
}

func TestRecorderAccumulates(t *testing.T) {
	t.Parallel()
	rec := &Recorder{}
	rec.Add(300 * time.Millisecond)
	rec.Add(700 * time.Millisecond)

	require.Equal(t, time.Second, rec.Elapsed())
	require.EqualValues(t, 2, rec.Calls())
}
