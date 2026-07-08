package race

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// t0 is a fixed base instant so report output is deterministic.
var t0 = time.Unix(0, 0).UTC()

func TestWriteReportJSON_Empty(t *testing.T) {
	var sb strings.Builder
	writeReportJSON(&sb, nil)
	assert.Equal(t, "{\"schema\":3,\"summary\":{\"total\":0},\"findings\":[]}\n", sb.String())
}

func TestWriteReportJSON_SortsByPathAndRendersEveryField(t *testing.T) {
	// Deliberately out of order so we exercise the path sort.
	findings := []finding{
		{
			path:         "/repo/z.txt",
			projectA:     "web",
			projectB:     "api",
			target:       "build",
			overlapStart: t0,
			overlapEnd:   t0.Add(2 * time.Millisecond),
		},
		{
			path:         "/repo/a.txt",
			projectA:     "docs",
			projectB:     "cli",
			target:       "gen",
			overlapStart: t0.Add(1 * time.Millisecond),
			overlapEnd:   t0.Add(5 * time.Millisecond),
		},
	}

	var sb strings.Builder
	writeReportJSON(&sb, findings)

	want := `{"schema":3,"summary":{"total":2},"findings":[
  {"path":"/repo/a.txt","project_a":"docs","project_b":"cli","target":"gen","overlap_start_ns":1000000,"overlap_end_ns":5000000},
  {"path":"/repo/z.txt","project_a":"web","project_b":"api","target":"build","overlap_start_ns":0,"overlap_end_ns":2000000}
]}
`
	assert.Equal(t, want, sb.String())
}

func TestRaceFindings_String_UnderCap(t *testing.T) {
	fs := raceFindings{
		{path: "/repo/dir/out.js", projectA: "a", projectB: "b", target: "build", overlapStart: t0, overlapEnd: t0.Add(3 * time.Millisecond)},
	}
	assert.Equal(t, "out.js [a|b] build overlap=3ms", fs.String())
}

func TestRaceFindings_String_CapsAndCountsRemainder(t *testing.T) {
	var fs raceFindings
	for i := 0; i < inlineCap+2; i++ {
		fs = append(fs, finding{
			path:         "/repo/f.txt",
			projectA:     "a",
			projectB:     "b",
			target:       "build",
			overlapStart: t0,
			overlapEnd:   t0.Add(time.Millisecond),
		})
	}

	got := fs.String()
	// Only inlineCap findings are rendered inline, then a "+N more" tail.
	assert.Equal(t, inlineCap, strings.Count(got, "overlap="))
	assert.Contains(t, got, "+2 more")
}

func TestRaceFindings_LogValue_MatchesString(t *testing.T) {
	fs := raceFindings{
		{path: "/repo/x", projectA: "a", projectB: "b", target: "t", overlapStart: t0, overlapEnd: t0.Add(time.Millisecond)},
	}
	assert.Equal(t, fs.String(), fs.LogValue().String())
}
