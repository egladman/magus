package tty

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilter_AND(t *testing.T) {
	items := []string{
		"apps/web/dashboard",
		"apps/mobile/dashboard",
		"services/api",
		"tools/scripts",
	}

	t.Run("empty matches all", func(t *testing.T) {
		assert.Equal(t, []int{0, 1, 2, 3}, Filter(items, ""))
	})
	t.Run("single token substring", func(t *testing.T) {
		assert.Equal(t, []int{0, 1}, Filter(items, "dash"))
	})
	t.Run("AND narrows", func(t *testing.T) {
		assert.Equal(t, []int{1}, Filter(items, "dash mobile"))
	})
	t.Run("AND no match", func(t *testing.T) {
		assert.Empty(t, Filter(items, "dash api"))
	})
	t.Run("case insensitive", func(t *testing.T) {
		assert.Equal(t, []int{0}, Filter(items, "DASH WEB"))
	})
	t.Run("order independent", func(t *testing.T) {
		assert.Equal(t, []int{1}, Filter(items, "mobile dash"))
	})
}
