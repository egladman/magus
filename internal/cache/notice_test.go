package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFailureExcerptFindsEarlyDiagnostic(t *testing.T) {
	t.Parallel()
	data := []byte("preparing suite\n# example.test\nlink: fingerprint mismatch: got A, want B\nok package/one\nok package/two\nFAIL\n")

	excerpt, omitted := failureExcerpt(data, maxFailureExcerptLines)

	assert.Positive(t, omitted)
	assert.Contains(t, string(excerpt), "# example.test")
	assert.Contains(t, string(excerpt), "link: fingerprint mismatch: got A, want B")
	assert.Contains(t, string(excerpt), "FAIL")
}

func TestFailureExcerptFallsBackToTail(t *testing.T) {
	t.Parallel()
	data := []byte("one\ntwo\nthree\nfour\n")

	excerpt, omitted := failureExcerpt(data, 2)

	assert.Equal(t, 2, omitted)
	assert.Equal(t, "three\nfour\n", string(excerpt))
}
