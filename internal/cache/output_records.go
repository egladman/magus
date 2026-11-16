package cache

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// ReadOutputRecords checks every JSON Lines record in r, then hands each to each; it
// writes nothing, and any bad line is an error naming it before each sees a record.
// Fields come back as sent, so a caller printing them uses [NeutralizeTerminal]; the
// exceptions are failed_need_target, which comes back neutralized, and failed_need_ref, which
// comes back empty unless it is a ref magus mints.
func ReadOutputRecords(r io.Reader, each func(types.StoredOutput) error) error {
	return readOutputRecords(r, defaultImportLimits, each)
}

func readOutputRecords(r io.Reader, limits importLimits, each func(types.StoredOutput) error) error {
	var recs []types.StoredOutput
	err := readRecordLines(r, limits, func(line int, raw []byte) error {
		rec, err := decodeOutputRecord(raw)
		if err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		recs = append(recs, rec)
		return nil
	})
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if err := each(rec); err != nil {
			return err
		}
	}
	return nil
}

// lineBreaks spells out the two controls NeutralizeTerminal lets through.
var lineBreaks = strings.NewReplacer("\n", "<U+000A>", "\t", "<U+0009>")

func decodeOutputRecord(raw []byte) (types.StoredOutput, error) {
	var rec types.StoredOutput
	if err := json.Unmarshal(raw, &rec); err != nil {
		return types.StoredOutput{}, fmt.Errorf("not an output record: %w", err)
	}
	switch {
	case rec.Version == "":
		return types.StoredOutput{}, errors.New("not an output record: it has no api_version, `magus query output <ref> -o jsonl` writes one per line")
	case rec.Version != types.StoredOutputAPIVersion:
		return types.StoredOutput{}, types.DiagnosticErrorf(types.StoreRecordsNewer, "%v",
			types.RefuseVersion(types.StoredOutputAPIVersion, rec.Version, "output record", "write it again with this magus"))
	case len(rec.Unmet(nil)) > 0:
		return types.StoredOutput{}, types.DiagnosticErrorf(types.StoreRecordsNewer, "output record requires %v, which this magus does not implement", rec.Unmet(nil))
	case rec.Output != "" && rec.OutputBase64 != nil:
		return types.StoredOutput{}, errors.New("output record carries both output and output_base64")
	}
	// The member a failure points at is advice, not identity: a forged one is made harmless
	// rather than refusing the record, whose captured output is still worth reading. It is
	// shown on one line, so a line break is escaped along with the other controls.
	need, _ := NeutralizeTerminal([]byte(rec.FailedNeedTarget), false)
	rec.FailedNeedTarget = lineBreaks.Replace(string(need))
	if !IsMintedRef(rec.FailedNeedRef) {
		rec.FailedNeedRef = ""
	}
	d := rec.OutputDescriptor
	err := checkImportedDescriptor(OutputDescriptor{
		Ref: d.Ref, Project: d.Project, Target: d.Target, Inv: d.Inv,
		TimestampMs: d.TimestampMs, DurationMs: d.DurationMs,
		FailedNeedTarget: d.FailedNeedTarget, FailedNeedRef: d.FailedNeedRef,
		Key: d.Key, KeyVersion: d.KeyVersion, Attempt: d.Attempt, MagusVersion: d.MagusVersion,
		Revision: d.Revision, Spell: d.Spell, ExtraArgs: d.ExtraArgs, VCSName: d.VCSName, Platform: d.Platform,
	}, rec.ClassDigests)
	if err != nil {
		return types.StoredOutput{}, err
	}
	return rec, nil
}

// PrintOutputRecords prints each record on in the way a local ref prints: the output on
// stdout, provenance on stderr. Every printed string is neutralized, and no reproduce
// command is printed, since a forged record chooses its own project and target.
func PrintOutputRecords(in io.Reader, stdout, stderr io.Writer) error {
	return ReadOutputRecords(in, func(rec types.StoredOutput) error {
		writeRecordProvenance(stderr, rec)
		out, n := NeutralizeTerminal(rec.Bytes(), true)
		if _, err := stdout.Write(out); err != nil {
			return err
		}
		if n > 0 {
			fmt.Fprintf(stderr, "%s: %d terminal control(s) in the output are shown as <U+XXXX> instead of acting on the terminal\n", rec.Ref, n)
		}
		return nil
	})
}

// shortRevisionLen is how much of a revision id a provenance line shows.
const shortRevisionLen = 12

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
		msg, _ := NeutralizeTerminal([]byte(strings.TrimRight(d.ErrMsg, "\n")), false)
		fmt.Fprintf(w, "  error:    %s\n", strings.ReplaceAll(string(msg), "\n", "\n            "))
	}
	// A record is another machine's, so the member's ref is only named when it has the shape
	// of one, and the target is neutralized like the error. The command reads the member's
	// output wherever the record came from, not here.
	if IsMintedRef(d.FailedNeedRef) {
		target, _ := NeutralizeTerminal([]byte(d.FailedNeedTarget), false)
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
		fmt.Fprintf(w, "  revision: %s%s\n", shortRevision(d.Revision), dirty)
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
