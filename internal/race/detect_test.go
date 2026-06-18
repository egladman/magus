package race

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staticFilter is a test-only gitFilter substitute.
type staticFilter struct{ allowed map[string]bool }

func (f *staticFilter) Allow(path string) bool { return f.allowed[path] }

// TestDetect_NoFinding_SingleWriter: when only one project's snapshot shows it
// wrote a path (e.g. format rewriting its own source while another project runs),
// no finding is emitted.
func TestDetect_NoFinding_SingleWriter(t *testing.T) {
	now := time.Now()
	s := snapshot{
		intervals: []interval{
			{
				Project: "api", Target: "format",
				StartedAt: now, EndedAt: now.Add(5 * time.Second),
				WrittenPaths: []string{"/ws/api/main.go"}, // api wrote its own file
			},
			{
				Project: "worker", Target: "format",
				StartedAt: now.Add(time.Second), EndedAt: now.Add(6 * time.Second),
				WrittenPaths: nil, // worker did not write /ws/api/main.go
			},
		},
		events: []fsEvent{
			{Path: "/ws/api/main.go", ObservedAt: now.Add(2 * time.Second)},
		},
	}
	filter := staticFilter{allowed: map[string]bool{"/ws/api/main.go": true}}
	findings := detect(s, &filter)
	assert.Empty(t, findings, "expected 0 findings for single-writer self-rewrite")
}

// TestDetect_NoFinding_NoAttribution: a path written during concurrent overlap
// but with no snapshot data (outside declared output dirs) produces no finding.
// This is the go.work.sum scenario: it lives at the workspace root, not under
// any project's declared output dirs.
func TestDetect_NoFinding_NoAttribution(t *testing.T) {
	now := time.Now()
	s := snapshot{
		intervals: []interval{
			{
				Project: "api", Target: "build",
				StartedAt: now, EndedAt: now.Add(5 * time.Second),
				WrittenPaths: nil, // no snapshot data for workspace-root files
			},
			{
				Project: "worker", Target: "build",
				StartedAt: now.Add(time.Second), EndedAt: now.Add(6 * time.Second),
				WrittenPaths: nil,
			},
		},
		events: []fsEvent{
			{Path: "/ws/go.work.sum", ObservedAt: now.Add(2 * time.Second)},
		},
	}
	filter := staticFilter{allowed: map[string]bool{"/ws/go.work.sum": true}}
	findings := detect(s, &filter)
	assert.Empty(t, findings, "expected 0 findings for unattributed path")
}

// TestDetect_OneFinding_TwoConfirmedWriters: two projects both show the same
// declared-output path in their snapshots → exactly one MGS4001 finding.
func TestDetect_OneFinding_TwoConfirmedWriters(t *testing.T) {
	now := time.Now()
	sharedPath := "/ws/shared/dist/bundle.js"
	s := snapshot{
		intervals: []interval{
			{
				Project: "api", Target: "build",
				StartedAt: now, EndedAt: now.Add(5 * time.Second),
				WrittenPaths: []string{sharedPath},
			},
			{
				Project: "worker", Target: "build",
				StartedAt: now.Add(time.Second), EndedAt: now.Add(6 * time.Second),
				WrittenPaths: []string{sharedPath},
			},
		},
		events: []fsEvent{
			{Path: sharedPath, ObservedAt: now.Add(2 * time.Second)},
		},
	}
	filter := staticFilter{allowed: map[string]bool{sharedPath: true}}
	findings := detect(s, &filter)
	require.Len(t, findings, 1, "expected 1 finding for two confirmed writers")
	f := findings[0]
	assert.Equal(t, sharedPath, f.path)
	// Canonical order: alphabetical by project.
	assert.Equal(t, "api", f.projectA)
	assert.Equal(t, "worker", f.projectB)
	assert.Equal(t, "build", f.target)
}

// TestDetect_NoFinding_CrossTarget: concurrent projects running different targets
// are a scheduling artifact, not a race — even with confirmed writers.
func TestDetect_NoFinding_CrossTarget(t *testing.T) {
	now := time.Now()
	sharedPath := "/ws/shared/out.js"
	s := snapshot{
		intervals: []interval{
			{
				Project: "api", Target: "build",
				StartedAt: now, EndedAt: now.Add(5 * time.Second),
				WrittenPaths: []string{sharedPath},
			},
			{
				Project: "worker", Target: "test",
				StartedAt: now, EndedAt: now.Add(5 * time.Second),
				WrittenPaths: []string{sharedPath},
			},
		},
		events: []fsEvent{
			{Path: sharedPath, ObservedAt: now.Add(time.Second)},
		},
	}
	filter := staticFilter{allowed: map[string]bool{sharedPath: true}}
	findings := detect(s, &filter)
	assert.Empty(t, findings, "expected 0 findings for cross-target overlap")
}

// TestDetect_NoFinding_Sequential: non-overlapping runs cannot produce a race.
func TestDetect_NoFinding_Sequential(t *testing.T) {
	now := time.Now()
	sharedPath := "/ws/shared/out.js"
	s := snapshot{
		intervals: []interval{
			{
				Project: "api", Target: "build",
				StartedAt: now, EndedAt: now.Add(2 * time.Second),
				WrittenPaths: []string{sharedPath},
			},
			{
				Project: "worker", Target: "build",
				StartedAt: now.Add(3 * time.Second), EndedAt: now.Add(5 * time.Second),
				WrittenPaths: []string{sharedPath},
			},
		},
		events: []fsEvent{
			{Path: sharedPath, ObservedAt: now.Add(4 * time.Second)},
		},
	}
	filter := staticFilter{allowed: map[string]bool{sharedPath: true}}
	findings := detect(s, &filter)
	assert.Empty(t, findings, "expected 0 findings for sequential runs")
}

// TestDetect_NoFinding_FilteredPath: non-git-tracked files are excluded.
func TestDetect_NoFinding_FilteredPath(t *testing.T) {
	now := time.Now()
	s := snapshot{
		intervals: []interval{
			{
				Project: "api", Target: "build", StartedAt: now, EndedAt: now.Add(5 * time.Second),
				WrittenPaths: []string{"/ws/.cache/something"},
			},
			{
				Project: "worker", Target: "build", StartedAt: now, EndedAt: now.Add(5 * time.Second),
				WrittenPaths: []string{"/ws/.cache/something"},
			},
		},
		events: []fsEvent{
			{Path: "/ws/.cache/something", ObservedAt: now.Add(time.Second)},
		},
	}
	filter := staticFilter{allowed: map[string]bool{}} // nothing allowed
	findings := detect(s, &filter)
	assert.Empty(t, findings, "expected 0 findings (filtered path)")
}

// TestDetect_MaxFindings: finding count is capped at maxFindings.
func TestDetect_MaxFindings(t *testing.T) {
	now := time.Now()
	ivs := []interval{
		{Project: "api", Target: "build", StartedAt: now, EndedAt: now.Add(10 * time.Second)},
		{Project: "worker", Target: "build", StartedAt: now, EndedAt: now.Add(10 * time.Second)},
	}
	var evs []fsEvent
	allowed := make(map[string]bool, maxFindings+10)
	for i := 0; i < maxFindings+10; i++ {
		p := "/ws/file" + string(rune('a'+i%26)) + string(rune('A'+i/26))
		evs = append(evs, fsEvent{Path: p, ObservedAt: now.Add(time.Second)})
		allowed[p] = true
		// Set both projects as confirmed writers for each path.
		ivs[0].WrittenPaths = append(ivs[0].WrittenPaths, p)
		ivs[1].WrittenPaths = append(ivs[1].WrittenPaths, p)
	}
	s := snapshot{intervals: ivs, events: evs}
	filter := staticFilter{allowed: allowed}
	findings := detect(s, &filter)
	assert.LessOrEqual(t, len(findings), maxFindings, "findings should not exceed cap")
}

// TestConfirmedWriters_ExactOne: only one project confirms the path → empty result.
func TestConfirmedWriters_ExactOne(t *testing.T) {
	ivs := []interval{
		{Project: "api", Target: "build", WrittenPaths: []string{"/ws/out.js"}},
		{Project: "worker", Target: "build", WrittenPaths: nil},
	}
	got := confirmedWriters("/ws/out.js", ivs)
	require.Len(t, got, 1)
	assert.Equal(t, "api", got[0].Project)
}

// TestConfirmedWriters_Both: both projects confirm the path → both returned.
func TestConfirmedWriters_Both(t *testing.T) {
	ivs := []interval{
		{Project: "api", Target: "build", WrittenPaths: []string{"/ws/out.js"}},
		{Project: "worker", Target: "build", WrittenPaths: []string{"/ws/out.js"}},
	}
	got := confirmedWriters("/ws/out.js", ivs)
	assert.Len(t, got, 2)
}

// TestConfirmedWriters_None: no snapshot data → empty result.
func TestConfirmedWriters_None(t *testing.T) {
	ivs := []interval{
		{Project: "api", Target: "build"},
		{Project: "worker", Target: "build"},
	}
	got := confirmedWriters("/ws/go.work.sum", ivs)
	assert.Empty(t, got, "expected empty (no snapshot data)")
}

func TestWriteReportJSON_Schema3(t *testing.T) {
	now := time.Now()
	findings := []finding{
		{
			path:         "/ws/shared/bundle.js",
			projectA:     "api",
			projectB:     "worker",
			target:       "build",
			overlapStart: now,
			overlapEnd:   now.Add(10 * time.Millisecond),
		},
	}
	var buf strings.Builder
	writeReportJSON(&buf, findings)
	out := buf.String()
	for _, want := range []string{
		`"schema":3`,
		`"total":1`,
		`"path":"/ws/shared/bundle.js"`,
		`"project_a":"api"`,
		`"project_b":"worker"`,
		`"target":"build"`,
	} {
		assert.Contains(t, out, want)
	}
	// Ensure removed fields are absent.
	for _, absent := range []string{`"tier"`, `"flipped"`, `"suppression_snippet"`, `"likely_writer"`} {
		assert.NotContains(t, out, absent)
	}
}

func TestWriteReportJSON_EmptySchema3(t *testing.T) {
	var buf strings.Builder
	writeReportJSON(&buf, nil)
	out := buf.String()
	assert.Contains(t, out, `"schema":3`, "empty findings should still emit schema:3")
	assert.Contains(t, out, `"total":0`, "empty findings should emit total:0")
}

func TestWrittenPaths_RoundTrip(t *testing.T) {
	rt := NewRuntime("/tmp/ws")
	// Don't start the watcher; we only need the recorder for interval bookkeeping.
	rt.rec.startInterval("api", "build")
	rt.rec.endInterval("api", "build")
	rt.rec.setWrittenPaths("api", "build", []string{"/tmp/ws/api/dist/x.js"})

	got := rt.WrittenPaths()
	assert.Equal(t, map[string][]string{"api": {"/tmp/ws/api/dist/x.js"}}, got)
}

func TestWrittenPaths_NilRuntime(t *testing.T) {
	var rt *Runtime
	assert.Nil(t, rt.WrittenPaths(), "nil runtime should return nil")
}
