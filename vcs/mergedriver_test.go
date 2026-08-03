package vcs

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testBegin = "# BEGIN magus-generated"
	testEnd   = "# END magus-generated"
)

func section(body string) string {
	return testBegin + " - do not edit this section manually\n" + body + testEnd + "\n"
}

func TestReplaceManagedSection(t *testing.T) {
	want := section("a.go merge=magus\n")

	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "empty file becomes the section alone",
			text: "",
			want: want,
		},
		{
			name: "unmanaged text keeps its content and gains the section",
			text: "*.png binary\n",
			want: "*.png binary\n\n" + want,
		},
		{
			name: "existing section is replaced in place",
			text: section("old.go merge=magus\n"),
			want: want,
		},
		{
			name: "surrounding hand-written lines survive on both sides",
			text: "*.png binary\n\n" + section("old.go merge=magus\n") + "\n*.jpg binary\n",
			want: "*.png binary\n\n" + want + "\n*.jpg binary\n",
		},
		{
			// The banner wording changed once (an em-dash went away). Matching begin as
			// a prefix keeps the old section findable instead of stranding it above a
			// freshly appended copy.
			name: "section written with the older banner wording is replaced, not duplicated",
			text: testBegin + " — do not edit this section manually\nold.go merge=magus\n" + testEnd + "\n",
			want: want,
		},
		{
			// The regression this was rewritten for: an end marker found by whole-text
			// scan matched the FIRST section's end, so a begin further down failed the
			// ordering test and appended instead of replacing, once per invocation.
			name: "accumulated duplicate sections collapse to one",
			text: section("first.go merge=magus\n") + "\n" +
				section("second.go merge=magus\n") + "\n" +
				section("third.go merge=magus\n"),
			want: want,
		},
		{
			name: "duplicates collapse without eating hand-written lines between them",
			text: section("first.go merge=magus\n") + "\nkeep-me\n" + section("second.go merge=magus\n"),
			want: want + "\nkeep-me\n",
		},
		{
			// A truncated or hand-mangled file must not have its remainder swallowed.
			name: "unterminated begin is left alone and the section is appended",
			text: "*.png binary\n" + testBegin + " - do not edit this section manually\ndangling\n",
			want: "*.png binary\n" + testBegin + " - do not edit this section manually\ndangling\n\n" + want,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceManagedSection(tt.text, want, testBegin, testEnd)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestReplaceManagedSectionIdempotent pins what the accumulation bug broke: applying the
// same section repeatedly converges after the first write.
func TestReplaceManagedSectionIdempotent(t *testing.T) {
	want := section("a.go merge=magus\n")
	text := "*.png binary\n"
	for range 5 {
		text = replaceManagedSection(text, want, testBegin, testEnd)
	}
	assert.Equal(t, "*.png binary\n\n"+want, text)
	assert.Equal(t, 1, strings.Count(text, testBegin), "exactly one managed section")
}
