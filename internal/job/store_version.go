package job

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// storePathToken stands for the store file in a fix text written where the
// path is not known, such as a migration's row function. [Store.migrate]
// swaps it for the store's own path.
const storePathToken = "<store>"

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

// jobMigrations take a jobs.jsonl from each migration to the next, in order; see
// [Store.migrate]. A migration rewrites stored rows once, so a change to a stored member's
// name lands here and no reader keeps the old name.
func jobMigrations() []migrate.Migration {
	return []migrate.Migration{
		{Name: "model, criteria and fresh_reason move under agent", Up: agentSection},
		{Name: "worker type resume becomes resumed", Up: workerResumed},
		{Name: triesMoved, Up: dropTries},
		{Name: "model tiers economy, standard and principal become routine, bounded and design", Up: fourModelTiers},
		{Name: recordsEnveloped, Up: envelopeRow, Header: envelopeHeader},
		{Name: "model tier source Row becomes Job", Up: modelTierSourceJob},
		{Name: "write, read, deny and soft paths move under spec.paths", Up: pathsNested},
	}
}

// specPathsMoved pairs each flat spec member with its name under spec.paths.
func specPathsMoved() []struct{ flat, nested string } {
	return []struct{ flat, nested string }{
		{"writePaths", "write"}, {"readPaths", "read"}, {"denyPaths", "deny"}, {"softPaths", "soft"},
	}
}

// pathsNested moves a row's flat spec path members under spec.paths.
func pathsNested(e migrate.Entry) (migrate.Entry, error) {
	raw, ok := e["spec"]
	if !ok {
		return e, nil
	}
	var spec map[string]codec.RawMessage
	if err := codec.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("reading spec: %w", err)
	}
	paths := map[string]codec.RawMessage{}
	for _, m := range specPathsMoved() {
		if v, ok := spec[m.flat]; ok {
			paths[m.nested] = v
			delete(spec, m.flat)
		}
	}
	if len(paths) == 0 {
		return e, nil
	}
	var err error
	if spec["paths"], err = codec.Marshal(paths); err == nil {
		e["spec"], err = codec.Marshal(spec)
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
	var status map[string]codec.RawMessage
	if err := codec.Unmarshal(raw, &status); err != nil {
		return nil, fmt.Errorf("reading status: %w", err)
	}
	var record map[string]codec.RawMessage
	if body, ok := status["modelTier"]; ok {
		if err := codec.Unmarshal(body, &record); err != nil {
			return nil, fmt.Errorf("reading status.modelTier: %w", err)
		}
	}
	var source string
	if body, ok := record["source"]; ok {
		if err := codec.Unmarshal(body, &source); err != nil {
			return nil, fmt.Errorf("reading status.modelTier.source: %w", err)
		}
	}
	// A flat entry enveloped since the rename keeps the domain value "row":
	// the wire table no longer lists it.
	if source != "Row" && source != "row" {
		return e, nil
	}
	var err error
	// The wire spelling of the source when this migration was written.
	if record["source"], err = codec.Marshal("Job"); err == nil {
		if status["modelTier"], err = codec.Marshal(record); err == nil {
			e["status"], err = codec.Marshal(status)
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

// envelopeRow rewrites a flat row as its record ([job.MarshalRecord]). A server's row is
// told by its registration once holder goes. A member job.Job does not
// declare is kept on the record as it was. The flat row spelled its paths
// before they nested, so flat reads them.
func envelopeRow(e migrate.Entry) (migrate.Entry, error) {
	text, err := codec.Marshal(e)
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
	if err := codec.Unmarshal(text, &row); err != nil {
		return nil, fmt.Errorf("reading the row: %w", err)
	}
	if err := codec.Unmarshal(text, &flat); err != nil {
		return nil, fmt.Errorf("reading holder: %w", err)
	}
	if _, old := e["schema_version"]; old || (flat.APIVersion != "" && flat.APIVersion.Compare(job.APIVersion) < 0) {
		return nil, types.RefuseVersion(job.APIVersion, flat.APIVersion, "job: row "+row.ID, job.StaleStoreFix(storePathToken))
	}
	holder := flat
	if holder.Holder == "server" {
		row.RegisteredBy.EntryPoint = job.EntryPointServer
	}
	row.Paths = job.Paths{Write: flat.WritePaths, Read: flat.ReadPaths, Deny: flat.DenyPaths, Soft: flat.SoftPaths}
	var unknown map[string]codec.RawMessage
	declared, read := jsonNames(reflect.TypeFor[job.Job]()), jsonNames(reflect.TypeOf(flat))
	for k, v := range e {
		if !declared[k] && !read[k] {
			if unknown == nil {
				unknown = map[string]codec.RawMessage{}
			}
			unknown[k] = v
		}
	}
	line, err := job.MarshalRecord(row, unknown, credentialKinds()...)
	if err != nil {
		return nil, err
	}
	return entryOf(line)
}

// envelopeHeader rewrites the flat header as the JobList line, its
// requires list as the requires annotation.
func envelopeHeader(e migrate.Entry) (migrate.Entry, error) {
	var requires []string
	if raw, ok := e["requires"]; ok {
		if err := codec.Unmarshal(raw, &requires); err != nil {
			return nil, fmt.Errorf("reading requires: %w", err)
		}
	}
	features := make([]apiversion.Feature, len(requires))
	for i, r := range requires {
		features[i] = apiversion.Feature(r)
	}
	delete(e, "api_version")
	delete(e, "requires")
	line, err := job.MarshalHeader(features)
	if err != nil {
		return nil, err
	}
	next, err := entryOf(line)
	if err != nil {
		return nil, err
	}
	for k, v := range e {
		next[k] = v
	}
	return next, nil
}

// entryOf is the line text as an entry.
func entryOf(text []byte) (migrate.Entry, error) {
	var e migrate.Entry
	if err := codec.Unmarshal(text, &e); err != nil {
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
	var section map[string]codec.RawMessage
	if err := codec.Unmarshal(raw, &section); err != nil {
		return nil, fmt.Errorf("reading the agent section: %w", err)
	}
	var worker map[string]codec.RawMessage
	if body, ok := section["worker"]; ok {
		if err := codec.Unmarshal(body, &worker); err != nil {
			return nil, fmt.Errorf("reading agent.worker: %w", err)
		}
	}
	var typ string
	if body, ok := worker["type"]; ok {
		if err := codec.Unmarshal(body, &typ); err != nil {
			return nil, fmt.Errorf("reading agent.worker.type: %w", err)
		}
	}
	if typ != "resume" {
		return e, nil
	}
	var err error
	if worker["type"], err = codec.Marshal(job.WorkerTypeResumed); err == nil {
		if section["worker"], err = codec.Marshal(worker); err == nil {
			e["agent"], err = codec.Marshal(section)
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
	flat := map[string]codec.RawMessage{}
	for _, key := range []string{"criteria", "model", "fresh_reason", "holder", "model_tier"} {
		if v, ok := e[key]; ok {
			flat[key] = v
		}
	}
	body, err := codec.Marshal(flat)
	if err == nil {
		err = codec.Unmarshal(body, &old)
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
	if e["agent"], err = codec.Marshal(spec); err != nil {
		return nil, fmt.Errorf("writing the agent section: %w", err)
	}
	return e, nil
}
