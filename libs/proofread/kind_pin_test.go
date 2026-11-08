package proofread

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// kindPin holds every built-in kind's rules, in [Rules] order, with the
// decision each declared before [unmeasured], as they stood when kinds became
// specs (ADR 0033). KIND_PIN_UPDATE=1 rewrites it.
const kindPin = "testdata/kind-rules.jsonl"

type pinnedRule struct {
	Rule     Rule     `json:"rule"`
	Decision Decision `json:"decision"`
}

type pinnedKind struct {
	Kind  Kind         `json:"kind"`
	Rules []pinnedRule `json:"rules"`
}

func TestEveryBuiltInKindJudgesItsPinnedRules(t *testing.T) {
	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	for _, k := range kinds {
		p := pinnedKind{Kind: k, Rules: []pinnedRule{}}
		for _, r := range KindRules(k) {
			p.Rules = append(p.Rules, pinnedRule{r, declaredDecision(k, r)})
		}

		if err := enc.Encode(p); err != nil {
			t.Fatal(err)
		}
	}

	if os.Getenv("KIND_PIN_UPDATE") == "1" {
		if err := os.WriteFile(kindPin, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(kindPin)
	if err != nil {
		t.Fatalf("%v: run with KIND_PIN_UPDATE=1 to write it", err)
	}

	if !bytes.Equal(b.Bytes(), want) {
		got, wantLines := bytes.Split(b.Bytes(), []byte("\n")), bytes.Split(want, []byte("\n"))
		for i := range min(len(got), len(wantLines)) {
			if !bytes.Equal(got[i], wantLines[i]) {
				t.Fatalf("%s:%d moved:\n got %s\nwant %s", kindPin, i+1, got[i], wantLines[i])
			}
		}

		t.Fatalf("%s holds %d kinds, the specs %d", kindPin, len(wantLines), len(got))
	}
}

// reviewKinds are the built-in kinds a review posts.
func reviewKinds() []Kind {
	var out []Kind

	for _, s := range builtinKinds {
		if s.Review {
			out = append(out, s.Name)
		}
	}

	return out
}

// everyDecision reports whether every built-in kind that judges r declares d
// for it, and some kind judges it.
func everyDecision(r Rule, d Decision) bool {
	judged := false

	for _, s := range builtinKinds {
		if got, ok := s.Rules[r]; ok {
			if got != d {
				return false
			}

			judged = true
		}
	}

	return judged
}

// declaredDecision is what kind declares for r before [unmeasured].
func declaredDecision(kind Kind, r Rule) Decision {
	return spec(kind).Rules[r]
}
