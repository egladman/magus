package rankeval

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// HoldoutRead is one read of a holdout topic set, as the ledger holds it: which set,
// the DigestQueries of its queries, the candidate scored, and when. A ReportOnly read
// came after the first and may inform nobody's choice.
type HoldoutRead struct {
	Set        EvalSet   `json:"eval_set"`
	Digest     string    `json:"eval_digest"`
	Candidate  string    `json:"candidate"`
	At         time.Time `json:"at"`
	ReportOnly bool      `json:"report_only,omitzero"`
}

// MarshalJSON refuses a read that would not read back.
func (r HoldoutRead) MarshalJSON() ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	type plain HoldoutRead
	return marshalJSON(plain(r))
}

// UnmarshalJSON is strict: an unknown or repeated field is an error, as is any read
// MarshalJSON would refuse.
func (r *HoldoutRead) UnmarshalJSON(b []byte) error {
	type plain HoldoutRead
	return decodeStrict(b, r, func(p plain) HoldoutRead { return HoldoutRead(p) })
}

func (r HoldoutRead) validate() error {
	switch {
	case r.Set != SetJudgedHoldout:
		return fmt.Errorf("holdout read of %q: only %s is a holdout", r.Set, SetJudgedHoldout)
	case r.Digest == "":
		return fmt.Errorf("holdout read of %s: empty eval_digest", r.Set)
	case r.Candidate == "":
		return fmt.Errorf("holdout read of %s: empty candidate", r.Set)
	case r.At.IsZero():
		return fmt.Errorf("holdout read of %s: no time", r.Set)
	}
	return nil
}

// errHoldoutRead is returned by RecordHoldoutRead when the topic set was read before.
var errHoldoutRead = errors.New("holdout already read")

// RecordHoldoutRead appends read to the JSON Lines ledger at path, creating it, and
// returns the reads before it. A holdout is read once (Dwork et al., "The reusable
// holdout", Science 2015): when the ledger already holds a read of read.Digest, a
// read that is not ReportOnly is refused with an error naming the first, and
// nothing is appended. A ReportOnly read is always appended. It is not safe for two
// processes recording at once.
func RecordHoldoutRead(path string, read HoldoutRead) ([]HoldoutRead, error) {
	line, err := marshalJSON(read)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	var prior []HoldoutRead
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		prior, err = readLines[HoldoutRead](f, path)
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, p := range prior {
		if p.Digest == read.Digest && !read.ReportOnly {
			return prior, fmt.Errorf("%w: %s %s was read for %s at %s, so a further read needs MAGUS_EVAL_REPORT_ONLY=1 and only reports",
				errHoldoutRead, p.Set, p.Digest, p.Candidate, p.At.Format(time.RFC3339))
		}
	}
	if err := appendLine(path, line); err != nil {
		return nil, err
	}
	return prior, nil
}

// appendLine copies internal/file.AppendLines, since a lib ships apart from the root
// module: one write, so a reader sees the line whole or not at all, then a sync.
func appendLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
