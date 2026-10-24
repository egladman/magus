package main

import (
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// printOutputRecords prints each record on in the way a local ref prints: the output on
// stdout, provenance on stderr. Every printed string is neutralized, and no reproduce
// command is printed, since a forged record chooses its own project and target.
func printOutputRecords(in io.Reader, stdout, stderr io.Writer) error {
	return cache.ReadOutputRecords(in, func(rec types.StoredOutput) error {
		writeRecordProvenance(stderr, rec)
		out, n := cache.NeutralizeTerminal(rec.Bytes(), true)
		if _, err := stdout.Write(out); err != nil {
			return err
		}
		if n > 0 {
			fmt.Fprintf(stderr, "%s: %d terminal control(s) in the output are shown as <U+XXXX> instead of acting on the terminal\n", rec.Ref, n)
		}
		return nil
	})
}

func writeRecordProvenance(w io.Writer, rec types.StoredOutput) {
	d := rec.OutputDescriptor
	fmt.Fprintf(w, "%s from stdin: not run here, not stored, never a cache hit\n", d.Ref)
	fmt.Fprintf(w, "  project:  %s\n", d.Project)
	if d.Target != "" {
		fmt.Fprintf(w, "  target:   %s\n", d.Target)
	}
	status := "pass"
	if d.Failed {
		status = "fail"
	}
	fmt.Fprintf(w, "  status:   %s (%s) at %s\n", status,
		(time.Duration(d.DurationMs) * time.Millisecond).Round(time.Millisecond),
		time.UnixMilli(d.TimestampMs).UTC().Format("2006-01-02 15:04:05 UTC"))
	if d.ErrMsg != "" {
		msg, _ := cache.NeutralizeTerminal([]byte(strings.TrimRight(d.ErrMsg, "\n")), false)
		fmt.Fprintf(w, "  error:    %s\n", strings.ReplaceAll(string(msg), "\n", "\n            "))
	}
	// A record is another machine's, so the member's ref is only named when it has the shape
	// of one, and the target is neutralized like the error. The command reads the member's
	// output wherever the record came from, not here.
	if cache.IsMintedRef(d.FailedNeedRef) {
		target, _ := cache.NeutralizeTerminal([]byte(d.FailedNeedTarget), false)
		fmt.Fprintf(w, "  failed:   ctx.needs %s; where the record was made, read it with %s\n", target, hint.QueryOutput.With(d.FailedNeedRef))
	}
	if d.Attempt != "" {
		fmt.Fprintf(w, "  attempt:  %s\n", d.Attempt)
	}
	here := runtime.GOOS + "/" + runtime.GOARCH
	platform := d.Platform
	if platform == "" {
		platform = "unknown"
	}
	fmt.Fprintf(w, "  platform: %s (this machine: %s)\n", platform, here)
	if d.Revision != "" {
		dirty := ""
		if d.Dirty {
			dirty = " (dirty)"
		}
		fmt.Fprintf(w, "  revision: %s%s\n", magus.ShortRevision(d.Revision), dirty)
	}
	if d.MagusVersion != "" {
		fmt.Fprintf(w, "  magus:    %s\n", d.MagusVersion)
	}
	if len(rec.ClassDigests) > 0 {
		parts := make([]string, 0, len(rec.ClassDigests))
		for _, c := range rec.ClassDigests {
			parts = append(parts, fmt.Sprintf("%s %s", c.Class, c.Digest))
		}
		fmt.Fprintf(w, "  key:      %s (compare with `magus describe target --cache` here)\n", strings.Join(parts, ", "))
	}
}
