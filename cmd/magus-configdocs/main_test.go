package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/config/gen/settings"
)

// TestRenderNamesEveryField: the reference is the settings table, so each
// key and its environment variable reach the page. Whether the committed page is this
// render is docs generate's drift gate.
func TestRenderNamesEveryField(t *testing.T) {
	page := render()
	for _, f := range settings.Settings {
		assert.Contains(t, page, fmt.Sprintf("`%s`", f.YamlPath))
		if f.EnvVar != "" {
			assert.Contains(t, page, fmt.Sprintf("`%s`", f.EnvVar))
		}
	}
}
