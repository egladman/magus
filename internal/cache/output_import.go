package cache

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/egladman/magus/internal/changeset"
)

// Output records read from stdin are untrusted: anyone can forge an artifact or a paste.
// So the reader stores nothing, bounds its input, checks every identity field's shape,
// and neutralizes terminal control in whatever it prints.

// ImportLimits bounds one read of output records.
type ImportLimits struct {
	LineBytes  int
	TotalBytes int
	Records    int
}

// DefaultImportLimits are the limits `magus query output --stdin` reads with.
var DefaultImportLimits = ImportLimits{LineBytes: 64 << 20, TotalBytes: 256 << 20, Records: 10_000}

// ReadRecordLines calls fn with each non-blank line of r and its 1-based number; past a
// limit it returns an error naming the line without buffering more of it.
func ReadRecordLines(r io.Reader, l ImportLimits, fn func(line int, raw []byte) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	total, records := 0, 0
	for line := 1; ; line++ {
		raw, err := readCappedLine(br, l.LineBytes)
		total += len(raw)
		switch {
		case errors.Is(err, errLineTooLong):
			return fmt.Errorf("line %d: longer than %d bytes", line, l.LineBytes)
		case err != nil && !errors.Is(err, io.EOF):
			return fmt.Errorf("line %d: %w", line, err)
		case total > l.TotalBytes:
			return fmt.Errorf("line %d: input exceeds %d bytes", line, l.TotalBytes)
		}
		if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 {
			if records++; records > l.Records {
				return fmt.Errorf("line %d: more than %d records", line, l.Records)
			}
			if ferr := fn(line, trimmed); ferr != nil {
				return ferr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

var errLineTooLong = errors.New("line too long")

func readCappedLine(br *bufio.Reader, maxBytes int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(line)+len(chunk) > maxBytes {
			return nil, errLineTooLong
		}
		line = append(line, chunk...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

var (
	fullKeyPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	attemptIDPattern   = regexp.MustCompile(`^` + RefPrefix + `[0-9a-f]{8,64}$`)
	revisionPattern    = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	platformPattern    = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)
	classLabelPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	classDigestPattern = regexp.MustCompile(`^[0-9a-f]{` + strconv.Itoa(classDigestHexLen) + `}$`)
)

const maxFieldBytes = 512

// CheckImportedDescriptor reports the first field of d or classes in a shape no magus
// writes, or nil; ErrMsg is free text and is left to NeutralizeTerminal.
func CheckImportedDescriptor(d OutputDescriptor, classes []ClassDigest) error {
	if !fullKeyPattern.MatchString(d.Key) {
		return fmt.Errorf("key %q is not a full cache key (64 lowercase hex)", d.Key)
	}
	if want := PortableRef(d.Key); d.Ref != want {
		return fmt.Errorf("ref %q does not derive from its key (want %s)", d.Ref, want)
	}
	if d.Attempt != "" && !attemptIDPattern.MatchString(d.Attempt) {
		return fmt.Errorf("attempt %q is not an attempt id", d.Attempt)
	}
	if d.Inv != "" && !LooksLikeInvocationID(d.Inv) {
		return fmt.Errorf("inv %q is not an invocation id", d.Inv)
	}
	if d.Revision != "" && !revisionPattern.MatchString(d.Revision) {
		return fmt.Errorf("revision %q is not a hex revision", d.Revision)
	}
	if d.Platform != "" && !platformPattern.MatchString(d.Platform) {
		return fmt.Errorf("platform %q is not GOOS/GOARCH", d.Platform)
	}
	switch d.VCSName {
	case "", "git", "hg", "sl", "jj":
	default:
		return fmt.Errorf("vcs %q is not a provider magus drives", d.VCSName)
	}
	if d.KeyVersion < 0 || d.TimestampMs < 0 || d.DurationMs < 0 {
		return errors.New("key_version, timestamp_ms and duration_ms must not be negative")
	}
	if d.Project == "" {
		return errors.New("project is empty")
	}
	names := []struct{ name, value string }{
		{"project", d.Project}, {"target", d.Target}, {"spell", d.Spell}, {"magus_version", d.MagusVersion},
	}
	for _, a := range d.ExtraArgs {
		names = append(names, struct{ name, value string }{"extra_args", a})
	}
	for _, n := range names {
		if err := checkName(n.value); err != nil {
			return fmt.Errorf("%s: %w", n.name, err)
		}
	}
	for _, c := range classes {
		if !classLabelPattern.MatchString(c.Class) || !classDigestPattern.MatchString(c.Digest) || c.Count < 0 {
			return fmt.Errorf("class digest %q=%q is not a class label and a %d-hex digest", c.Class, c.Digest, classDigestHexLen)
		}
	}
	return nil
}

func checkName(s string) error {
	if len(s) > maxFieldBytes {
		return fmt.Errorf("longer than %d bytes", maxFieldBytes)
	}
	if !utf8.ValidString(s) {
		return errors.New("not valid UTF-8")
	}
	for _, r := range s {
		if isControl(r) {
			return fmt.Errorf("contains control character U+%04X", r)
		}
	}
	if _, changed := changeset.SanitizeBidi(s); changed {
		return errors.New("contains a bidirectional or invisible character")
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) }

// NeutralizeTerminal returns b safe to write to a terminal and the number of controls it
// escaped: newline and tab pass, CR becomes LF, and every other control, escape sequence,
// bidirectional or invisible character and invalid byte is written as visible text
// (<U+001B>, <0xFF>). With keepSGR, a foreground-color or bold/italic/underline sequence
// passes intact.
func NeutralizeTerminal(b []byte, keepSGR bool) ([]byte, int) {
	out := make([]byte, 0, len(b))
	n := 0
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == '\n' || c == '\t':
			out = append(out, c)
			i++
			continue
		case c == '\r':
			// A bare CR would let later text overwrite a line already shown.
			if i+1 >= len(b) || b[i+1] != '\n' {
				out = append(out, '\n')
			}
			i++
			continue
		case c == 0x1b && keepSGR:
			if end := sgrEnd(b[i:]); end > 0 {
				out = append(out, b[i:i+end]...)
				i += end
				continue
			}
		}
		r, size := utf8.DecodeRune(b[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			out = fmt.Appendf(out, "<0x%02X>", c)
			n++
		case isControl(r):
			// Escaping the ESC leaves the rest of its sequence as inert text, and shows
			// the reader where it was.
			out = fmt.Appendf(out, "<U+%04X>", r)
			n++
		case r >= 0x200B:
			s, changed := changeset.SanitizeBidi(string(r))
			out = append(out, s...)
			if changed {
				n++
			}
		default:
			out = append(out, b[i:i+size]...)
		}
		i += size
	}
	return out, n
}

func sgrEnd(b []byte) int {
	if len(b) < 3 || b[1] != '[' {
		return 0
	}
	j := 2
	for j < len(b) && (b[j] >= '0' && b[j] <= '9' || b[j] == ';') {
		j++
	}
	if j >= len(b) || b[j] != 'm' {
		return 0
	}
	for _, p := range bytes.Split(b[2:j], []byte{';'}) {
		if !allowedSGR(string(p)) {
			return 0
		}
	}
	return j + 1
}

// allowedSGR keeps the colors CI logs mark failures with and drops every parameter that
// can make text match the background: black, background, reverse, conceal, 256/RGB.
func allowedSGR(p string) bool {
	switch p {
	case "", "0", "1", "3", "4", "22", "23", "24", "39":
		return true
	}
	v, err := strconv.Atoi(p)
	return err == nil && (v >= 31 && v <= 37 || v >= 91 && v <= 97)
}
