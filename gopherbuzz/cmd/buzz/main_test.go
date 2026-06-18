package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArgs(t *testing.T) {
	t.Run("bare file", func(t *testing.T) {
		got, err := parseArgs([]string{"x.buzz"})
		require.NoError(t, err)
		assert.Equal(t, opts{args: []string{"x.buzz"}}, got)
	})
	t.Run("file then script args", func(t *testing.T) {
		got, err := parseArgs([]string{"x.buzz", "a", "b"})
		require.NoError(t, err)
		assert.Equal(t, opts{args: []string{"x.buzz", "a", "b"}}, got)
	})
	t.Run("check long", func(t *testing.T) {
		got, err := parseArgs([]string{"--check", "x.buzz"})
		require.NoError(t, err)
		assert.Equal(t, opts{check: true, args: []string{"x.buzz"}}, got)
	})
	t.Run("check short", func(t *testing.T) {
		got, err := parseArgs([]string{"-c", "x.buzz"})
		require.NoError(t, err)
		assert.Equal(t, opts{check: true, args: []string{"x.buzz"}}, got)
	})
	t.Run("test short", func(t *testing.T) {
		got, err := parseArgs([]string{"-t", "x.buzz"})
		require.NoError(t, err)
		assert.Equal(t, opts{test: true, args: []string{"x.buzz"}}, got)
	})
	t.Run("test long", func(t *testing.T) {
		got, err := parseArgs([]string{"--test", "x.buzz"})
		require.NoError(t, err)
		assert.Equal(t, opts{test: true, args: []string{"x.buzz"}}, got)
	})
	t.Run("eval", func(t *testing.T) {
		got, err := parseArgs([]string{"-e", "code"})
		require.NoError(t, err)
		assert.Equal(t, opts{eval: "code"}, got)
	})
	t.Run("eval equals", func(t *testing.T) {
		got, err := parseArgs([]string{"--eval=code"})
		require.NoError(t, err)
		assert.Equal(t, opts{eval: "code"}, got)
	})
	t.Run("ast", func(t *testing.T) {
		got, err := parseArgs([]string{"--ast", "x.buzz"})
		require.NoError(t, err)
		assert.Equal(t, opts{dumpAST: true, args: []string{"x.buzz"}}, got)
	})
	t.Run("version", func(t *testing.T) {
		got, err := parseArgs([]string{"-v"})
		require.NoError(t, err)
		assert.Equal(t, opts{showVer: true}, got)
	})
	t.Run("help", func(t *testing.T) {
		got, err := parseArgs([]string{"--help"})
		require.NoError(t, err)
		assert.Equal(t, opts{showHelp: true}, got)
	})
	t.Run("repeatable -L", func(t *testing.T) {
		got, err := parseArgs([]string{"-L", "a", "-L", "b", "x.buzz"})
		require.NoError(t, err)
		assert.Equal(t, opts{libDirs: []string{"a", "b"}, args: []string{"x.buzz"}}, got)
	})
	t.Run("stdin dash", func(t *testing.T) {
		got, err := parseArgs([]string{"-"})
		require.NoError(t, err)
		assert.Equal(t, opts{args: []string{"-"}}, got)
	})
	t.Run("options stop at script", func(t *testing.T) {
		got, err := parseArgs([]string{"x.buzz", "-c"})
		require.NoError(t, err)
		assert.Equal(t, opts{args: []string{"x.buzz", "-c"}}, got)
	})
	t.Run("double dash ends options", func(t *testing.T) {
		got, err := parseArgs([]string{"--", "-c"})
		require.NoError(t, err)
		assert.Equal(t, opts{args: []string{"-c"}}, got)
	})
}

func TestParseArgsErrors(t *testing.T) {
	for _, argv := range [][]string{
		{"--bogus"},
		{"-e"},             // missing value
		{"--library-path"}, // missing value
	} {
		_, err := parseArgs(argv)
		assert.Errorf(t, err, "parseArgs(%v): want error, got nil", argv)
	}
}

func TestSourceRejectsEvalWithFile(t *testing.T) {
	_, _, err := source(opts{eval: "x", args: []string{"f.buzz"}})
	assert.Error(t, err, "source(-e + file): want error")
}
