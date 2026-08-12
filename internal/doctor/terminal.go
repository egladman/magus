package doctor

import (
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
)

// minInteractiveHeight is the shortest terminal the interactive surfaces still
// work in: the reserved band, the rule the zone draws above it, and a scrolling
// area worth reading above that.
//
// Named here rather than reaching into tty because it is a REPORTING threshold -
// what to warn a reader about - and the zone enforces its own limit regardless.
const minInteractiveHeight = 15

// checkTerminal reports what the terminal in front of magus can actually do.
//
// It PROBES rather than infers wherever it can. Every other question here is
// answered from an environment variable or a descriptor, but whether a click
// can be resolved against an inline view depends on the terminal answering a
// cursor-position query - and terminals differ, silently. Asking is the only
// honest answer, and it is the one capability a reader cannot look up.
//
// Nothing here is a Fail. A terminal that cannot do these things is not broken
// and neither is the workspace: magus degrades to plain output on every one of
// them. What a reader needs is to know WHICH way it degraded before they wonder
// why the colours or the mouse are missing.
func (r *runner) checkTerminal() types.DoctorCheck {
	const name = "terminal capabilities"

	// The FORMAT decides this before the terminal gets a say. pretty and plain
	// install the handler that owns the pinned band; text and json install a
	// structured slog handler instead, so a perfectly capable terminal shows no
	// band at all. Reported first because it is the answer to "why is there no
	// status line" more often than anything about the terminal is.
	format := r.opts.cfg.Log.Format
	if format == "" {
		format = "pretty"
	}
	if format != "pretty" && format != "plain" {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorOK,
			Message: fmt.Sprintf("log format is %q, so magus emits structured records and draws no interactive surface", format),
			Details: []string{formatSource(format), "set log.format to pretty for the pinned band, live status and notifications"},
		}
	}

	// Both ends, because the interactive surfaces need to paint AND to listen.
	// A pipe on either is the ordinary CI case and not worth a warning.
	canRender := tty.CanRender(os.Stderr, tty.SystemProbe)
	canRead := tty.IsTerminalReader(os.Stdin, tty.SystemProbe)
	if !canRender || !canRead {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorOK,
			Message: "not an interactive terminal; magus renders plain output",
			Details: terminalDetails(canRender, canRead, false),
		}
	}

	width, height := 0, 0
	if fd, ok := tty.Fd(os.Stderr); ok {
		width, height, _ = tty.SystemProbe.Size(fd)
	}

	// The one real probe. It costs a round trip and briefly takes the terminal
	// raw, which is why it runs only once both ends are known to be terminals.
	mouse := probeCursorReport()

	details := terminalDetails(canRender, canRead, mouse)
	details = append(details,
		formatSource(format),
		fmt.Sprintf("size: %dx%d", width, height),
		fmt.Sprintf("color: %s", yesNo(tty.WantsColor(os.Stderr, tty.SystemProbe))),
		fmt.Sprintf("hyperlinks: %s", yesNo(tty.WantsHyperlinks(os.Stderr, tty.SystemProbe))),
	)

	var degraded []string
	// Quiet admits only errors, and the live pool sample that drives the status
	// row is emitted at info - so the band still pins failures and never shows
	// progress. Worth saying, because the row simply not appearing looks like a
	// terminal problem and is not one.
	if r.opts.cfg.Log.Silent != nil && *r.opts.cfg.Log.Silent {
		degraded = append(degraded, "quiet or silent is set, so the live status row is suppressed; failures still pin")
	}
	if !mouse {
		degraded = append(degraded, "no mouse (this terminal does not report the cursor position, so a click cannot be resolved against an inline view)")
	}
	if !tty.WantsColor(os.Stderr, tty.SystemProbe) {
		degraded = append(degraded, "no color")
	}
	// magus draws the band's border in box-drawing runes on every terminal,
	// rather than picking per locale, so that one look is the same everywhere
	// and a committed picture does not depend on the shell that made it. The
	// cost is that this is the one setting that can make it render wrong, so it
	// is reported instead.
	if !utf8Locale() {
		degraded = append(degraded, "locale is not UTF-8 ("+localeSource()+"), so the band's border may render as stray characters; set LANG to a UTF-8 value")
	}
	if height > 0 && height < minInteractiveHeight {
		degraded = append(degraded, fmt.Sprintf("only %d rows; the pinned band needs a scrolling area above it and stands down below about %d", height, minInteractiveHeight))
	}
	if len(degraded) == 0 {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorOK,
			Message: "interactive terminal, fully capable",
			Details: details,
		}
	}
	return types.DoctorCheck{
		Name:    name,
		Status:  types.DoctorAdvice,
		Message: "interactive terminal with reduced capability; magus degrades rather than failing",
		Details: append(details, degraded...),
	}
}

// probeCursorReport asks the terminal where the cursor is and reports whether it
// answered.
//
// This is the capability that decides whether the mouse is usable on a view
// drawn wherever the cursor happened to be - a picker, a watch frame. A reserved
// band can be hit-tested from geometry magus chose; an inline one cannot, so the
// terminal has to be asked, and a terminal that does not implement the query
// simply says nothing.
func probeCursorReport() bool {
	in, err := tty.OpenInput(os.Stdin, os.Stderr, tty.SystemProbe)
	if err != nil {
		return false
	}
	defer func() { _ = in.Close() }()
	_, _, ok := in.CursorPosition()
	return ok
}

// terminalDetails lists the facts every branch above wants to report.
func terminalDetails(canRender, canRead, mouse bool) []string {
	return []string{
		fmt.Sprintf("TERM=%q", os.Getenv("TERM")),
		fmt.Sprintf("display is a terminal: %s", yesNo(canRender)),
		fmt.Sprintf("input is a terminal: %s", yesNo(canRead)),
		fmt.Sprintf("mouse: %s", yesNo(mouse)),
	}
}

// formatSource names the log format and where the value came from, because the
// remedy depends on it: an environment variable overrides magus.yaml, so a
// reader editing the file would change nothing.
func formatSource(format string) string {
	if env := os.Getenv("MAGUS_LOG_FORMAT"); env != "" {
		return fmt.Sprintf("log format: %q (from MAGUS_LOG_FORMAT, which overrides magus.yaml)", format)
	}
	return fmt.Sprintf("log format: %q (from config)", format)
}

// utf8Locale reports whether the environment claims a UTF-8 character set,
// checked in the order POSIX resolves them.
func utf8Locale() bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			u := strings.ToUpper(v)
			return strings.Contains(u, "UTF-8") || strings.Contains(u, "UTF8")
		}
	}
	return false
}

// localeSource names the variable that decided, so the remedy is unambiguous:
// setting LANG changes nothing when LC_ALL is what is in force.
func localeSource() string {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return k + "=" + v
		}
	}
	return "none of LC_ALL, LC_CTYPE or LANG is set"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
