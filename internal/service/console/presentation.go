package console

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egladman/magus/internal/hint"
)

const DefaultApp = "dashboard"

// Presentation is a local console link an MCP client may show to its user.
type Presentation struct {
	URL    string `json:"url" yaml:"url"`
	App    string `json:"app" yaml:"app"`
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
	// OpenCommand is a shell line that opens URL signed in (see [OpenCommand]). URL alone
	// lands on the console's sign-in screen, because every console route needs a token.
	OpenCommand string `json:"open" yaml:"open"`
}

// Present validates a console app and builds its tokenless console link.
func Present(host, app, reason string) (Presentation, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		app = DefaultApp
	}
	if !IsAppRoute(app) {
		return Presentation{}, fmt.Errorf("console: unknown app %q", app)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return Presentation{}, fmt.Errorf("console: no local address")
	}
	link := Link(LinkOpts{Host: host, App: app})
	return Presentation{
		URL:    link,
		App:    app,
		Reason: strings.TrimSpace(reason),
		// The server answers this, so its own argv0 means nothing to the reader.
		OpenCommand: OpenCommandAs(link, runtime.GOOS, hint.DefaultBinaryName),
	}, nil
}
