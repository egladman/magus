package std

import (
	"context"
	"reflect"
	"testing"
)

func TestStringsCase(t *testing.T) {
	ctx := context.Background()
	const in = "hello_world-test case"
	cases := []struct {
		name string
		fn   func(context.Context, string) (string, error)
		want string
	}{
		{"camel", StringsCamelCase, "helloWorldTestCase"},
		{"snake", StringsSnakeCase, "hello_world_test_case"},
		{"kebab", StringsKebabCase, "hello-world-test-case"},
		{"pascal", StringsPascalCase, "HelloWorldTestCase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("%s_case(%q) = %q, want %q", tc.name, in, got, tc.want)
			}
		})
	}
}

func TestStringsCapitalize(t *testing.T) {
	got, err := StringsCapitalize(context.Background(), "hELLO")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Hello"; got != want {
		t.Fatalf("capitalize = %q, want %q", got, want)
	}
}

func TestStringsWords(t *testing.T) {
	got, err := StringsWords(context.Background(), "fooBarBaz")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"foo", "Bar", "Baz"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("words = %v, want %v", got, want)
	}
}

func TestStringsEllipsis(t *testing.T) {
	got, err := StringsEllipsis(context.Background(), "abcdefgh", 5)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ab..."; got != want {
		t.Fatalf("ellipsis = %q, want %q", got, want)
	}
}
