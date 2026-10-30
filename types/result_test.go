package types

import (
	"testing"

	"github.com/egladman/magus/internal/json"
)

func TestResultConstructors(t *testing.T) {
	ok := Ok(42)
	if !ok.IsOk() || ok != (Result[int]{Value: 42}) {
		t.Errorf("Ok(42) = %+v", ok)
	}

	d := Diagnostic{Code: "MGS1001", Message: "no ci target", Fix: "declare ci"}
	failed := Fail[int](d)
	if failed.IsOk() || failed.Value != 0 || failed.Error == nil || *failed.Error != d {
		t.Errorf("Fail(d) = %+v", failed)
	}
}

func TestResultJSON(t *testing.T) {
	cases := []struct {
		name string
		in   Result[string]
		want string
	}{
		{"ok", Ok("v"), `{"value":"v"}`},
		{"fail", Fail[string](Diagnostic{Code: "MGS1001", Message: "m", Fix: "f"}),
			`{"value":"","error":{"code":"MGS1001","message":"m","fix":"f"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("json = %s, want %s", got, tc.want)
			}
		})
	}
}
