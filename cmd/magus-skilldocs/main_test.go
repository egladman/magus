package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriteFencedCountsIndentedClosingFence(t *testing.T) {
	var got strings.Builder
	writeFenced(&got, "- example:\n\n  ```sh\n  magus affected ci\n  ```")

	assert.True(t, strings.HasPrefix(got.String(), "````markdown\n"))
	assert.True(t, strings.HasSuffix(got.String(), "\n````\n\n"))
}
