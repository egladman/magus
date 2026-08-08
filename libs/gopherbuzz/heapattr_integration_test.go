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
// The assertion is on the REGION, not the exact statement. Sampling catches
// whichever instruction the tick landed on, and a tight loop runs its control as
// often as its body, so either the `foreach` header or the append inside it is a
// correct answer. Both put the reader on the same few lines.
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
	// Line 4 is the foreach, line 5 the append inside it.
	wantLines := []string{":4", ":5"}

	s := buzz.NewSession(context.Background())
	_, err := s.Eval(context.Background(), src)
	require.NoError(t, err)

	sites, _ := vm.HeapHotSites(5)
	require.NotEmpty(t, sites, "a loop of 20000 allocating iterations must have been sampled")

	var named bool
	for _, want := range wantLines {
		named = named || strings.HasSuffix(sites[0].Site, want)
	}
	assert.Truef(t, named,
		"the hottest site should be the growing loop (line 4 or 5), got %q (top 5: %v)", sites[0].Site, sites)
	assert.Positive(t, sites[0].Objects, "the hottest site must carry a growth figure")
}
