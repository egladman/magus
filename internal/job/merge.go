package job

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// DecodePut reads one put from a param map shaped like magus\job.put's opts, the map
// contract at the Buzz edge: only the keys the caller named are set, and a key present
// with an empty value is an explicit clear.
//
// It is strict. A key outside [mergeFields], a value of the wrong type, check beside
// validation, and every refusal [PutMerge] makes are errors, joined rather than returned
// at the first one: a client that mistyped two params learns about both in one round trip.
// `op` and `id` may ride alongside, since they are how a door names the call.
func DecodePut(params map[string]any) (types.JobPut, error) {
	var put types.JobPut
	err := unknownParams(params)
	str := func(key string, dst **string) {
		v, ok, e := mergeString(params, key)
		switch {
		case e != nil:
			err = errors.Join(err, e)
		case ok:
			*dst = &v
		}
	}
	list := func(key string, dst **[]string) {
		v, ok, e := mergeList(params, key)
		switch {
		case e != nil:
			err = errors.Join(err, e)
		case ok:
			*dst = &v
		}
	}
	str("parent", &put.Parent)
	str("criteria", &put.Criteria)
	str("checkpoint", &put.Checkpoint)
	str("model", &put.Model)
	str("follows", &put.Follows)
	str("fresh_reason", &put.FreshReason)
	if v, ok, e := mergeString(params, "brief_format"); e != nil {
		err = errors.Join(err, e)
	} else if ok {
		f := types.BriefFormat(strings.TrimSpace(v))
		put.BriefFormat = &f
	}
	str("timeout", &put.Timeout)
	str("enter", &put.Enter)
	list("write_paths", &put.WritePaths)
	list("deny_paths", &put.DenyPaths)
	list("read_paths", &put.ReadPaths)
	list("depends_on", &put.DependsOn)
	list("breaks", &put.Breaks)
	if goals, ok, e := decodeObjects[types.Goal](params, "goals", "goals"); e != nil {
		err = errors.Join(err, e)
	} else if ok {
		put.Goals = &goals
	}
	if deviations, ok, e := decodeObjects[types.Deviation](params, "deviations", "deviations"); e != nil {
		err = errors.Join(err, e)
	} else if ok {
		put.Deviations = &deviations
	}

	// check and validation are two spellings of one field, so a put naming each of them is
	// a caller that does not know which one it meant.
	check := func(key string, parse func(string) (types.LeaseCheck, error)) {
		v, ok, e := mergeString(params, key)
		switch {
		case e != nil:
			err = errors.Join(err, e)
		case !ok:
		case strings.TrimSpace(v) == "":
			put.Check = &types.LeaseCheck{}
		default:
			c, perr := parse(v)
			if perr != nil {
				err = errors.Join(err, fmt.Errorf("job: %s: %w", key, perr))
				return
			}
			put.Check = &c
		}
	}
	_, hasCheck := params["check"]
	_, hasLine := params["validation"]
	switch {
	case hasCheck && hasLine:
		err = errors.Join(err, errors.New("job: a put carries `check` or a rendered `validation` line, not both"))
	case hasCheck:
		check("check", types.ParseLeaseCheck)
	case hasLine:
		// compat(until: no client still sends a rendered line; observe: the grep
		// types.Job.Validation names).
		check("validation", types.ParseLeaseRunLine)
	}

	if v, ok, e := mergeString(params, "state"); e != nil {
		err = errors.Join(err, e)
	} else if ok {
		s := types.JobState(strings.TrimSpace(v))
		put.State = &s
	}
	if v, present := params["read_only"]; present {
		if b, ok := v.(bool); ok {
			put.ReadOnly = &b
		} else {
			err = errors.Join(err, errors.New("job: read_only must be a boolean"))
		}
	}
	if n, ok, e := mergeCount(params, "max_attempts"); e != nil {
		err = errors.Join(err, e)
	} else if ok {
		put.MaxAttempts = &n
	}
	if n, ok, e := mergeCount(params, "max_claims"); e != nil {
		err = errors.Join(err, e)
	} else if ok {
		put.MaxClaims = &n
	}
	if v, present := params["weight"]; present {
		switch w := v.(type) {
		case float64:
			put.Weight = &w
		case int:
			f := float64(w)
			put.Weight = &f
		case int64:
			f := float64(w)
			put.Weight = &f
		default:
			err = errors.Join(err, errors.New("job: weight must be a number"))
		}
	}
	if _, perr := PutMerge(put); perr != nil {
		err = errors.Join(err, perr)
	}
	if err != nil {
		return types.JobPut{}, err
	}
	return put, nil
}

// PutMerge validates p and returns the merge it applies to a row: only the fields p sets,
// so a later put advances a row without erasing what an earlier one declared.
//
// Every value is checked HERE rather than inside the returned merge, because Store.Update
// runs it while holding the lock and has no way to report a failure. A state outside the
// vocabulary, a timeout that does not parse, goals that do not validate, a negative
// max_attempts, and an enter beside any other field are errors, joined.
func PutMerge(p types.JobPut) (func(*types.Job), error) {
	var (
		set []func(*types.Job)
		err error
	)
	if p.Parent != nil {
		v := strings.TrimSpace(*p.Parent)
		set = append(set, func(u *types.Job) { u.Parent = v })
	}
	if p.Criteria != nil {
		v := *p.Criteria
		set = append(set, func(u *types.Job) { u.Criteria = v })
	}
	if p.Checkpoint != nil {
		v := strings.TrimSpace(*p.Checkpoint)
		set = append(set, func(u *types.Job) { u.Checkpoint = v })
	}
	if p.Model != nil {
		v := strings.TrimSpace(*p.Model)
		set = append(set, func(u *types.Job) { u.Model = v })
	}
	if p.Follows != nil {
		v := strings.TrimSpace(*p.Follows)
		set = append(set, func(u *types.Job) { u.Follows = v })
	}
	if p.FreshReason != nil {
		v := strings.TrimSpace(*p.FreshReason)
		set = append(set, func(u *types.Job) { u.FreshReason = v })
	}
	if p.BriefFormat != nil {
		if f := *p.BriefFormat; f != "" && !f.Valid() {
			err = errors.Join(err, fmt.Errorf("job: brief_format %q is not one of %s, or empty for magus.yaml's agents.brief_format",
				f, strings.Join(f.Values(), ", ")))
		} else {
			set = append(set, func(u *types.Job) { u.BriefFormat = f })
		}
	}
	for _, l := range []struct {
		v     *[]string
		apply func(*types.Job, []string)
	}{
		{p.WritePaths, func(u *types.Job, v []string) { u.WritePaths = v }},
		{p.DenyPaths, func(u *types.Job, v []string) { u.DenyPaths = v }},
		{p.ReadPaths, func(u *types.Job, v []string) { u.ReadPaths = v }},
		{p.DependsOn, func(u *types.Job, v []string) { u.DependsOn = v }},
		{p.Breaks, func(u *types.Job, v []string) { u.Breaks = v }},
	} {
		if l.v != nil {
			v, apply := slices.Clone(*l.v), l.apply
			set = append(set, func(u *types.Job) { apply(u, v) })
		}
	}
	if p.Goals != nil {
		goals := slices.Clone(*p.Goals)
		if e := (types.Declaration{ID: "merge", Goals: goals}).Validate(); e != nil {
			err = errors.Join(err, e)
		} else {
			set = append(set, func(u *types.Job) { u.Goals = goals })
		}
	}
	if p.Deviations != nil {
		deviations := slices.Clone(*p.Deviations)
		if e := (types.Declaration{ID: "merge", Deviations: deviations}).Validate(); e != nil {
			err = errors.Join(err, e)
		} else {
			set = append(set, func(u *types.Job) { u.Deviations = deviations })
		}
	}
	if p.Check != nil {
		if p.Check.Target == "" && p.Check.Script == "" {
			set = append(set, func(u *types.Job) { u.Check, u.Validation = nil, "" })
		} else {
			c := *p.Check
			c.Args = slices.Clone(c.Args)
			set = append(set, func(u *types.Job) { u.Check, u.Validation = &c, c.String() })
		}
	}
	if p.State != nil {
		if s := *p.State; !types.ValidJobState(s) {
			err = errors.Join(err, fmt.Errorf("job: state must be one of %s", types.JobStateVocabulary()))
		} else {
			set = append(set, func(u *types.Job) { u.State = s })
		}
	}
	// A length, never an instant: the deadline is stamped when the store applies the merge.
	if p.Timeout != nil {
		if d, perr := types.ParseJobTimeout(*p.Timeout); perr != nil {
			err = errors.Join(err, perr)
		} else {
			set = append(set, func(u *types.Job) {
				u.Deadline = 0
				if d > 0 {
					u.Deadline = deadlineAfter(d)
				}
			})
		}
	}
	// An entry changes no declared field, so it travels alone and the store grades it as
	// an entry; see Store.Enter.
	if p.Enter != nil {
		rel, perr := entryPath(*p.Enter)
		for _, key := range declaredKeys(p) {
			perr = errors.Join(perr, fmt.Errorf("job: a put carrying enter enters a job and declares nothing, so %s does not belong beside it", key))
		}
		if perr != nil {
			err = errors.Join(err, perr)
		} else {
			set = append(set, func(u *types.Job) { u.Entries = append(u.Entries, types.JobEntry{Path: rel}) })
		}
	}
	if p.ReadOnly != nil {
		b := *p.ReadOnly
		set = append(set, func(u *types.Job) { u.ReadOnly = b })
	}
	if p.MaxAttempts != nil {
		if n := *p.MaxAttempts; n < 0 {
			err = errors.Join(err, errors.New("job: max_attempts must be a whole number of at least 0"))
		} else {
			set = append(set, func(u *types.Job) { u.MaxAttempts = n })
		}
	}
	if p.MaxClaims != nil {
		if n := *p.MaxClaims; n < 0 {
			err = errors.Join(err, errors.New("job: max_claims must be a whole number of at least 0"))
		} else {
			set = append(set, func(u *types.Job) { u.MaxClaims = n })
		}
	}
	if p.Weight != nil {
		if w := *p.Weight; !types.ValidJobWeight(w) {
			err = errors.Join(err, fmt.Errorf("job: weight %v is outside (0,1]", w))
		} else {
			set = append(set, func(u *types.Job) { u.Weight = w })
		}
	}
	if err != nil {
		return nil, err
	}
	return func(u *types.Job) {
		for _, apply := range set {
			apply(u)
		}
	}, nil
}

// declaredKeys names the fields p sets other than Enter, by the keys a put map spells
// them with.
func declaredKeys(p types.JobPut) []string {
	var keys []string
	for _, f := range []struct {
		key string
		set bool
	}{
		{"parent", p.Parent != nil}, {"criteria", p.Criteria != nil}, {"checkpoint", p.Checkpoint != nil},
		{"model", p.Model != nil}, {"brief_format", p.BriefFormat != nil}, {"timeout", p.Timeout != nil}, {"state", p.State != nil},
		{"check", p.Check != nil}, {"write_paths", p.WritePaths != nil}, {"deny_paths", p.DenyPaths != nil},
		{"read_paths", p.ReadPaths != nil}, {"depends_on", p.DependsOn != nil}, {"breaks", p.Breaks != nil},
		{"goals", p.Goals != nil}, {"deviations", p.Deviations != nil}, {"read_only", p.ReadOnly != nil},
		{"max_attempts", p.MaxAttempts != nil}, {"max_claims", p.MaxClaims != nil}, {"follows", p.Follows != nil}, {"fresh_reason", p.FreshReason != nil},
		{"weight", p.Weight != nil},
	} {
		if f.set {
			keys = append(keys, f.key)
		}
	}
	return keys
}

// mergeFields are the row fields a put may carry. `op` and `id` ride alongside them
// because they are how a door names the call rather than fields of the row.
var mergeFields = []string{
	"parent", "criteria", "checkpoint", "write_paths", "deny_paths", "read_paths",
	"depends_on", "breaks", "model", "brief_format", "check", "validation", "goals", "deviations", "state", "read_only",
	"timeout", "max_attempts", "max_claims", "weight", "enter", "follows", "fresh_reason",
}

// unknownParams rejects a key outside that set. A dropped key reads to its sender exactly
// like one that was taken into account, and the tool then reports the row as written.
func unknownParams(params map[string]any) error {
	if dir, ok := params["checkout"]; ok {
		return fmt.Errorf("job: a put names checkout %v, which only a fork makes, `%s` makes it", dir, hint.JobFork.With("<job>", "--checkout", fmt.Sprint(dir)))
	}
	if branches, ok := params["integrates"]; ok {
		return fmt.Errorf("job: a put names integrates %v, which only a fork takes, since the fork computes the job's write paths from those branches: `%s` with a record naming them",
			branches, hint.JobFork.With("--stdin"))
	}
	if _, ok := params["claim"]; ok {
		return fmt.Errorf("job: a put names claim, which only `%s` proves", hint.JobApply.With("-f", "<record>"))
	}
	var unknown []string
	for key := range params {
		if key == "op" || key == "id" || slices.Contains(mergeFields, key) {
			continue
		}
		unknown = append(unknown, key)
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	return fmt.Errorf("job: no field of a job row is named %s, a put carries %s",
		strings.Join(unknown, ", "), strings.Join(mergeFields, ", "))
}

// mergeString distinguishes an absent key from an empty value, which is what lets a put
// carry only the fields it means to change. A present key holding something that is not
// a string is an ERROR, not an absent one: silently dropping it records a row the
// caller did not ask for, and the caller is then told the put succeeded.
func mergeString(params map[string]any, key string) (string, bool, error) {
	v, present := params[key]
	if !present {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("job: %s must be a string", key)
	}
	return s, true, nil
}

// mergeCount reads a whole number of at least 0: a JSON number decodes as float64 and a Buzz
// int as int64, and a client the MCP descriptor holds to strings sends it as decimal text.
// A fraction or a negative is refused, never truncated or clamped.
func mergeCount(params map[string]any, key string) (int, bool, error) {
	v, present := params[key]
	if !present {
		return 0, false, nil
	}
	bad := fmt.Errorf("job: %s must be a whole number of at least 0", key)
	var n int
	switch t := v.(type) {
	case int:
		n = t
	case int64:
		n = int(t)
	case float64:
		if t != float64(int(t)) {
			return 0, false, bad
		}
		n = int(t)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, false, bad
		}
		n = parsed
	default:
		return 0, false, bad
	}
	if n < 0 {
		return 0, false, bad
	}
	return n, true, nil
}

// mergeList accepts the natural JSON/Buzz array shape as well as the space-separated
// string the MCP descriptor schema forces on typed clients (magus\describe.file's paths
// set the precedent). A caller sending a real array must not silently record nothing,
// nor may one element of the wrong type quietly shorten the list, so both are reported.
func mergeList(params map[string]any, key string) ([]string, bool, error) {
	v, present := params[key]
	if !present {
		return nil, false, nil
	}
	badType := fmt.Errorf("job: %s must be an array of strings or a space-separated string", key)
	switch t := v.(type) {
	case string:
		return strings.Fields(t), true, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false, badType
			}
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out, true, nil
	}
	return nil, false, badType
}

// decodeObjects accepts the natural JSON/Buzz object array, or that array as a JSON string
// for a client the MCP descriptor holds to scalars; what names the elements in the error.
// [PutMerge] validates them through the same typed declaration boundary stdin uses.
func decodeObjects[T any](params map[string]any, key, what string) ([]T, bool, error) {
	v, present := params[key]
	if !present {
		return nil, false, nil
	}
	body, err := json.Marshal(v)
	if s, ok := v.(string); ok {
		body, err = []byte(s), nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("job: %s must be an array of %s: %w", key, what, err)
	}
	var objects []T
	if err := json.Unmarshal(body, &objects); err != nil {
		return nil, false, fmt.Errorf("job: %s must be an array of %s: %w", key, what, err)
	}
	return objects, true, nil
}
