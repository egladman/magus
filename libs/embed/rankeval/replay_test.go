package rankeval

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func judgments(n int) []Judgment {
	out := make([]Judgment, n)
	for i := range out {
		out[i] = Judgment{ID: strconv.Itoa(i), Query: "q" + strconv.Itoa(i)}
	}
	return out
}

func TestReplayKeepsJudgmentOrder(t *testing.T) {
	js := judgments(50)
	last := js[len(js)-1].ID
	lastDone := make(chan struct{})
	var running, peak atomic.Int32
	runs, err := Replay(context.Background(), js, 4, func(_ context.Context, j Judgment) (Run, error) {
		now := running.Add(1)
		defer running.Add(-1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		// The first judgment finishes after the last, so runs kept in completion order
		// would show.
		switch j.ID {
		case "0":
			<-lastDone
		case last:
			close(lastDone)
		}
		return Run{Judgment: j, Ranked: []string{j.Query}}, nil
	})
	require.NoError(t, err)
	require.Len(t, runs, len(js))
	for i, r := range runs {
		assert.Equal(t, js[i].ID, r.Judgment.ID)
		assert.Equal(t, []string{js[i].Query}, r.Ranked)
	}
	assert.LessOrEqual(t, peak.Load(), int32(4))
}

func TestReplayStopsAtTheFirstError(t *testing.T) {
	boom := errors.New("boom")
	var calls atomic.Int32
	runs, err := Replay(context.Background(), judgments(100), 1, func(_ context.Context, j Judgment) (Run, error) {
		calls.Add(1)
		if j.ID == "3" {
			return Run{}, boom
		}
		return Run{Judgment: j}, nil
	})
	require.ErrorIs(t, err, boom)
	assert.ErrorContains(t, err, "judgment 3: boom")
	assert.Nil(t, runs)
	assert.Less(t, calls.Load(), int32(100), "judgments after the failure are not started")
}

func TestReplayZeroWorkersRunsOneAtATime(t *testing.T) {
	runs, err := Replay(context.Background(), judgments(3), 0, func(_ context.Context, j Judgment) (Run, error) {
		return Run{Judgment: j}, nil
	})
	require.NoError(t, err)
	assert.Len(t, runs, 3)
}
