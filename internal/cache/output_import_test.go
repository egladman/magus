package cache

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputImportNeutralizesTerminalControls(t *testing.T) {
	cases := []struct {
		name, in, want string
		keepSGR        bool
		escaped        int
	}{
		{"osc 52 clipboard write", "a\x1b]52;c;ZXZpbA==\x07b", "a<U+001B>]52;c;ZXZpbA==<U+0007>b", true, 2},
		{"osc 8 link", "\x1b]8;;https://evil\x1b\\x", "<U+001B>]8;;https://evil<U+001B>\\x", true, 2},
		{"osc 0 title", "\x1b]0;pwned\x07", "<U+001B>]0;pwned<U+0007>", true, 2},
		{"clear screen", "\x1b[2J", "<U+001B>[2J", true, 1},
		{"cursor up", "ok\x1b[1Afake", "ok<U+001B>[1Afake", true, 1},
		{"c1 csi", "x\u009b2Jy", "x<U+009B>2Jy", true, 1},
		{"nul and del", "a\x00b\x7f", "a<U+0000>b<U+007F>", true, 2},
		{"bidi override", "a" + string(rune(0x202E)) + "b", "a<U+202E>b", true, 1},
		{"invalid byte", "a\xffb", "a<0xFF>b", true, 1},
		{"bare cr cannot overwrite", "FAIL\rPASS\r\n", "FAIL\nPASS\n", true, 0},
		{"newline and tab pass", "a\tb\nc", "a\tb\nc", true, 0},
		{"red passes", "\x1b[1;31mFAIL\x1b[0m", "\x1b[1;31mFAIL\x1b[0m", true, 0},
		{"conceal escaped", "\x1b[8mhidden", "<U+001B>[8mhidden", true, 1},
		{"background escaped", "\x1b[31;41mx", "<U+001B>[31;41mx", true, 1},
		{"rgb escaped", "\x1b[38;2;0;0;0mx", "<U+001B>[38;2;0;0;0mx", true, 1},
		{"no sgr for fields", "\x1b[31mx", "<U+001B>[31mx", false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, n := NeutralizeTerminal([]byte(c.in), c.keepSGR)
			assert.Equal(t, c.want, string(got))
			assert.Equal(t, c.escaped, n)
		})
	}
}

func validImportedDescriptor() OutputDescriptor {
	key := strings.Repeat("ab", 32)
	return OutputDescriptor{
		Ref: PortableRef(key), Key: key, Attempt: "out1234abcd", Project: "libs/x", Target: "lint:gha",
		Revision: strings.Repeat("c", 40), Platform: "linux/amd64", VCSName: "git", MagusVersion: "v0.5.0",
	}
}

func TestOutputImportRejectsForgedIdentity(t *testing.T) {
	require.NoError(t, CheckImportedDescriptor(validImportedDescriptor(), []ClassDigest{{Class: "src", Digest: "0123456789ab", Count: 3}}))

	cases := map[string]func(*OutputDescriptor){
		"short key":            func(d *OutputDescriptor) { d.Key = "abcd"; d.Ref = PortableRef(d.Key) },
		"path in key":          func(d *OutputDescriptor) { d.Key = "../../" + d.Key[6:] },
		"ref not from key":     func(d *OutputDescriptor) { d.Ref = "out000000000000" },
		"attempt traversal":    func(d *OutputDescriptor) { d.Attempt = "out../../x" },
		"newline in project":   func(d *OutputDescriptor) { d.Project = "a\nmagus x out1" },
		"escape in target":     func(d *OutputDescriptor) { d.Target = "lint\x1b]0;t\x07" },
		"bidi in target":       func(d *OutputDescriptor) { d.Target = "lint" + string(rune(0x202E)) },
		"empty project":        func(d *OutputDescriptor) { d.Project = "" },
		"long project":         func(d *OutputDescriptor) { d.Project = strings.Repeat("p", maxFieldBytes+1) },
		"bad platform":         func(d *OutputDescriptor) { d.Platform = "linux/amd64; rm -rf" },
		"bad revision":         func(d *OutputDescriptor) { d.Revision = "HEAD" },
		"bad vcs":              func(d *OutputDescriptor) { d.VCSName = "svn" },
		"bad inv":              func(d *OutputDescriptor) { d.Inv = "inv;ls" },
		"control in extra arg": func(d *OutputDescriptor) { d.ExtraArgs = []string{"-run\x07"} },
		"negative duration":    func(d *OutputDescriptor) { d.DurationMs = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := validImportedDescriptor()
			mutate(&d)
			assert.Error(t, CheckImportedDescriptor(d, nil))
		})
	}
	assert.Error(t, CheckImportedDescriptor(validImportedDescriptor(), []ClassDigest{{Class: "src\n", Digest: "0123456789ab"}}))
	assert.Error(t, CheckImportedDescriptor(validImportedDescriptor(), []ClassDigest{{Class: "src", Digest: "zz"}}))
}

func TestOutputImportEnforcesLimits(t *testing.T) {
	limits := ImportLimits{LineBytes: 16, TotalBytes: 40, Records: 3}
	read := func(in string) ([]int, error) {
		var lines []int
		err := ReadRecordLines(strings.NewReader(in), limits, func(line int, _ []byte) error {
			lines = append(lines, line)
			return nil
		})
		return lines, err
	}

	lines, err := read("a\n\nb\nc")
	require.NoError(t, err)
	assert.Equal(t, []int{1, 3, 4}, lines, "blank lines are skipped but still counted")

	_, err = read("a\n" + strings.Repeat("x", 17) + "\n")
	assert.EqualError(t, err, "line 2: longer than 16 bytes")

	_, err = read(strings.Repeat("123456789012345\n", 3))
	assert.EqualError(t, err, "line 3: input exceeds 40 bytes")

	_, err = read("a\nb\nc\nd\n")
	assert.EqualError(t, err, "line 4: more than 3 records")
}

// A line far past the limit is refused without being read whole.
func TestOutputImportDoesNotBufferAnOversizedLine(t *testing.T) {
	r := &countingReader{size: 1 << 30}
	err := ReadRecordLines(r, ImportLimits{LineBytes: 1 << 20, TotalBytes: 1 << 40, Records: 1}, func(int, []byte) error { return nil })
	assert.EqualError(t, err, "line 1: longer than 1048576 bytes")
	assert.Less(t, r.read, 2<<20)
}

type countingReader struct{ size, read int }

func (r *countingReader) Read(p []byte) (int, error) {
	n := min(len(p), r.size-r.read)
	copy(p, bytes.Repeat([]byte("x"), n))
	r.read += n
	return n, nil
}
