package schema

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// BenchmarkFieldLookup measures O(1) map lookup via FieldByEnv.
func BenchmarkFieldLookup(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = FieldByEnv("MAGUS_CACHE_DIR")
	}
}

// BenchmarkFieldLookup_miss measures miss path (key not in map).
func BenchmarkFieldLookup_miss(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = FieldByEnv("MAGUS_DOES_NOT_EXIST")
	}
}

// BenchmarkFieldLookup_reflect is the control: linear scan via reflect, as a
// baseline to demonstrate the advantage of the precomputed map.
func BenchmarkFieldLookup_reflect(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		target := "MAGUS_CACHE_DIR"
		for _, f := range Fields {
			if f.EnvVar == target {
				break
			}
		}
	}
}

// BenchmarkApplyEnv_AllUnset measures the hot path where no MAGUS_* vars are
// set — essentially N empty-string checks.
func BenchmarkApplyEnv_AllUnset(b *testing.B) {
	// Ensure none of the MAGUS_ vars exist in the process environment.
	saved := make(map[string]string)
	for _, f := range Fields {
		if v, ok := os.LookupEnv(f.EnvVar); ok {
			saved[f.EnvVar] = v
			if err := os.Unsetenv(f.EnvVar); err != nil {
				b.Fatalf("unsetenv %s: %v", f.EnvVar, err)
			}
		}
	}
	b.Cleanup(func() {
		for k, v := range saved {
			if err := os.Setenv(k, v); err != nil {
				b.Errorf("restoring %s: %v", k, err)
			}
		}
	})

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, f := range Fields {
			_ = os.Getenv(f.EnvVar)
		}
	}
}

// BenchmarkApplyEnv_SomeSet measures applying env overrides when a handful
// of MAGUS_* vars are set.
func BenchmarkApplyEnv_SomeSet(b *testing.B) {
	b.Setenv("MAGUS_CACHE_DIR", "/tmp/bench-cache")
	b.Setenv("MAGUS_CONCURRENCY", "4")
	b.Setenv("MAGUS_DRY_RUN", "true")
	b.Setenv("MAGUS_CACHE_SIZE_MB", "512")
	b.Setenv("MAGUS_LOG_FORMAT", "json")
	b.Setenv("MAGUS_FLAKE_ENABLED", "false")
	b.Setenv("MAGUS_CI_MAX_SHARDS", "8")
	b.Setenv("MAGUS_GRAPH_DIRECTION", "upstream")
	b.Setenv("MAGUS_TELEMETRY_ENABLED", "true")
	b.Setenv("MAGUS_TELEMETRY_ENDPOINT", "localhost:4317")

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, f := range Fields {
			_ = os.Getenv(f.EnvVar)
		}
	}
}

// BenchmarkParseBool measures the string→bool conversion used by ApplyEnv.
func BenchmarkParseBool_true(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = ParseBool("true", false)
	}
}

func BenchmarkParseBool_false(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = ParseBool("false", true)
	}
}

func BenchmarkParseBool_fallback(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = ParseBool("", false)
	}
}

// BenchmarkField_String measures the String formatter.
func BenchmarkField_String(b *testing.B) {
	f, _ := FieldByEnv("MAGUS_CACHE_DIR")
	b.ReportAllocs()
	for b.Loop() {
		_ = f.String()
	}
}

// BenchmarkFieldByGoPath measures the GoPath index lookup.
func BenchmarkFieldByGoPath(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = FieldByGoPath("Cache.Dir")
	}
}

// BenchmarkFieldIteration measures a full pass over all Fields (simulates doctor's
// KnownEnvVars construction or a tool that enumerates all config fields).
func BenchmarkFieldIteration(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var n int
		for _, f := range Fields {
			if strings.HasPrefix(f.EnvVar, "MAGUS_") {
				n++
			}
		}
		_ = n
	}
}

// BenchmarkFieldIteration_reflect is the control: use reflect to walk the
// schema.Fields slice with field-by-field comparison.
func BenchmarkFieldIteration_reflect(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		rv := reflect.ValueOf(Fields)
		n := rv.Len()
		for i := 0; i < n; i++ {
			ev := rv.Index(i).FieldByName("EnvVar").String()
			_ = strings.HasPrefix(ev, "MAGUS_")
		}
	}
}
