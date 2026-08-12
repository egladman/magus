package tty

import (
	"encoding/base64"
	"fmt"
	"io"
)

// clipboardLimit is the most we will put on the wire in one OSC 52.
//
// Terminals cap the sequence and differ about where, and one that is over its
// limit typically drops the WHOLE thing rather than truncating - so a copy that
// silently did nothing would be worse than one that says it took the tail. 64 KiB
// is comfortably under every limit worth caring about and is far more captured
// output than anybody pastes.
const clipboardLimit = 64 << 10

// Copy puts text on the SYSTEM clipboard using OSC 52, reporting whether it was
// attempted and how much was sent.
//
// This exists because a two-column band cannot be drag-selected cleanly and no
// arrangement of it can be: terminals select LINEARLY, so a drag down one column
// takes the other column, the divider and the frame with it. Rectangular select
// exists but is modifier-gated, absent from several terminals, and something a
// reader has to already know.
//
// So magus does not ask anyone to select. A keypress puts the exact text on the
// clipboard, with no frame, no padding, no divider and no escape sequences - and
// OSC 52 travels through ssh and tmux, which is where a build actually runs.
//
// Only ever on an explicit keystroke. A tool that wrote the clipboard on its own
// would clobber whatever the reader had copied, which is hostile.
func Copy(w io.Writer, text string) (sent int, err error) {
	if text == "" {
		return 0, nil
	}
	// The TAIL is kept when over the limit, for the same reason the preview
	// shows the tail: a failure says why at the end.
	if len(text) > clipboardLimit {
		text = text[len(text)-clipboardLimit:]
	}
	_, err = fmt.Fprintf(w, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
	if err != nil {
		return 0, err
	}
	return len(text), nil
}
