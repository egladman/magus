//go:build !buzz_safe && !buzz_unsafe

package buzz_test

import (
	"context"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The attribution has to name the loop that is growing the heap. Reporting a
// large heap without saying where leaves the reader bisecting a magusfile by
// hand, which is the whole reason this exists.
//
// The program allocates LINEARLY - one slot per append. An earlier version used
// the pathological `kept = kept + line` shape instead, which drove this one test
// binary to a 13.5GB peak and killed a CI shard. That is an absurd price for a
// test about a diagnostic, and the diagnostic only needs a hot loop that grows.
//
// The assertion is that the loop is SURFACED, not that it ranks first. Sampling
// catches whichever instruction a tick landed on, so the exact ordering and the
// exact line vary by machine speed: an earlier version demanded the top slot and
// passed on macOS while failing on a Linux runner, which is a test asserting a
// probabilistic property as if it were deterministic.
//
// Either line 4 (the foreach) or line 5 (the append) is a correct answer; both put
// the reader on the same two lines, which is the whole job.
func TestHeapHotSitesNamesTheGrowingLine(t *testing.T) {
	const src = `
fun grow() > int {
    var parts = mut [<str>];
    foreach (i in 0..20000) {
        parts.append("a line of coverage profile text");
    }
    return parts.len();
}
grow();
`
	// Both tests here read the same process-global attribution table, and both
	// programs put their loop on the same line number, so without a reset each
	// could be satisfied by the other's samples.
	vm.ResetHeapStats()

	s := buzz.NewSession(context.Background())
	_, err := s.Eval(context.Background(), src)
	require.NoError(t, err)

	sites, _ := vm.HeapHotSites(10)
	require.NotEmpty(t, sites, "a loop of 20000 allocating iterations must have been sampled")

	var found *vm.HeapSite
	for i, site := range sites {
		if strings.HasSuffix(site.Site, ":4") || strings.HasSuffix(site.Site, ":5") {
			found = &sites[i]
			break
		}
	}
	require.NotNilf(t, found,
		"the growing loop (line 4 or 5) must appear among the hot sites, got %v", sites)
	assert.Positive(t, found.Objects, "the loop's entry must carry a growth figure")
}

// Every reported site must name a line a reader can actually open. A chunk with no
// line data used to surface as "<main>:?" and, on a runner, outranked the real
// loop - a ranking led by an entry nobody can act on is worse than a shorter one.
func TestHeapHotSitesNamesOnlyRealLines(t *testing.T) {
	vm.ResetHeapStats()

	s := buzz.NewSession(context.Background())
	_, err := s.Eval(context.Background(), `
fun churn() > int {
    var xs = mut [<str>];
    foreach (i in 0..20000) { xs.append("x"); }
    return xs.len();
}
churn();
`)
	require.NoError(t, err)

	sites, _ := vm.HeapHotSites(20)
	for _, site := range sites {
		assert.NotContainsf(t, site.Site, ":?",
			"site %q has no line number and cannot be acted on", site.Site)
	}
}
