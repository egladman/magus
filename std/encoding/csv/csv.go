// Package csv is the "encoding/csv" host module. See std/encoding/register.go
// for why this and its eight siblings each get their own leaf package instead
// of living in std's flat root, and how this directory's Module reaches the
// rest of magus without std importing back down to collect it.
package csv

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/egladman/magus/std"
)

//go:generate go run ../../../cmd/magus-utils bindings -module csv -lang buzz -out ../../../internal/interp/bindings/gen/csv.go

// Module is the "encoding/csv" host module: delimiter-separated tabular text, in and
// out. Nothing in Buzz or in magus could read a table before this - a magusfile
// handed a coverage export, a dependency inventory, or any tool's --format=csv
// output had to split on commas by hand, which is wrong the moment a field is
// quoted or contains the delimiter.
//
// TSV is this module with delimiter set to a tab rather than a separate module:
// the format differs by one character and nothing else, so a second name would be
// two things to learn for one behaviour.
//
// The options are ORDINARY OPTIONAL ARGUMENTS, not an opts map. Most of the
// stdlib's older methods take {str: any} bags, which means a misspelled key is
// silent; a declared argument is checked. New surface should not add to that.
var Module = std.Module{
	Name: "csv",
	Path: "encoding/csv",
	Doc:  "Delimiter-separated tabular text (CSV, TSV) parsing and rendering.",
	Methods: []std.Method{
		{
			Name: "parse",
			Doc:  "Parse delimiter-separated text into a list of rows, each a list of fields. Quoted fields, embedded delimiters, and embedded newlines are handled per RFC 4180. delimiter defaults to \",\" - pass \"\\t\" for TSV. When comment is a single character, lines starting with it are skipped. Raises when a row has a different field count than the first, which is the corruption a hand-rolled split would silently pass through.",
			Args: []std.Arg{
				{Name: "s", Type: std.TypeString},
				{Name: "delimiter", Type: std.TypeString, Optional: true, Default: ","},
				{Name: "comment", Type: std.TypeString, Optional: true},
			},
			Returns: []std.Ret{{Type: std.TypeStringSliceSlice}},
			Raises:  true,
			Impl:    CSVParse,
		},
		{
			Name: "stringify",
			Doc:  "Render rows (a list of lists of fields) as delimiter-separated text, quoting any field that needs it. delimiter defaults to \",\". The output ends with a newline, so it is ready to write.",
			Args: []std.Arg{
				{Name: "rows", Type: std.TypeStringSliceSlice},
				{Name: "delimiter", Type: std.TypeString, Optional: true, Default: ","},
			},
			Returns: []std.Ret{{Type: std.TypeString}},
			Raises:  true,
			Impl:    CSVStringify,
		},
	},
}

// csvDelimiter resolves the delimiter argument to the single rune encoding/csv
// wants. An empty value means the caller omitted it.
//
// Multi-character delimiters are rejected rather than truncated: a caller passing
// "||" means it, and silently reading only the first character would split on
// every single pipe instead.
func csvDelimiter(delimiter string) (rune, error) {
	if delimiter == "" {
		return ',', nil
	}
	r := []rune(delimiter)
	if len(r) != 1 {
		return 0, fmt.Errorf("csv: delimiter must be exactly one character, got %q", delimiter)
	}
	return r[0], nil
}

// CSVParse parses delimiter-separated text into rows of fields.
func CSVParse(_ context.Context, s, delimiter, comment string) ([][]string, error) {
	comma, err := csvDelimiter(delimiter)
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(strings.NewReader(s))
	r.Comma = comma
	if comment != "" {
		c := []rune(comment)
		if len(c) != 1 {
			return nil, fmt.Errorf("csv.parse: comment must be exactly one character, got %q", comment)
		}
		r.Comment = c[0]
	}
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv.parse: %w", err)
	}
	if records == nil {
		records = [][]string{}
	}
	return records, nil
}

// CSVStringify renders rows as delimiter-separated text.
func CSVStringify(_ context.Context, rows [][]string, delimiter string) (string, error) {
	comma, err := csvDelimiter(delimiter)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Comma = comma
	if err := w.WriteAll(rows); err != nil {
		return "", fmt.Errorf("csv.stringify: %w", err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("csv.stringify: %w", err)
	}
	return b.String(), nil
}
