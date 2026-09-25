package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCodeSpanShowsAFileNameLiterally(t *testing.T) {
	for in, want := range map[string]string{
		"libs/a.go":            "`libs/a.go`",
		"a`b.go":               "``a`b.go``",
		"``` fence":            "```` ``` fence ````",
		"x`":                   "`` x` ``",
		" padded ":             "`  padded  `",
		"x\ny.md":              "`\"x\\ny.md\"`",
		"a\r\n<!-- -->":        "`\"a\\r\\n<!-- -->\"`",
		"@org/team":            "`@org/team`",
		"--> <img src=x>":      "`--> <img src=x>`",
		"evil\u202egpj.exe":    "`\"evil\\u202egpj.exe\"`",
		"\xff.go":              "`\"\\xff.go\"`",
		"":                     "`\"\"`",
		"tab\there":            "`\"tab\\there\"`",
		"#12 is not a link":    "`#12 is not a link`",
		"[x](https://evil.io)": "`[x](https://evil.io)`",
	} {
		assert.Equal(t, want, CodeSpan(in), "%q", in)
	}
	assert.Equal(t, "`h\xc3\xa9llo.md`", CodeSpan("h\xc3\xa9llo.md"), "a printable rune is shown as it is")
}

func TestCodeBlockCannotBeClosedByItsContent(t *testing.T) {
	assert.Equal(t, "```text\nexit 1\n```\n", CodeBlock("exit 1\n"))
	got := CodeBlock("```\n@everyone\n`````\n<!-- x -->")
	assert.Equal(t, "``````text\n```\n@everyone\n`````\n<!-- x -->\n``````\n", got)
	assert.Equal(t, "```text\n\uFFFD\n```\n", CodeBlock("\xff"))
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n")[1:5] {
		assert.Less(t, len(line)-len(strings.TrimLeft(line, "`")), 6, "no content line opens or closes a six-backtick fence: %q", line)
	}
}
