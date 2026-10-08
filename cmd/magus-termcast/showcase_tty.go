//go:build darwin || linux

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/egladman/magus/internal/proc/run"
)

// recordShowcase drives the session and writes the marked capture.
func recordShowcase(dir string) error {
	s, err := run.StartTTY(context.Background(), "bash", []string{"--noprofile", "--norc"}, dir, showCols, showRows)
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}

	at := 0
	var out []byte
	take := func() {
		b := s.Output()
		out = append(out, b[at:]...)
		out = append(out, frameMark...)
		at = len(b)
	}

	for _, st := range showcaseScript() {
		if err := s.Type(st.keys); err != nil {
			return fmt.Errorf("type %q: %w", st.keys, err)
		}
		time.Sleep(st.settle)
		if st.frame {
			take()
		}
	}
	if err := s.Close(); err != nil {
		return fmt.Errorf("session: %w", err)
	}
	out = append(out, s.Output()[at:]...)

	if err := checkNoise(string(out), true); err != nil {
		return err
	}
	return os.WriteFile(showCapture, out, 0o644)
}
