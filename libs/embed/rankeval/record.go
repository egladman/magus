package rankeval

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// EvalRecord is one system's score on one set, the line evals.jsonl keeps per
// (System, TableDigest, Set, Metric). TableDigest names the shipped table the run
// was made beside, so a retrained table never overwrites an older row's key. The
// interval is the 95% one the metric's report prints.
type EvalRecord struct {
	System      string  `json:"system"`
	TableDigest string  `json:"table_digest"`
	Set         string  `json:"set"`
	Metric      string  `json:"metric"`
	Value       float64 `json:"value"`
	Low         float64 `json:"low"`
	High        float64 `json:"high"`
	Queries     int     `json:"queries"`
	Export      string  `json:"export"`
}

type evalKey struct{ system, table, set, metric string }

func (r EvalRecord) key() evalKey { return evalKey{r.System, r.TableDigest, r.Set, r.Metric} }

// MarshalJSON refuses a record that would not read back.
func (r EvalRecord) MarshalJSON() ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	type plain EvalRecord
	return marshalJSON(plain(r))
}

// UnmarshalJSON is strict: an unknown or repeated field is an error, as is any record
// MarshalJSON would refuse.
func (r *EvalRecord) UnmarshalJSON(b []byte) error {
	type plain EvalRecord
	return decodeStrict(b, r, func(p plain) EvalRecord { return EvalRecord(p) })
}

func (r EvalRecord) validate() error {
	switch {
	case r.System == "":
		return fmt.Errorf("eval record: empty system")
	case r.Set == "":
		return fmt.Errorf("eval record %s: empty set", r.System)
	case r.Metric == "":
		return fmt.Errorf("eval record %s on %s: empty metric", r.System, r.Set)
	case r.Queries <= 0:
		return fmt.Errorf("eval record %s on %s: %d queries", r.System, r.Set, r.Queries)
	}
	for _, x := range []float64{r.Value, r.Low, r.High} {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Errorf("eval record %s on %s: %v is not finite", r.System, r.Set, x)
		}
	}
	if r.Low > r.Value || r.Value > r.High {
		return fmt.Errorf("eval record %s on %s: value %v is outside [%v, %v]", r.System, r.Set, r.Value, r.Low, r.High)
	}
	return nil
}

// Cluster is one cluster's hits among its trials, such as one file's
// tokens with no vocabulary row among all its tokens.
type Cluster struct{ Trials, Hits int }

// ClusterRatio is the share of hits over all trials, with a 95% interval
// from the clusters' own spread. Trials within a cluster are not
// independent, so the interval counts each cluster as one draw.
func ClusterRatio(clusters []Cluster) (rate, low, high float64, trials int, err error) {
	var hits int
	for _, c := range clusters {
		trials += c.Trials
		hits += c.Hits
	}
	if trials == 0 {
		return 0, 0, 0, 0, errors.New("no trials in any cluster")
	}
	rate = float64(hits) / float64(trials)
	var ss float64
	for _, c := range clusters {
		d := float64(c.Hits) - rate*float64(c.Trials)
		ss += d * d
	}
	half := 0.0
	if n := float64(len(clusters)); n > 1 {
		half = 1.96 * math.Sqrt(ss*n/(n-1)) / float64(trials)
	}
	return rate, math.Max(0, rate-half), math.Min(1, rate+half), trials, nil
}

// ReadEvalRecords reads JSON Lines, one EvalRecord per non-blank line, and names the
// line of the first that does not decode. The last line per key wins; the rows come
// back in the order their keys first appeared.
func ReadEvalRecords(r io.Reader) ([]EvalRecord, error) {
	all, err := readLines[EvalRecord](r, "eval records")
	if err != nil {
		return nil, err
	}
	at := map[evalKey]int{}
	var out []EvalRecord
	for _, rec := range all {
		if i, ok := at[rec.key()]; ok {
			out[i] = rec
			continue
		}
		at[rec.key()] = len(out)
		out = append(out, rec)
	}
	return out, nil
}

// AppendEvalRecord appends rec as one line to the file at path, creating it.
func AppendEvalRecord(path string, rec EvalRecord) error {
	line, err := marshalJSON(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
