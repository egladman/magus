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

// The attribution has to survive the case it was built for: a string grown by
// reassignment inside a loop, which is what turned 2.1MB of output into 13.1GB of
// pinned intermediates and killed a CI runner. Reporting a large heap without
// saying where leaves the reader bisecting a magusfile by hand.
//
// The assertion is on the REGION, not the exact statement. Sampling catches
// whichever instruction the tick landed on, and a tight loop runs its control as
// often as its body, so either the `foreach` header or the concatenation inside it
// is a correct answer. Both put the reader on the same five lines, which is what
// the diagnostic is for.
func TestHeapHotSitesNamesTheGrowingLine(t *testing.T) {
	const src = `
fun grow() > str {
    var kept = "";
    foreach (i in 0..40000) {
        kept = kept + "a line of coverage profile text\n";
    }
    return kept;
}
grow();
`
	// Line 4 is the foreach, line 5 the concatenation inside it. Either identifies
	// the growing loop.
	wantLines := []string{":4", ":5"}

	s := buzz.NewSession(context.Background())
	_, err := s.Eval(context.Background(), src)
	require.NoError(t, err)

	sites, _ := vm.HeapHotSites(5)
	require.NotEmpty(t, sites, "a loop that allocated its way through 40000 iterations must have been sampled")

	var named bool
	for _, want := range wantLines {
		named = named || strings.HasSuffix(sites[0].Site, want)
	}
	assert.Truef(t, named,
		"the hottest site should be the growing loop (line 4 or 5), got %q (top 5: %v)", sites[0].Site, sites)
	assert.Positive(t, sites[0].Objects, "the hottest site must carry a growth figure")
}
