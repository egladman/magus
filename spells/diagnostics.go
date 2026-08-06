package spells

import (
	"regexp"
	"strconv"
	"strings"
)

// DiagnosticFormat names the convention a tool prints its findings in, so magus reads
// them as records (file, line, severity, message) instead of scraping prose.
//
// A CONVENTION, never a per-tool shape: a registry of per-tool patterns rots, so magus
// implements documented standards once and tools opt in. The flag that produces it
// (`-f gnu`, `--format=gcc`) belongs in the op's own args - magus never rewrites argv,
// which would collide with charms and contradict what `magus describe` prints.
type DiagnosticFormat string

const (
	// DiagnosticNone is the zero value: prose as far as magus is concerned. Default
	// because a mis-parsed line claims a file and a line that do not exist.
	DiagnosticNone DiagnosticFormat = ""
	// DiagnosticGNU is the GNU Coding Standards format,
	// `[program:]file:line[:column]: severity: message`. hadolint spells it `-f gnu`
	// and shellcheck `--format=gcc`; gcc, clang and ruff emit the same skeleton.
	DiagnosticGNU DiagnosticFormat = "gnu"
)

// Diagnostic is one finding read out of a tool's output. Line and Col are 1-based;
// zero means the tool did not say, so a file-level finding needs no sentinel.
type Diagnostic struct {
	File     string
	Line     int
	Col      int
	Severity string
	Code     string
	Message  string
}

// gnuLine matches the GNU Coding Standards diagnostic skeleton, with the column
// optional. One pattern covers tools that look different at a glance: hadolint writes
// `hadolint:Dockerfile:1: DL3006 warning: msg` and shellcheck
// `bad.sh:2:4: note: msg [SC2086]`.
var gnuLine = regexp.MustCompile(`^(.+?):(\d+)(?::(\d+))?:\s+(.*)$`)

// gnuProgram strips the optional leading program name GNU allows ("hadolint:Dockerfile").
// Two characters minimum, which is what keeps a Windows drive letter ("C:\src\x.go")
// out of it - the file group cannot tell the two apart, but a drive is always one char.
var gnuProgram = regexp.MustCompile(`^[A-Za-z0-9_.\-]{2,}:(.+)$`)

// gnuTail splits the part after the location into severity, code and message. A code
// appears either before the severity (hadolint's "DL3006 warning:") or trailing in
// brackets (shellcheck's "[SC2086]"), so both are lifted rather than left in the text.
var (
	gnuLead  = regexp.MustCompile(`^(?:([A-Z][A-Za-z0-9]*\d+)\s+)?(error|warning|note|info|style|fatal)s?:\s*(.*)$`)
	gnuTrail = regexp.MustCompile(`\s*\[([A-Z][A-Za-z0-9]*\d+)\]\s*$`)
)

// ParseDiagnostics reads text as format f, returning one Diagnostic per recognized
// line and skipping the rest. An unknown or absent format yields nothing, so a caller
// treats "no format declared" and "nothing recognized" the same way: fall back to
// showing the output as written.
func ParseDiagnostics(f DiagnosticFormat, text string) []Diagnostic {
	if f != DiagnosticGNU {
		return nil
	}
	var out []Diagnostic
	for _, line := range strings.Split(text, "\n") {
		m := gnuLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		d := Diagnostic{File: m[1], Message: m[4]}
		if p := gnuProgram.FindStringSubmatch(d.File); p != nil {
			d.File = p[1]
		}
		d.Line, _ = strconv.Atoi(m[2])
		if m[3] != "" {
			d.Col, _ = strconv.Atoi(m[3])
		}
		if lead := gnuLead.FindStringSubmatch(d.Message); lead != nil {
			d.Code, d.Severity, d.Message = lead[1], lead[2], lead[3]
		}
		if trail := gnuTrail.FindStringSubmatch(d.Message); trail != nil && d.Code == "" {
			d.Code = trail[1]
			d.Message = strings.TrimSpace(gnuTrail.ReplaceAllString(d.Message, ""))
		}
		out = append(out, d)
	}
	return out
}
