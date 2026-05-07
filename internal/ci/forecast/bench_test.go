package forecast

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// syntheticHistory builds a History with n projects × 2 targets, each with
// SampleWindow recent duration observations — representative of a mid-size
// monorepo after several CI runs.
func syntheticHistory(n int) History {
	h := History{
		Version:   HistoryVersion,
		UpdatedAt: time.Now().UTC(),
		Constants: Constants{SetupP50Ms: 30_000, AlphaMs: 5_000},
		Projects:  make(map[string]map[string]Stats, n),
	}
	for i := range n {
		proj := fmt.Sprintf("apps/service-%03d", i)
		h.Projects[proj] = map[string]Stats{
			"build": syntheticStats(i, "build"),
			"test":  syntheticStats(i, "test"),
		}
	}
	return h
}

func syntheticStats(seed int, _ string) Stats {
	recent := make([]int64, SampleWindow)
	for j := range recent {
		recent[j] = int64(30_000 + seed*100 + j*50)
	}
	return Stats{
		P75Ms:       int64(30_000 + seed*100),
		Samples:     SampleWindow,
		LastUpdated: time.Now().UTC(),
		Recent:      recent,
		HitCount:    seed % 10,
		MissCount:   (seed + 3) % 10,
		HitRate:     float64(seed%10) / 10.0,
	}
}

// BenchmarkHistoryLoad measures the full Load path: os.ReadFile +
// json.Unmarshal for a 100-project history file.
func BenchmarkHistoryLoad(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "history.json")
	h := syntheticHistory(100)
	if err := h.Save(context.Background(), path); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		var h2 History
		if err := h2.Load(context.Background(), path); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHistorySave measures the full Save path: json.MarshalIndent +
// atomic write for a 100-project history file.
func BenchmarkHistorySave(b *testing.B) {
	path := filepath.Join(b.TempDir(), "history.json")
	h := syntheticHistory(100)
	b.ResetTimer()
	for range b.N {
		if err := h.Save(context.Background(), path); err != nil {
			b.Fatal(err)
		}
	}
}
