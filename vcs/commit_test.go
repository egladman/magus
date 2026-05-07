package vcs

import "testing"

const nul = "\x00"

func TestParseCommit(t *testing.T) {
	// Fields in fixed order: id, short, authorName, authorEmail, date, parents, message.
	raw := "abc123def" + nul + "abc123d" + nul + "Alice" + nul + "alice@example.com" + nul +
		"2026-06-04T12:34:56+00:00" + nul + "p1 p2" + nul + "subject line\n\nbody text"
	c := parseCommit(raw)
	if c.ID != "abc123def" {
		t.Errorf("ID = %q", c.ID)
	}
	if c.Short != "abc123d" {
		t.Errorf("Short = %q", c.Short)
	}
	if c.Author.Name != "Alice" || c.Author.Email != "alice@example.com" {
		t.Errorf("Author = %+v", c.Author)
	}
	if c.Subject != "subject line" || c.Body != "body text" {
		t.Errorf("Subject/Body = %q / %q", c.Subject, c.Body)
	}
	if len(c.Parents) != 2 || c.Parents[0] != "p1" || c.Parents[1] != "p2" {
		t.Errorf("Parents = %v", c.Parents)
	}
	if c.Date.IsZero() || c.Date.Year() != 2026 {
		t.Errorf("Date = %v", c.Date)
	}
}

func TestParseCommitEmptyAndShort(t *testing.T) {
	// Empty driver output → empty ID (FindCommit turns this into an error).
	if c := parseCommit(""); c.ID != "" {
		t.Errorf("empty input ID = %q, want empty", c.ID)
	}
	// A short record (fewer than numCommitFields) must not panic.
	if c := parseCommit("onlyid"); c.ID != "onlyid" || c.Subject != "" {
		t.Errorf("short record = %+v", c)
	}
}

func TestParents(t *testing.T) {
	null40 := "0000000000000000000000000000000000000000"
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a b c", []string{"a", "b", "c"}},
		{"a  b", []string{"a", "b"}},      // collapse runs of whitespace
		{"a " + null40, []string{"a"}},    // drop the null p2node sentinel
		{null40, nil},                     // all-null → empty
	}
	for _, tc := range cases {
		got := parents(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("parents(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parents(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestSplitMessage(t *testing.T) {
	cases := []struct{ in, subject, body string }{
		{"", "", ""},
		{"just a subject", "just a subject", ""},
		{"subject\n\nbody", "subject", "body"},
		{"subject\nbody no blank", "subject", "body no blank"},
		{"  trimmed  ", "trimmed", ""},
		{"sub\r\nbody", "sub", "body"}, // CRLF: the \r is trimmed off the subject
	}
	for _, tc := range cases {
		s, b := splitMessage(tc.in)
		if s != tc.subject || b != tc.body {
			t.Errorf("splitMessage(%q) = (%q, %q), want (%q, %q)", tc.in, s, b, tc.subject, tc.body)
		}
	}
}

func TestParseWhen(t *testing.T) {
	cases := []struct {
		in   string
		zero bool
	}{
		{"2026-06-04T12:34:56+00:00", false}, // RFC3339 colon offset (git %cI, hg, jj %:z)
		{"2026-06-04T12:34:56Z", false},      // Z
		{"2026-06-04T12:34:56+0000", false},  // no-colon offset — the defensive fallback
		{"", true},
		{"not a date", true},
	}
	for _, tc := range cases {
		if got := parseWhen(tc.in); got.IsZero() != tc.zero {
			t.Errorf("parseWhen(%q).IsZero() = %v, want %v", tc.in, got.IsZero(), tc.zero)
		}
	}
}
