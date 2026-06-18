package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTailLines(t *testing.T) {
	t.Run("n=0 returns all", func(t *testing.T) {
		assert.Equal(t, []byte("a\nb\nc\n"), tailLines([]byte("a\nb\nc\n"), 0))
	})
	t.Run("n greater than line count returns all", func(t *testing.T) {
		assert.Equal(t, []byte("a\nb\nc\n"), tailLines([]byte("a\nb\nc\n"), 10))
	})
	t.Run("n equals line count returns all", func(t *testing.T) {
		assert.Equal(t, []byte("a\nb\nc\n"), tailLines([]byte("a\nb\nc\n"), 3))
	})
	t.Run("n less than line count returns last n", func(t *testing.T) {
		assert.Equal(t, []byte("c\nd\ne\n"), tailLines([]byte("a\nb\nc\nd\ne\n"), 3))
	})
	t.Run("n=1 returns last line", func(t *testing.T) {
		assert.Equal(t, []byte("c\n"), tailLines([]byte("a\nb\nc\n"), 1))
	})
	t.Run("empty input", func(t *testing.T) {
		assert.Equal(t, []byte{}, tailLines([]byte{}, 5))
	})
	t.Run("no trailing newline", func(t *testing.T) {
		assert.Equal(t, []byte("b\nc"), tailLines([]byte("a\nb\nc"), 2))
	})
}
