package pycompat

import (
	"math"
	"strings"
	"testing"
)

// Every expected value below was printed by CPython 3.14 on the host:
// random.Random(seed) draws, math.fsum, repr and json.dumps.

func TestBenchRandomIntSeedMatchesCPython(t *testing.T) {
	r := NewRandomInt(20260902)
	want := []float64{0.816797988684408, 0.6096000518410029, 0.7468696412598451, 0.8209786640137855, 0.971871000250248, 0.08854444946100504}
	for i, w := range want {
		if got := r.Float64(); got != w {
			t.Fatalf("random() #%d = %v, want %v", i, got, w)
		}
	}
}

func TestBenchRandomSeedShapes(t *testing.T) {
	cases := []struct {
		name string
		rng  *Random
		want [2]float64
	}{
		{"zero", NewRandomInt(0), [2]float64{0.8444218515250481, 0.7579544029403025}},
		{"two words", NewRandomInt(1 << 32), [2]float64{0.11299430095636409, 0.41782886486292836}},
		{"negative uses the magnitude", NewRandomInt(-5), [2]float64{0.6229016948897019, 0.7417869892607294}},
		{"max int64", NewRandomInt(math.MaxInt64), [2]float64{0.3166448820870279, 0.631259308253863}},
		{"string", NewRandomString("20260902|dollars|task-a"), [2]float64{0.5873082740269127, 0.9266754726742051}},
		{"empty string", NewRandomString(""), [2]float64{0.9602256525641875, 0.595411957851699}},
		{"leading NUL bytes drop high zero words", NewRandomString("\x00\x00abc"), [2]float64{0.2834855576453934, 0.793594952900316}},
	}
	for _, c := range cases {
		got := [2]float64{c.rng.Float64(), c.rng.Float64()}
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBenchGetRandBits(t *testing.T) {
	r := NewRandomInt(20260902)
	for i, want := range []uint64{304155831376, 1042000297902, 398344771905} {
		if got := r.GetRandBits(40); got != want {
			t.Fatalf("getrandbits(40) #%d = %d, want %d", i, got, want)
		}
	}
	r = NewRandomInt(20260902)
	for _, c := range []struct {
		k    int
		want uint64
	}{{64, 5094823856520670288}, {5, 19}, {33, 8361363414}} {
		if got := r.GetRandBits(c.k); got != c.want {
			t.Fatalf("getrandbits(%d) = %d, want %d", c.k, got, c.want)
		}
	}
}

func TestBenchRandRange(t *testing.T) {
	cases := []struct {
		name string
		rng  *Random
		n    int
		want []int
	}{
		{"int seed, n=3", NewRandomInt(20260902), 3, []int{1, 2, 2, 1, 0, 2, 0, 2, 1, 0, 2, 2}},
		{"int seed, n=1000", NewRandomInt(20260902), 1000, []int{836, 282, 624, 969, 764, 370}},
		{"string seed, n=3", NewRandomString("20260902|dollars|task-a"), 3, []int{2, 0, 2, 1, 2, 2, 0, 0, 2, 2, 0, 0}},
	}
	for _, c := range cases {
		for i, w := range c.want {
			if got := c.rng.RandRange(c.n); got != w {
				t.Errorf("%s: draw #%d = %d, want %d", c.name, i, got, w)
			}
		}
	}
}

func TestBenchFSum(t *testing.T) {
	cases := []struct {
		values []float64
		want   float64
	}{
		{[]float64{1e-16, 1, 1e16}, 1.0000000000000002e+16},
		{[]float64{0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1}, 1.0},
		{[]float64{1e100, 1.0, -1e100, 1e-100}, 1.0},
		{[]float64{0.3995908, 0.35016109999999995, 0.5824523}, 1.3322042},
		{nil, 0.0},
		{[]float64{1.0, 1e-17, -1.0}, 1e-17},
	}
	for _, c := range cases {
		got, err := FSum(c.values)
		if err != nil {
			t.Fatalf("fsum(%v): %v", c.values, err)
		}
		if got != c.want {
			t.Errorf("fsum(%v) = %v, want %v", c.values, got, c.want)
		}
	}
	if _, err := FSum([]float64{1e308, 1e308}); err == nil {
		t.Error("fsum overflow: want an error")
	}
}

func TestBenchFloatRepr(t *testing.T) {
	cases := map[float64]string{
		1e16: "1e+16", 1e15: "1000000000000000.0", 0.0001: "0.0001", 0.00001: "1e-05",
		1.5e300: "1.5e+300", 123456789012345678.0: "1.2345678901234568e+17", 5e-324: "5e-324",
		0.1: "0.1", 100.0: "100.0", 1234567.0: "1234567.0", 2.5e-7: "2.5e-07", 1e22: "1e+22",
		0.30000000000000004: "0.30000000000000004", 0.0: "0.0",
	}
	for f, want := range cases {
		if got := FloatRepr(f); got != want {
			t.Errorf("repr(%v) = %q, want %q", f, got, want)
		}
	}
	if got := FloatRepr(math.Copysign(0, -1)); got != "-0.0" {
		t.Errorf("repr(-0.0) = %q", got)
	}
}

func TestBenchNumberKeepsIntAndFloatApart(t *testing.T) {
	i, err := ParseNumber("3")
	if err != nil || i.IsFloat() || i.String() != "3" {
		t.Fatalf("ParseNumber(3) = %v, %v", i, err)
	}
	for _, lit := range []string{"3.0", "3e0", "3E0"} {
		f, err := ParseNumber(lit)
		if err != nil || !f.IsFloat() || f.String() != "3.0" {
			t.Fatalf("ParseNumber(%s) = %v, %v", lit, f, err)
		}
	}
	if got := Int(7).Sub(Int(2)); got.IsFloat() || got.String() != "5" {
		t.Errorf("int - int = %v", got)
	}
	if got := Int(7).Sub(Float(2)); !got.IsFloat() || got.String() != "5.0" {
		t.Errorf("int - float = %v", got)
	}
	if got := Int(7).TrueDiv(2); got != 3.5 {
		t.Errorf("7 / 2 = %v", got)
	}
}

func TestBenchMarshalIsJSONDumpsSortKeys(t *testing.T) {
	doc := map[string]any{
		"b":                 []any{1, 2.0, nil, true},
		"a":                 map[string]any{},
		"c":                 []string{},
		"é\U0001F600\n\"\\": "x/y\x7f",
	}
	want := "{\"a\": {}, \"b\": [1, 2.0, null, true], \"c\": [], " +
		"\"\\u00e9\\ud83d\\ude00\\n\\\"\\\\\": \"x/y\\u007f\"}"
	got, err := Marshal(doc, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("compact:\n got %s\nwant %s", got, want)
	}
	indented := map[string]any{"b": []any{1, map[string]any{"z": 2}}, "a": map[string]any{}, "c": []any{}}
	want = strings.Join([]string{
		"{", `  "a": {},`, `  "b": [`, "    1,", "    {", `      "z": 2`, "    }", "  ],", `  "c": []`, "}",
	}, "\n")
	got, err = Marshal(indented, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("indent=2:\n got %s\nwant %s", got, want)
	}
}

func TestBenchMarshalStructsSortTaggedFields(t *testing.T) {
	type rec struct {
		Zeta   *string  `json:"zeta"`
		Alpha  Number   `json:"alpha"`
		Skip   int      `json:"-"`
		Values []int64  `json:"values"`
		Ratio  *float64 `json:"ratio"`
	}
	ratio := 0.5
	got, err := Marshal(rec{Alpha: Float(1), Skip: 9, Ratio: &ratio}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"alpha": 1.0, "ratio": 0.5, "values": [], "zeta": null}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestBenchUnmarshalRoundTripsNumbers(t *testing.T) {
	doc, err := Unmarshal([]byte(`{"i": 5, "f": 5.0, "e": 1e2, "s": "é", "l": [null, false]}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(doc, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"e\": 100.0, \"f\": 5.0, \"i\": 5, \"l\": [null, false], \"s\": \"\\u00e9\"}"; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if _, err := Unmarshal([]byte(`{} {}`)); err == nil {
		t.Error("trailing data: want an error")
	}
}
