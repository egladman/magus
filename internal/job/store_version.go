package job

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
)

// storePathToken stands for the store file in a fix text written where the
// path is not known, such as a migration's row function. [Store.migrate]
// swaps it for the store's own path.
const storePathToken = "<store>"

// staleStoreFix is how a person gets past the job store at path, which an
// older magus wrote. A newer magus's lines are kept whole.
func staleStoreFix(path string) string {
	return fmt.Sprintf("move %s aside, or delete it, and fork the jobs again", path)
}

// pathNamed is err with its message naming the store file where it held
// [storePathToken]. It still unwraps to err.
type pathNamed struct {
	err error
	msg string
}

func (p pathNamed) Error() string { return p.msg }
func (p pathNamed) Unwrap() error { return p.err }

func namePath(err error, path string) error {
	msg := err.Error()
	if !strings.Contains(msg, storePathToken) {
		return err
	}
	return pathNamed{err: err, msg: strings.ReplaceAll(msg, storePathToken, path)}
}

// refuseRecord refuses the record at line of the store, got where this
// magus reads only want. Another version names staleStoreFix or a newer
// magus. Another kind is a line no magus writes.
func (s *Store) refuseRecord(want, got apiversion.TypeMeta, line int) error {
	what := fmt.Sprintf("job: %s:%d", s.path, line)
	if got.APIVersion != want.APIVersion {
		return types.RefuseVersion(types.APIVersion(want.APIVersion), types.APIVersion(got.APIVersion), what, staleStoreFix(s.path))
	}
	return types.DiagnosticErrorf(types.StoreLineUndecodable, "%s: %v", what, &apiversion.RefusedError{Want: want, Got: got})
}

// jobMigrations take a jobs.jsonl from each migration to the next, in order; see
// [Store.migrate]. A migration rewrites stored rows once, so a change to a stored member's
// name lands here and no reader keeps the old name.
var jobMigrations = []migrate.Migration{
	{Name: "model, criteria and fresh_reason move under agent", Up: agentSection},
	{Name: "worker type resume becomes resumed", Up: workerResumed},
	{Name: triesMoved, Up: dropTries},
	{Name: "model tiers economy, standard and principal become routine, bounded and design", Up: fourModelTiers},
	{Name: recordsEnveloped, Up: envelopeRow, Header: envelopeHeader},
	{Name: "model tier source Row becomes Job", Up: modelTierSourceJob},
	{Name: "write, read, deny and soft paths move under spec.paths", Up: pathsNested},
}

// specPathsMoved pairs each flat spec member with its name under spec.paths.
var specPathsMoved = []struct{ flat, nested string }{
	{"writePaths", "write"}, {"readPaths", "read"}, {"denyPaths", "deny"}, {"softPaths", "soft"},
}

// pathsNested moves a row's flat spec path members under spec.paths.
func pathsNested(e migrate.Entry) (migrate.Entry, error) {
	raw, ok := e["spec"]
	if !ok {
		return e, nil
	}
	var spec map[string]json.RawMessage
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("reading spec: %w", err)
	}
	paths := map[string]json.RawMessage{}
	for _, m := range specPathsMoved {
		if v, ok := spec[m.flat]; ok {
			paths[m.nested] = v
			delete(spec, m.flat)
		}
	}
	if len(paths) == 0 {
		return e, nil
	}
	var err error
	if spec["paths"], err = json.Marshal(paths); err == nil {
		e["spec"], err = json.Marshal(spec)
	}
	if err != nil {
		return nil, fmt.Errorf("writing spec.paths: %w", err)
	}
	return e, nil
}

// modelTierSourceJob respells the stored model tier source "Row", the
// wire spelling of the source before the domain dropped the word row.
func modelTierSourceJob(e migrate.Entry) (migrate.Entry, error) {
	raw, ok := e["status"]
	if !ok {
		return e, nil
	}
	var status map[string]json.RawMessage
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, fmt.Errorf("reading status: %w", err)
	}
	var record map[string]json.RawMessage
	if body, ok := status["modelTier"]; ok {
		if err := json.Unmarshal(body, &record); err != nil {
			return nil, fmt.Errorf("reading status.modelTier: %w", err)
		}
	}
	var source string
	if body, ok := record["source"]; ok {
		if err := json.Unmarshal(body, &source); err != nil {
			return nil, fmt.Errorf("reading status.modelTier.source: %w", err)
		}
	}
	// A flat entry enveloped since the rename keeps the domain value "row":
	// the wire table no longer lists it.
	if source != "Row" && source != "row" {
		return e, nil
	}
	var err error
	if record["source"], err = json.Marshal(wireTierSource.wire[tier.SourceJob]); err == nil {
		if status["modelTier"], err = json.Marshal(record); err == nil {
			e["status"], err = json.Marshal(status)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("writing status.modelTier.source: %w", err)
	}
	return e, nil
}

// recordsEnveloped names the migration [Store.migrate] rewrites
// attempts.jsonl before, with [Store.envelopeAttempts].
const recordsEnveloped = "rows and attempts take the batch.magus/v1alpha1 envelope; holder goes"

// headerEntry reports the header line, the one that is neither a row
// before [recordsEnveloped] nor one after.
func headerEntry(e migrate.Entry) bool {
	_, id := e["id"]
	_, spec := e["spec"]
	return !id && !spec
}

// envelopeRow rewrites a flat row as its [jobRecord]. A server's row is
// told by its registration once holder goes. A member job.Job does not
// declare is kept on the record as it was. The flat row spelled its paths
// before they nested, so flat reads them.
func envelopeRow(e migrate.Entry) (migrate.Entry, error) {
	text, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	var row job.Job
	var flat struct {
		Holder     string           `json:"holder"`
		APIVersion types.APIVersion `json:"api_version"`
		WritePaths []string         `json:"write_paths"`
		ReadPaths  []string         `json:"read_paths"`
		DenyPaths  []string         `json:"deny_paths"`
		SoftPaths  []string         `json:"soft_paths"`
	}
	if err := json.Unmarshal(text, &row); err != nil {
		return nil, fmt.Errorf("reading the row: %w", err)
	}
	if err := json.Unmarshal(text, &flat); err != nil {
		return nil, fmt.Errorf("reading holder: %w", err)
	}
	if _, old := e["schema_version"]; old || (flat.APIVersion != "" && flat.APIVersion.Compare(job.APIVersion) < 0) {
		return nil, types.RefuseVersion(job.APIVersion, flat.APIVersion, "job: row "+row.ID, staleStoreFix(storePathToken))
	}
	holder := flat
	if holder.Holder == "server" {
		row.RegisteredBy.EntryPoint = job.EntryPointServer
	}
	row.Paths = job.Paths{Write: flat.WritePaths, Read: flat.ReadPaths, Deny: flat.DenyPaths, Soft: flat.SoftPaths}
	r := recordJob(row, nil)
	declared, read := jsonNames(reflect.TypeFor[job.Job]()), jsonNames(reflect.TypeOf(flat))
	for k, v := range e {
		if !declared[k] && !read[k] {
			if r.Unknown == nil {
				r.Unknown = map[string]json.RawMessage{}
			}
			r.Unknown[k] = v
		}
	}
	return remarshal(r)
}

// envelopeHeader rewrites the flat header as the JobList line, its
// requires list as the requires annotation.
func envelopeHeader(e migrate.Entry) (migrate.Entry, error) {
	var requires []string
	if raw, ok := e["requires"]; ok {
		if err := json.Unmarshal(raw, &requires); err != nil {
			return nil, fmt.Errorf("reading requires: %w", err)
		}
	}
	features := make([]apiversion.Feature, len(requires))
	for i, r := range requires {
		features[i] = apiversion.Feature(r)
	}
	delete(e, "api_version")
	delete(e, "requires")
	next, err := remarshal(jobsFile{TypeMeta: jobListType, Metadata: apiversion.Metadata{}.WithRequires(recordGroup, features)})
	if err != nil {
		return nil, err
	}
	for k, v := range e {
		next[k] = v
	}
	return next, nil
}

// remarshal is v as an entry.
func remarshal(v any) (migrate.Entry, error) {
	text, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var e migrate.Entry
	if err := json.Unmarshal(text, &e); err != nil {
		return nil, err
	}
	return e, nil
}

// jsonNames are the member names t's fields declare.
func jsonNames(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case name == "-":
		case name != "":
			out[name] = true
		case f.Anonymous:
			for k := range jsonNames(f.Type) {
				out[k] = true
			}
		default:
			out[f.Name] = true
		}
	}
	return out
}

// workerResumed respells a stored worker type "resume" as "resumed", the
// value Go and the Buzz enum now share.
func workerResumed(e migrate.Entry) (migrate.Entry, error) {
	raw, ok := e["agent"]
	if !ok {
		return e, nil
	}
	var section map[string]json.RawMessage
	if err := json.Unmarshal(raw, &section); err != nil {
		return nil, fmt.Errorf("reading the agent section: %w", err)
	}
	var worker map[string]json.RawMessage
	if body, ok := section["worker"]; ok {
		if err := json.Unmarshal(body, &worker); err != nil {
			return nil, fmt.Errorf("reading agent.worker: %w", err)
		}
	}
	var typ string
	if body, ok := worker["type"]; ok {
		if err := json.Unmarshal(body, &typ); err != nil {
			return nil, fmt.Errorf("reading agent.worker.type: %w", err)
		}
	}
	if typ != "resume" {
		return e, nil
	}
	var err error
	if worker["type"], err = json.Marshal(job.WorkerTypeResumed); err == nil {
		if section["worker"], err = json.Marshal(worker); err == nil {
			e["agent"], err = json.Marshal(section)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("writing agent.worker.type: %w", err)
	}
	return e, nil
}

// agentSection moves a row's flat agent members under agent. A model the
// stored tier resolved as a tier name becomes the requested tier, and any
// other model an id. A server's row takes no section: it ran no agent.
func agentSection(e migrate.Entry) (migrate.Entry, error) {
	var old struct {
		Criteria    string `json:"criteria"`
		Model       string `json:"model"`
		FreshReason string `json:"fresh_reason"`
		Holder      string `json:"holder"`
		ModelTier   *struct {
			Source string `json:"source"`
			Model  string `json:"model"`
		} `json:"model_tier"`
	}
	flat := map[string]json.RawMessage{}
	for _, key := range []string{"criteria", "model", "fresh_reason", "holder", "model_tier"} {
		if v, ok := e[key]; ok {
			flat[key] = v
		}
	}
	body, err := json.Marshal(flat)
	if err == nil {
		err = json.Unmarshal(body, &old)
	}
	if err != nil {
		return nil, fmt.Errorf("reading the flat agent members: %w", err)
	}
	delete(e, "criteria")
	delete(e, "model")
	delete(e, "fresh_reason")
	if old.Criteria == "" && old.Model == "" && old.FreshReason == "" || old.Holder == "server" {
		return e, nil
	}
	spec := job.Agent{Instructions: old.Criteria, Worker: job.Worker{Type: job.WorkerTypeResumed}}
	switch {
	case old.Model == "":
	// These entries predate ModelTierSourceJob and stored "row" for it.
	case old.ModelTier != nil && old.ModelTier.Source == "row" && old.ModelTier.Model == "":
		spec.Model.Tier = old.Model
	default:
		spec.Model.ID = old.Model
	}
	if old.FreshReason != "" {
		spec.Worker = job.Worker{Type: job.WorkerTypeNew, Reason: old.FreshReason}
	}
	if e["agent"], err = json.Marshal(spec); err != nil {
		return nil, fmt.Errorf("writing the agent section: %w", err)
	}
	return e, nil
}
