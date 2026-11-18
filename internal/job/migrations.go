package job

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
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
		{Name: job.AcceptWitnessedMigration, Up: acceptWitnessed},
		{Name: groupsMoved, Up: regroup, Header: regroup},
		{Name: keysSnaked, Up: snakeRecord, Header: snakeRecord},
	}
}

// groupsMoved names the migration [Store.migrate] rewrites attempts.jsonl before, with
// [Store.regroupAttempts].
const groupsMoved = "rows, the header and attempts move from batch.magus to the safetyscissors groups"

// batchMagusGroup is the API group every job store line carried before ADR 0050 gave the
// records the module's own.
const batchMagusGroup = "batch.magus"

// regroup respells a line stamped with the batch.magus group in the job group, its
// apiVersion and each annotation keyed under the group. A line already in another group
// comes back unchanged, so the rewrite is the same applied twice.
func regroup(e migrate.Entry) (migrate.Entry, error) {
	if raw, ok := e["apiVersion"]; ok {
		var v apiversion.APIVersion
		if err := codec.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("reading apiVersion: %w", err)
		}
		if v.Group() == batchMagusGroup {
			next, err := codec.Marshal(apiversion.APIVersion(fmt.Sprintf("%s/%s", job.APIVersion.Group(), v.Version())))
			if err != nil {
				return nil, err
			}
			e["apiVersion"] = next
		}
	}
	raw, ok := e["metadata"]
	if !ok {
		return e, nil
	}
	var meta map[string]codec.RawMessage
	if err := codec.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("reading metadata: %w", err)
	}
	notesRaw, ok := meta["annotations"]
	if !ok {
		return e, nil
	}
	var notes map[string]string
	if err := codec.Unmarshal(notesRaw, &notes); err != nil {
		return nil, fmt.Errorf("reading metadata.annotations: %w", err)
	}
	moved := make(map[string]string, len(notes))
	for k, v := range notes {
		if rest, found := strings.CutPrefix(k, batchMagusGroup+"/"); found {
			k = fmt.Sprintf("%s/%s", job.APIVersion.Group(), rest)
		}
		moved[k] = v
	}
	var err error
	if meta["annotations"], err = codec.Marshal(moved); err == nil {
		e["metadata"], err = codec.Marshal(meta)
	}
	if err != nil {
		return nil, fmt.Errorf("writing metadata.annotations: %w", err)
	}
	return e, nil
}

// acceptWitnessed writes accept: [witnessed] onto every stored check goal; see
// [job.AcceptWitnessed].
func acceptWitnessed(e migrate.Entry) (migrate.Entry, error) { return job.AcceptWitnessed(e) }

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
// before they nested, so flat reads them. A row already enveloped is kept
// as it is: a store with no header replays this migration over rows a
// later magus wrote.
func envelopeRow(e migrate.Entry) (migrate.Entry, error) {
	if _, enveloped := e["spec"]; enveloped {
		return e, nil
	}
	if raw, ok := e["apiVersion"]; ok {
		var v apiversion.APIVersion
		if err := codec.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("reading apiVersion: %w", err)
		}
		if v == job.APIVersion {
			return e, nil
		}
	}
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
	if _, old := e["schema_version"]; old || (flat.APIVersion != "" && flat.APIVersion.Compare(types.APIVersion(job.APIVersion)) < 0) {
		return nil, types.RefuseVersion(types.APIVersion(job.APIVersion), flat.APIVersion, "job: row "+row.ID, staleStoreFix(storePathToken))
	}
	holder := flat
	if holder.Holder == "server" {
		row.RegisteredBy.EntryPoint = job.EntryPointServer
	}
	row.Paths = job.Paths{Write: flat.WritePaths, Read: flat.ReadPaths, Deny: flat.DenyPaths, Soft: flat.SoftPaths}
	// The record is written in today's spelling, which the later migrations that respell a
	// CamelCase value never see: what they would make of it is made here.
	for i, g := range row.Goals {
		if g.Resolve().Kind == job.GoalKindCheck && len(g.Accept) == 0 {
			row.Goals[i].Accept = []job.Basis{job.BasisWitnessed}
		}
	}
	if row.ModelTier != nil && row.ModelTier.Source == "row" {
		row.ModelTier.Source = tier.SourceJob
	}
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
	line, err := job.MarshalRecord(row, unknown)
	if err != nil {
		return nil, err
	}
	return decodeEntry(line)
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
	next, err := decodeEntry(line)
	if err != nil {
		return nil, err
	}
	for k, v := range e {
		next[k] = v
	}
	return next, nil
}

// decodeEntry is the line text as an entry.
func decodeEntry(text []byte) (migrate.Entry, error) {
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

// staleStoreFix is how a person gets past the job store at path, which an older writer
// wrote. A newer writer's lines are kept whole.
func staleStoreFix(path string) string {
	return fmt.Sprintf("move %s aside, or delete it, and fork the jobs again", path)
}

// keysSnaked names the migration [Store.migrate] rewrites attempts.jsonl before, with
// [Store.snakeAttempts].
const keysSnaked = "records and attempts spell their keys and enum values in snake_case"

// snakeKeys pairs each camelCase key a job store record spelled before [keysSnaked] with
// its snake_case spelling. An explicit table rather than a case conversion, because a
// conversion would respell every camelCase key, those of a member this build keeps without
// declaring among them; the table names only keys a record declared.
func snakeKeys() map[string]string {
	return map[string]string{
		"apiVersion": "api_version", "attemptId": "attempt_id", "baseVerdict": "base_verdict",
		"binaryDigest": "binary_digest", "briefFormat": "brief_format", "bytesReclaimed": "bytes_reclaimed",
		"changedPaths": "changed_paths", "checkoutRoot": "checkout_root", "deadBecause": "dead_because",
		"denialsKnown": "denials_known", "dependsOn": "depends_on", "durationMs": "duration_ms",
		"endReason": "end_reason", "entryPoint": "entry_point", "extraArgs": "extra_args",
		"failingRefs": "failing_refs", "followUps": "follow_ups", "gateAttempts": "gate_attempts",
		"gateId": "gate_id", "holderStopped": "holder_stopped", "itemsRemoved": "items_removed",
		"jobState": "job_state", "lastRun": "last_run", "lastTransitionTime": "last_transition_time",
		"leftUnmerged": "left_unmerged", "maxAttempts": "max_attempts", "maxClaims": "max_claims",
		"modelTier": "model_tier", "observedGeneration": "observed_generation", "outputRef": "output_ref",
		"parentHead": "parent_head", "pauseReason": "pause_reason", "pausedBy": "paused_by",
		"pausedUntil": "paused_until", "readOnly": "read_only", "registeredBy": "registered_by",
		"releaseRequests": "release_requests", "releasedAt": "released_at", "reportedBase": "reported_base",
		"reworkRounds": "rework_rounds", "takenBy": "taken_by", "timestampMs": "timestamp_ms",
		"unresolvedRisks": "unresolved_risks", "verdictRef": "verdict_ref",
		"verificationEvidence": "verification_evidence", "waiterEvidence": "waiter_evidence",
		"writeProof": "write_proof",
	}
}

// opaqueMembers are the members whose values [snakeRecord] never respells: maps a caller
// keys, and a check, whose one renamed member it respells itself.
func opaqueMembers() map[string]bool {
	return map[string]bool{"annotations": true, "labels": true, "check": true}
}

// SnakeKeyCollisionError is a stored object carrying a member under its camelCase key and
// its snake_case key both, so neither can be told for the one the record means.
type SnakeKeyCollisionError struct {
	Path, Camel, Snake string
}

func (e *SnakeKeyCollisionError) Error() string {
	return fmt.Sprintf("%s carries %q and %q, one member under two spellings", e.Path, e.Camel, e.Snake)
}

// UnplacedEnumError is a stored enum value no value list spells, in Go or in the CamelCase
// a record stored before [keysSnaked].
type UnplacedEnumError struct {
	Path, Value string
	Values      []string
}

func (e *UnplacedEnumError) Error() string {
	return fmt.Sprintf("%s is %q, which is no stored value of %s", e.Path, e.Value, strings.Join(e.Values, ", "))
}

// snakeRecord respells one stored line in snake_case: every key [snakeKeys] lists, every
// enum value from the CamelCase the record stored to its Go value, and no_default_charms
// inside each check. A line already respelled comes back unchanged, so the rewrite is the
// same applied twice.
func snakeRecord(e migrate.Entry) (migrate.Entry, error) {
	keys, opaque := snakeKeys(), opaqueMembers()
	out := make(migrate.Entry, len(e))
	for k, v := range e {
		name := snakeKey(keys, k)
		if _, dup := out[name]; dup {
			return nil, &SnakeKeyCollisionError{Path: "the line", Camel: k, Snake: name}
		}
		if !opaque[k] {
			var err error
			if v, err = snakeValue(keys, opaque, k, v); err != nil {
				return nil, err
			}
		}
		out[name] = v
	}
	if err := snakeEnums(out); err != nil {
		return nil, err
	}
	if err := snakeChecks(out); err != nil {
		return nil, err
	}
	return out, nil
}

func snakeKey(keys map[string]string, k string) string {
	if snake, ok := keys[k]; ok {
		return snake
	}
	return k
}

// snakeValue is raw at path with every object's keys respelled, members in opaque left as
// they are.
func snakeValue(keys map[string]string, opaque map[string]bool, path string, raw codec.RawMessage) (codec.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, nil
	}
	switch trimmed[0] {
	case '{':
		var obj map[string]codec.RawMessage
		if err := codec.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		out := make(map[string]codec.RawMessage, len(obj))
		for k, v := range obj {
			name := snakeKey(keys, k)
			if _, dup := out[name]; dup {
				return nil, &SnakeKeyCollisionError{Path: path, Camel: k, Snake: name}
			}
			if !opaque[k] {
				var err error
				if v, err = snakeValue(keys, opaque, fmt.Sprintf("%s.%s", path, name), v); err != nil {
					return nil, err
				}
			}
			out[name] = v
		}
		return codec.Marshal(out)
	case '[':
		var items []codec.RawMessage
		if err := codec.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		for i, v := range items {
			var err error
			if items[i], err = snakeValue(keys, opaque, fmt.Sprintf("%s[%d]", path, i), v); err != nil {
				return nil, err
			}
		}
		return codec.Marshal(items)
	}
	return raw, nil
}

// storedEnum maps each value of an enum, in Go and in the CamelCase a record stored before
// [keysSnaked], to its Go value. CamelCase cannot be inverted, so the Go values are listed.
type storedEnum struct {
	values []string
	known  map[string]string
}

func newStoredEnum[T ~string](values ...T) storedEnum {
	e := storedEnum{known: make(map[string]string, 2*len(values))}
	for _, v := range values {
		e.values = append(e.values, string(v))
		e.known[string(v)] = string(v)
		e.known[camelWord(string(v))] = string(v)
	}
	return e
}

// camelWord is v as a record spelled an enum value before [keysSnaked]: words joined by
// '_' or '-' as one CamelCase word.
func camelWord(v string) string {
	var b strings.Builder
	for word := range strings.FieldsFuncSeq(v, func(r rune) bool { return r == '_' || r == '-' }) {
		b.WriteString(strings.ToUpper(word[:1]) + word[1:])
	}
	return b.String()
}

// read is stored's Go value; an empty value stays empty.
func (e storedEnum) read(path, stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if v, ok := e.known[stored]; ok {
		return v, nil
	}
	return "", &UnplacedEnumError{Path: path, Value: stored, Values: e.values}
}

// enumMember is one enum member of a stored record: the record's kind, the path to it
// below the line, an object member by name and "[]" for every item of an array, and the
// enum it holds. A member holding an array holds the enum in each item.
type enumMember struct {
	kind string
	path []string
	enum storedEnum
}

// recordEnums are the enum members of a job row and an attempt.
func recordEnums() []enumMember {
	entry := newStoredEnum(job.EntryPointCLI, job.EntryPointHook, job.EntryPointMCP, job.EntryPointRPC, job.EntryPointServer)
	creds := newStoredEnum(credentialKinds()...)
	fixed := []enumMember{
		{"Job", []string{"spec", "brief_format"}, newStoredEnum(job.BriefFormatMarkdown, job.BriefFormatBuzz)},
		{"Job", []string{"spec", "agent", "worker", "type"}, newStoredEnum(job.WorkerTypeResumed, job.WorkerTypeNew)},
		{"Job", []string{"spec", "goals", "[]", "kind"}, newStoredEnum(job.GoalKindCheck, job.GoalKindPaths, job.GoalKindSymbol)},
		{"Job", []string{"spec", "goals", "[]", "expect"}, newStoredEnum(job.ExpectPassed, job.ExpectChanged, job.ExpectPresent,
			job.ExpectAbsent, job.ExpectUnreferenced, job.ExpectSignature)},
		{"Job", []string{"spec", "goals", "[]", "accept"}, newStoredEnum(job.Bases()...)},
		{"Job", []string{"status", "model_tier", "skills"}, newStoredEnum(tier.SkillVariantShort, tier.SkillVariantFull)},
		{"Job", []string{"status", "model_tier", "source"}, newStoredEnum(tier.SourceJob, tier.SourceMatrix, tier.SourceUserMatrix, tier.SourceDefault)},
		{"Job", []string{"status", "write_proof"}, newStoredEnum(job.WriteProofAlone, job.WriteProofDisjoint, job.WriteProofOverlapping)},
		{"Job", []string{"status", "base_verdict"}, newStoredEnum(job.BaseMatch, job.BaseRevisionMatch, job.BaseDiverged, job.BaseUnknown)},
		{"Job", []string{"status", "claims", "[]", "evidence", "kind"}, newStoredEnum(job.ClaimRefs, job.ClaimTest, job.ClaimOutput,
			job.ClaimReleased, job.ClaimDeadHolder)},
		{"Attempt", []string{"status", "end"}, newStoredEnum(job.AttemptEndExit, job.AttemptEndHandback, job.AttemptEndStop,
			job.AttemptEndDeadline, job.AttemptEndJob)},
		{"Attempt", []string{"status", "job_state"}, newStoredEnum(storedStates()...)},
		{"Attempt", []string{"status", "retry", "change"}, newStoredEnum(job.RetryAfterConfig, job.RetryAfterWidening,
			job.RetryAfterPerson, job.RetryAfterUpgrade)},
	}
	origins := [][]string{{"status", "paused_by"}, {"status", "registered_by"}, {"status", "claims", "[]", "by"}, {"status", "entries", "[]", "by"}}
	by := make([]enumMember, 0, 2*len(origins))
	for _, at := range origins {
		by = append(by,
			enumMember{"Job", append(slices.Clone(at), "entry_point"), entry},
			enumMember{"Job", append(slices.Clone(at), "credential", "class"), creds})
	}
	return slices.Concat(fixed, by)
}

// storedStates is every job state, as [job.StateVocabulary] lists them.
func storedStates() []job.State {
	words := strings.Split(job.StateVocabulary(), ", ")
	out := make([]job.State, 0, len(words))
	for _, s := range words {
		out = append(out, job.State(s))
	}
	return out
}

// snakeEnums respells every enum member of e, a line of its kind, to its Go value.
func snakeEnums(e migrate.Entry) error {
	var kind string
	if raw, ok := e["kind"]; ok {
		if err := codec.Unmarshal(raw, &kind); err != nil {
			return fmt.Errorf("reading kind: %w", err)
		}
	}
	for _, m := range recordEnums() {
		if m.kind != kind {
			continue
		}
		raw, ok := e[m.path[0]]
		if !ok {
			continue
		}
		next, err := walkEnum(raw, m.path[0], m.path[1:], m.enum)
		if err != nil {
			return err
		}
		e[m.path[0]] = next
	}
	return nil
}

// walkEnum is raw, found at at, with the enum below it along path respelled.
func walkEnum(raw codec.RawMessage, at string, path []string, enum storedEnum) (codec.RawMessage, error) {
	if len(path) == 0 {
		return respellEnum(raw, at, enum)
	}
	if path[0] == "[]" {
		var items []codec.RawMessage
		if err := codec.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("reading %s: %w", at, err)
		}
		for i, item := range items {
			next, err := walkEnum(item, fmt.Sprintf("%s[%d]", at, i), path[1:], enum)
			if err != nil {
				return nil, err
			}
			items[i] = next
		}
		return codec.Marshal(items)
	}
	var obj map[string]codec.RawMessage
	if err := codec.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("reading %s: %w", at, err)
	}
	member, ok := obj[path[0]]
	if !ok {
		return raw, nil
	}
	next, err := walkEnum(member, fmt.Sprintf("%s.%s", at, path[0]), path[1:], enum)
	if err != nil {
		return nil, err
	}
	obj[path[0]] = next
	return codec.Marshal(obj)
}

// respellEnum is raw, a value or an array of values of enum, as its Go values.
func respellEnum(raw codec.RawMessage, at string, enum storedEnum) (codec.RawMessage, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '[' {
		var values []string
		if err := codec.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("reading %s: %w", at, err)
		}
		for i, v := range values {
			var err error
			if values[i], err = enum.read(fmt.Sprintf("%s[%d]", at, i), v); err != nil {
				return nil, err
			}
		}
		return codec.Marshal(values)
	}
	var v string
	if err := codec.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("reading %s: %w", at, err)
	}
	next, err := enum.read(at, v)
	if err != nil {
		return nil, err
	}
	return codec.Marshal(next)
}

// snakeChecks respells noDefaultCharms inside the check of a row's spec and of each of its
// goals.
func snakeChecks(e migrate.Entry) error {
	raw, ok := e["spec"]
	if !ok {
		return nil
	}
	var spec map[string]codec.RawMessage
	if err := codec.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("reading spec: %w", err)
	}
	var err error
	if check, ok := spec["check"]; ok {
		if spec["check"], err = snakeCheck(check, "spec.check"); err != nil {
			return err
		}
	}
	if goalsRaw, ok := spec["goals"]; ok {
		var goals []map[string]codec.RawMessage
		if err := codec.Unmarshal(goalsRaw, &goals); err != nil {
			return fmt.Errorf("reading spec.goals: %w", err)
		}
		for i, g := range goals {
			if check, ok := g["check"]; ok {
				if g["check"], err = snakeCheck(check, fmt.Sprintf("spec.goals[%d].check", i)); err != nil {
					return err
				}
			}
		}
		if spec["goals"], err = codec.Marshal(goals); err != nil {
			return err
		}
	}
	if e["spec"], err = codec.Marshal(spec); err != nil {
		return err
	}
	return nil
}

// snakeCheck is the stored check raw, at at, with noDefaultCharms respelled. A null check
// is kept.
func snakeCheck(raw codec.RawMessage, at string) (codec.RawMessage, error) {
	if !job.HasCheck(raw) {
		return raw, nil
	}
	var check map[string]codec.RawMessage
	if err := codec.Unmarshal(raw, &check); err != nil {
		return nil, fmt.Errorf("reading %s: %w", at, err)
	}
	v, ok := check["noDefaultCharms"]
	if !ok {
		return raw, nil
	}
	if _, dup := check["no_default_charms"]; dup {
		return nil, &SnakeKeyCollisionError{Path: at, Camel: "noDefaultCharms", Snake: "no_default_charms"}
	}
	delete(check, "noDefaultCharms")
	check["no_default_charms"] = v
	return codec.Marshal(check)
}
