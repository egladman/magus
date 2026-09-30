package figure

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSourceIsTheFigureModule(t *testing.T) {
	assert.True(t, strings.Contains(Source, "\nnamespace figure;\n"), "figure.buzz declares the figure namespace")
	assert.True(t, strings.Contains(Source, "\nexport fun of(id: str) > mut Figure {"), "of() is the module's entry point")
}
