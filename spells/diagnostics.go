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
	// DiagnosticCustom lets a spell supply its own extraction pattern via
	// Command.DiagnosticPattern, for a tool whose output fits no standard magus
	// implements. tsc is the motivating case: its parenthesized
	// `file(line,col): error TSxxxx: msg` has no flag that turns it into gnu.
	// Declared once by whoever writes the spell, the same place gnu is declared.
	// Decode rejects a bad regex, or one missing the required capture groups, at
	// decode time - the same place it rejects an unrecognized format.
	DiagnosticCustom DiagnosticFormat = "custom"
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

// gnuLine matches the GNU Coding Standards diagnostic skeleton, column optional.
// hadolint writes `hadolint:Dockerfile:1: DL3006 warning: msg`; shellcheck writes
// `bad.sh:2:4: note: msg [SC2086]`; buf omits the space after the location
// (`file.proto:3:1:msg`); Biome's `concise` reporter prefixes a severity glyph
// (`! bad.js:1:10: msg`, `× bad.js:3:7: msg`). The glyph tolerance is one shared rule,
// not a per-tool case - many linters decorate a line this way.
var gnuLine = regexp.MustCompile(`^(?:[^\w\s]\s+)?(.+?):(\d+)(?::(\d+))?:\s*(.*)$`)

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

// ParseDiagnostics reads output as format f, returning one Diagnostic per recognized
// line and skipping the rest. An unknown or absent format yields nothing, so "no
// format declared" and "nothing recognized" fall back to the same thing: show the
// output as written. pattern is read only when f is DiagnosticCustom; decode has
// already validated it compiles and names "file"/"line", so a compile failure here
// (a caller that bypassed decode) also yields nothing instead of panicking.
func ParseDiagnostics(f DiagnosticFormat, pattern string, output string) []Diagnostic {
	switch f {
	case DiagnosticGNU:
		return parseGNU(output)
	case DiagnosticCustom:
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil
		}
		return parseCustom(re, output)
	default:
		// Reachable only if a future DiagnosticFormat case is registered in
		// boundaryEnums without a matching case here - decode's Valid() check means no
		// spell can declare one today. Update this switch in lockstep with any new case.
		return nil
	}
}

func parseGNU(output string) []Diagnostic {
	var out []Diagnostic
	for _, line := range strings.Split(output, "\n") {
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

// LikelyMisconfigured reports whether a declared format is probably not matching the
// tool's real output: the run failed, a format was declared, the tool wrote something,
// and ParseDiagnostics extracted nothing. That combination is the only place a stale
// pattern (built-in or custom) is knowable at all - decode can't validate a pattern
// against output that doesn't exist yet.
//
// A clean run with zero diagnostics does not count; only failed-and-nothing-extracted
// does. The intended caller is the future diagnostics consumer (see
// internal/cache/cache.go's failureExcerpt/ann.Annotate path): log a slog.Warn naming
// the op on a hit, not a coded MGS diagnostic - this is a run-time signal, not a
// decode-time authoring bug.
func LikelyMisconfigured(f DiagnosticFormat, failed bool, output string, found []Diagnostic) bool {
	return failed && f != DiagnosticNone && strings.TrimSpace(output) != "" && len(found) == 0
}

// parseCustom reads output against an author-supplied pattern, using named capture
// groups instead of fixed submatch indexes: gnuLine can rely on its groups never
// moving, but a pattern magus didn't write has no such guarantee. Unnamed or unused
// groups are ignored.
func parseCustom(re *regexp.Regexp, output string) []Diagnostic {
	names := re.SubexpNames()
	var out []Diagnostic
	for _, line := range strings.Split(output, "\n") {
		m := re.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		var d Diagnostic
		for i, name := range names {
			if i == 0 {
				continue
			}
			switch name {
			case "file":
				d.File = m[i]
			case "line":
				d.Line, _ = strconv.Atoi(m[i])
			case "col":
				d.Col, _ = strconv.Atoi(m[i])
			case "severity":
				d.Severity = m[i]
			case "code":
				d.Code = m[i]
			case "message":
				d.Message = m[i]
			}
		}
		out = append(out, d)
	}
	return out
}
