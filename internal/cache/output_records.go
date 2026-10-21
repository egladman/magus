package cache

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// ReadOutputRecords checks every JSON Lines record in r, then hands each to each; it
// writes nothing, and any bad line is an error naming it before each sees a record.
// Fields come back as sent, so a caller printing them uses [NeutralizeTerminal]; the
// exceptions are failed_need_target, which comes back neutralized, and failed_need_ref, which
// comes back empty unless it is a ref magus mints.
func ReadOutputRecords(r io.Reader, each func(types.StoredOutput) error) error {
	return readOutputRecords(r, DefaultImportLimits, each)
}

func readOutputRecords(r io.Reader, limits ImportLimits, each func(types.StoredOutput) error) error {
	var recs []types.StoredOutput
	err := ReadRecordLines(r, limits, func(line int, raw []byte) error {
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
	case rec.Version == 0:
		return types.StoredOutput{}, errors.New("not an output record: it has no schema_version, `magus query output <ref> -o jsonl` writes one per line")
	case rec.Version > types.StoredOutputSchemaVersion:
		return types.StoredOutput{}, fmt.Errorf("output record schema_version %d is newer than this magus reads (%d), read it with a newer magus", rec.Version, types.StoredOutputSchemaVersion)
	case len(rec.Unmet(nil)) > 0:
		return types.StoredOutput{}, fmt.Errorf("output record requires %v, which this magus does not implement", rec.Unmet(nil))
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
	err := CheckImportedDescriptor(OutputDescriptor{
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
