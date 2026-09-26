package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/schema"
)

// TestRenderNamesEveryField: the reference is the inventory of schema.Fields, so each
// key and its environment variable reach the page. Whether the committed page is this
// render is docs generate's drift gate.
func TestRenderNamesEveryField(t *testing.T) {
	page := render()
	for _, f := range schema.Fields {
		assert.Contains(t, page, "`"+f.YamlPath+"`")
		if f.EnvVar != "" {
			assert.Contains(t, page, "`"+f.EnvVar+"`")
		}
	}
}
