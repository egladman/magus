package interactive

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/clikit/exit"
)

// FragmentWarnBytes is a conservative ceiling on the encoded fragment. The whole
// URL rides on the command line to the browser and into the address bar; Chrome
// handles multi-megabyte URLs, but Safari (~80 KB) and older Firefox (~64 KB)
// cap shorter. Past this we point at --serve, which has no size limit.
const FragmentWarnBytes = 48 * 1024

// OpenGraphFragment opens the graph explorer at base with encoded, the knowledge graph
// as render.EncodeFragmentRaw writes it, carried whole in the URL fragment, so the
// graph never leaves the machine. nodes and edges are only reported. With printOnly it
// prints the URL and opens nothing. A browser that fails to open is reported and
// returned as exit.Failure.
func OpenGraphFragment(ctx context.Context, base, encoded string, nodes, edges int, printOnly bool) error {
	openURL := fmt.Sprintf("%s/#data=%s", strings.TrimRight(base, "/"), encoded)

	if len(encoded) > FragmentWarnBytes {
		slog.WarnContext(ctx, fmt.Sprintf("this graph encodes to %d KB, near or past what Safari and older\n"+
			"Firefox accept in a URL (Chrome is fine). If the page does not load, re-run with\n"+
			"--serve to deliver it over a loopback server instead (no size limit). Continuing.", len(encoded)/1024),
			attr.Notice("magus graph export --open"))
	}

	if printOnly {
		fmt.Println(openURL)
		return nil
	}

	slog.InfoContext(ctx, fmt.Sprintf("opening the graph explorer for this workspace (%d nodes, %d edges).\n"+
		"your graph rides in the link fragment and is never uploaded - it does not leave your machine.", nodes, edges),
		attr.Notice(""))
	if err := OpenBrowser(openURL); err != nil {
		slog.ErrorContext(ctx, "could not open a browser", attr.Notice("magus graph export --open"), attr.Error(err),
			attr.Why("Re-run with --print to get the URL, or open it yourself."))
		return exit.Reported(exit.Failure)
	}
	return nil
}
