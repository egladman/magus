package docs

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFrontmatterRoundTrip(t *testing.T) {
	var b strings.Builder
	WriteFrontmatter(&b, Frontmatter{
		Title:       "Charms: argv edits",
		PageType:    "overview",
		Aliases:     []string{"/old-charms"},
		Description: "How charms work.",
		Tags:        []string{"reference", "argv"},
	})
	b.WriteString("Body text.\n")

	f, ok := ParseFrontmatter(b.String())
	require.True(t, ok)
	assert.Equal(t, "Charms: argv edits", f.Title)
	assert.Equal(t, "overview", f.PageType)
	assert.Equal(t, []string{"/old-charms"}, f.Aliases)
	assert.Equal(t, "How charms work.", f.Description)
	assert.Equal(t, []string{"reference", "argv"}, f.Tags)
}

func TestParseFrontmatterAbsent(t *testing.T) {
	for _, tc := range []struct {
		name, in string
	}{
		{"no fence", "# Heading\nBody.\n"},
		{"fence not first line", "\n---\ntitle: X\n---\n"},
		{"unterminated", "---\ntitle: X\nno closing fence\n"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := ParseFrontmatter(tc.in)
			assert.False(t, ok)
			assert.Equal(t, Frontmatter{}, f)
		})
	}
}

func TestParseFrontmatterEmptyBlock(t *testing.T) {
	f, ok := ParseFrontmatter("---\n---\nBody.\n")
	require.True(t, ok, "an empty but well-formed block parses")
	assert.Equal(t, Frontmatter{}, f)
}

func TestParseFrontmatterMalformedYAML(t *testing.T) {
	// A block whose contents are not valid YAML is treated as absent, never an error.
	f, ok := ParseFrontmatter("---\ntitle: [unterminated\n---\nBody.\n")
	assert.False(t, ok)
	assert.Equal(t, Frontmatter{}, f)
}

func TestStripFrontmatter(t *testing.T) {
	assert.Equal(t, "# Heading\nBody.\n", StripFrontmatter("---\ntitle: X\ntags: [a]\n---\n# Heading\nBody.\n"),
		"the body begins after the closing fence")
	assert.Equal(t, "# No block\n", StripFrontmatter("# No block\n"),
		"content with no leading block is returned unchanged")
	assert.Equal(t, "Body.\n", StripFrontmatter("---\n---\nBody.\n"), "empty block")
	assert.Equal(t, "---\ntitle: unterminated\nBody.\n", StripFrontmatter("---\ntitle: unterminated\nBody.\n"),
		"an unterminated block is not a block, so the content is left whole rather than eaten")
}
