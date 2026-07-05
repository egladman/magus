package ci

import "testing"

func TestDedup_CrossShardRedundancy(t *testing.T) {
	// key A built on 3 shards (durations 100/200/300) -> 2 extra builds, waste =
	// 100+200+300 - 300 = 300. key B built on 2 shards (50/50) -> 1 extra, waste 50.
	// key C built once -> not redundant. A duplicate of A within one file counts once.
	misses := []MissBuild{
		{Project: "web", Target: "test", Hash: "aaaa", DurationMs: 100, File: "s1"},
		{Project: "web", Target: "test", Hash: "aaaa", DurationMs: 999, File: "s1"}, // same file, ignored
		{Project: "web", Target: "test", Hash: "aaaa", DurationMs: 200, File: "s2"},
		{Project: "web", Target: "test", Hash: "aaaa", DurationMs: 300, File: "s3"},
		{Project: "api", Target: "build", Hash: "bbbb", DurationMs: 50, File: "s1"},
		{Project: "api", Target: "build", Hash: "bbbb", DurationMs: 50, File: "s2"},
		{Project: "cli", Target: "lint", Hash: "cccc", DurationMs: 10, File: "s1"},
	}
	res := Dedup(misses)

	if res.TotalMisses != 7 {
		t.Errorf("TotalMisses = %d, want 7", res.TotalMisses)
	}
	if res.UniqueKeys != 3 {
		t.Errorf("UniqueKeys = %d, want 3", res.UniqueKeys)
	}
	if res.RedundantBuilds != 3 {
		t.Errorf("RedundantBuilds = %d, want 3", res.RedundantBuilds)
	}
	if res.RedundantMs != 350 {
		t.Errorf("RedundantMs = %d, want 350", res.RedundantMs)
	}
	if res.Approx {
		t.Error("Approx = true, want false (all hashes present)")
	}
	if len(res.Top) != 2 {
		t.Fatalf("len(Top) = %d, want 2", len(res.Top))
	}
	if res.Top[0].Hash != "aaaa" || res.Top[0].ExtraMs != 300 || res.Top[0].ExtraBuilds != 2 {
		t.Errorf("Top[0] = %+v, want aaaa/300/2", res.Top[0])
	}
	if res.Top[1].Hash != "bbbb" || res.Top[1].ExtraMs != 50 {
		t.Errorf("Top[1] = %+v, want bbbb/50", res.Top[1])
	}
}

func TestDedup_EmptyHashIsApproximate(t *testing.T) {
	res := Dedup([]MissBuild{
		{Project: "web", Target: "test", Hash: "", DurationMs: 100, File: "s1"},
		{Project: "web", Target: "test", Hash: "", DurationMs: 100, File: "s2"},
	})
	if !res.Approx {
		t.Error("Approx = false, want true (missing hash)")
	}
	if res.RedundantBuilds != 1 {
		t.Errorf("RedundantBuilds = %d, want 1", res.RedundantBuilds)
	}
}

func TestDedup_NoMisses(t *testing.T) {
	res := Dedup(nil)
	if res.TotalMisses != 0 || res.RedundantBuilds != 0 || len(res.Top) != 0 {
		t.Errorf("empty input gave %+v", res)
	}
}

func TestDedup_MixedHashCoarsensToApprox(t *testing.T) {
	// Same (project, target) on two shards, one with a hash and one (older report)
	// without. A missing hash coarsens the analysis to (project, target), so these
	// collapse into ONE redundant key instead of two keys that would undercount.
	res := Dedup([]MissBuild{
		{Project: "web", Target: "test", Hash: "aaaa", DurationMs: 100, File: "s1"},
		{Project: "web", Target: "test", Hash: "", DurationMs: 200, File: "s2"},
	})
	if !res.Approx {
		t.Error("Approx = false, want true (one event lacks a hash)")
	}
	if res.UniqueKeys != 1 {
		t.Errorf("UniqueKeys = %d, want 1 (coarsened to project,target)", res.UniqueKeys)
	}
	if res.RedundantBuilds != 1 {
		t.Errorf("RedundantBuilds = %d, want 1", res.RedundantBuilds)
	}
	if res.RedundantMs != 100 { // 100+200 - max(200) = 100
		t.Errorf("RedundantMs = %d, want 100", res.RedundantMs)
	}
}

func TestDedup_TopOrderStableOnTies(t *testing.T) {
	// Two redundant keys with identical wasted time must sort by identity, so Top's
	// order is deterministic rather than dependent on map iteration.
	misses := []MissBuild{
		{Project: "web", Target: "zeta", Hash: "h", DurationMs: 50, File: "s1"},
		{Project: "web", Target: "zeta", Hash: "h", DurationMs: 50, File: "s2"},
		{Project: "api", Target: "alpha", Hash: "h", DurationMs: 50, File: "s1"},
		{Project: "api", Target: "alpha", Hash: "h", DurationMs: 50, File: "s2"},
	}
	for i := 0; i < 8; i++ {
		res := Dedup(misses)
		if len(res.Top) != 2 {
			t.Fatalf("len(Top) = %d, want 2", len(res.Top))
		}
		// Equal ExtraMs (50 each) -> tiebreak by project: "api" < "web".
		if res.Top[0].Project != "api" || res.Top[1].Project != "web" {
			t.Errorf("Top order = [%s, %s], want [api, web]", res.Top[0].Project, res.Top[1].Project)
		}
	}
}
