package interactive

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// OpenBrowser launches a browser for a URL and does not wait: the browser owns the
// tab from there. It honors the freedesktop/de-facto BROWSER convention first, so a
// user can force a specific browser on any platform (e.g.
// `BROWSER=firefox magus query out1a2b3c --open`); only when BROWSER is unset or every
// entry fails does it fall back to the OS default handler (macOS `open`, Windows
// FileProtocolHandler, else `xdg-open`, which itself already respects BROWSER and the
// desktop's default-web-browser setting on Linux).
func OpenBrowser(raw string) error {
	target, err := safeBrowserURL(raw)
	if err != nil {
		return err
	}
	if err := openViaBrowserEnv(target); err == nil {
		return nil
	}
	var cmd *exec.Cmd
	// The opener binary is fixed; the only variable is the URL, and safeBrowserURL
	// above has already required an http(s) scheme and a host and re-serialized it.
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

// safeBrowserURL parses raw and returns it only if it is an http(s) URL, re-serialized
// from the parsed form.
//
// The base of every URL magus opens is overridable (`--url` for the explorer,
// MAGUS_LOG_VIEWER_URL for the log viewer), so what reaches the opener is not a
// constant, and it is handed to a program as an argument. Two things follow. A string
// beginning with "-" would be read by the opener as a FLAG rather than a URL, which the
// scheme check rules out. And a scheme like file: or a shell-ish string is not something
// magus should hand a launcher on the strength of an environment variable.
//
// Re-serializing rather than returning raw is the part that makes this a sanitizer and
// not just a check: what gets executed is what was parsed and validated, so no unparsed
// tail can ride along.
func safeBrowserURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("not a URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("refusing to open %q: only http and https URLs are opened", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("refusing to open %q: no host", raw)
	}
	return u.String(), nil
}

// errNoBrowserEnvEntry means no $BROWSER entry launched (or BROWSER was unset), so the
// caller falls back to the platform opener. The specific entries tried are not worth
// carrying: openBrowser only branches on nil-vs-non-nil, so a richer error would be
// built and immediately discarded.
var errNoBrowserEnvEntry = errors.New("no BROWSER entry launched")

// openViaBrowserEnv tries the $BROWSER convention: a colon-separated list of commands,
// each either containing "%s" (replaced by the URL) or taking the URL as a trailing
// argument. The first entry that launches wins. Returns errNoBrowserEnvEntry if BROWSER
// is unset or no entry starts, so the caller falls back to the platform opener.
func openViaBrowserEnv(url string) error {
	env := strings.TrimSpace(os.Getenv("BROWSER"))
	if env == "" {
		return errNoBrowserEnvEntry
	}
	for _, entry := range strings.Split(env, ":") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		var fields []string
		if strings.Contains(entry, "%s") {
			fields = strings.Fields(strings.ReplaceAll(entry, "%s", url))
		} else {
			fields = append(strings.Fields(entry), url)
		}
		if len(fields) == 0 {
			continue
		}
		cmd := exec.Command(fields[0], fields[1:]...)
		if err := cmd.Start(); err == nil {
			return nil
		}
	}
	return errNoBrowserEnvEntry
}
