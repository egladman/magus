package proofread

import "testing"

func TestEveryBuiltInKindReadsTheFormatOfEachOfItsRules(t *testing.T) {
	for _, s := range builtinKinds {
		for _, c := range checks {
			if s.judges(c.rule) && !c.canRead(s.Reader.Format) {
				t.Errorf("%s judges %s, whose judge reads %v, not %s", s.Name, c.rule, c.reads, s.Reader.Format)
			}
		}
	}
}

func TestEveryRuleReadsAFormatSomeBuiltInKindReads(t *testing.T) {
	for _, c := range checks {
		read := false

		for _, s := range builtinKinds {
			read = read || (s.judges(c.rule) && c.canRead(s.Reader.Format))
		}

		if !read {
			t.Errorf("no built-in kind judges %s", c.rule)
		}
	}
}

func TestBuiltinKindReturnsACopy(t *testing.T) {
	s, ok := BuiltinKind(KindReference)
	if !ok {
		t.Fatal("no reference kind")
	}

	s.Rules[RuleFiller] = DecisionDeny

	if got := spec(KindReference).Rules[RuleFiller]; got != DecisionAdvise {
		t.Errorf("reference filler = %s after editing a copy, want advise", got)
	}
}

func TestBuiltinKindReadsAFormerName(t *testing.T) {
	s, ok := BuiltinKind("message")
	if !ok || s.Name != KindDiagnostic {
		t.Errorf("BuiltinKind(message) = %q, %t, want diagnostic", s.Name, ok)
	}
}
