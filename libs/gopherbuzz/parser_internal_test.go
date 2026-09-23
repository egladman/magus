package buzz

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTokenCache_AccountsKeyBytesTowardTheBound pins the fix for an undercount: the map
// key holds the whole source string alive, not just the token slice, so a size that
// ignored len(src) let tokenCache retain far more than maxTokenCacheBytes advertises.
// Priming bytes near the bound and inserting one more entry exercises both the reset
// and the post-reset accounting without needing to actually lex 64MB of source.
func TestTokenCache_AccountsKeyBytesTowardTheBound(t *testing.T) {
	origM, origBytes := tokenCache.m, tokenCache.bytes
	t.Cleanup(func() {
		tokenCache.Lock()
		tokenCache.m, tokenCache.bytes = origM, origBytes
		tokenCache.Unlock()
	})
	tokenCache.Lock()
	tokenCache.m = map[string][]token.Token{}
	tokenCache.bytes = maxTokenCacheBytes - 10
	tokenCache.Unlock()

	src := strings.Repeat("x", minCachedSource) + " var y = 1;\n"
	toks, err := tokenize(src)
	require.NoError(t, err)
	require.NotEmpty(t, toks)

	tokenCache.Lock()
	defer tokenCache.Unlock()
	assert.LessOrEqual(t, tokenCache.bytes, maxTokenCacheBytes,
		"the accounted size must never exceed the bound that is supposed to trigger a reset")
	wantSize := len(toks)*int(unsafe.Sizeof(token.Token{})) + len(src)
	assert.Equal(t, wantSize, tokenCache.bytes,
		"the near-full cache must have reset before inserting, leaving exactly this entry's size")
}
