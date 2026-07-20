package main

import (
	"flag"
	"io"
	"reflect"
	"testing"
)

// TestParseFlagsAllowingLeadingPositionals proves a command's positional(s) may
// appear before OR after its flags — the fix for the flags-stop-at-first-
// positional trap that made `swartznet files <ih> --api-addr X` and `create
// <path> -o out` fail (the latter matching the command's own usage string).
func TestParseFlagsAllowingLeadingPositionals(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantPos []string
		wantAPI string
		wantB   bool
	}{
		{"flags first", []string{"--api-addr", "h:1", "--flag", "ih"}, []string{"ih"}, "h:1", true},
		{"positional first", []string{"ih", "--api-addr", "h:1", "--flag"}, []string{"ih"}, "h:1", true},
		{"positional only", []string{"ih"}, []string{"ih"}, "def", false},
		{"three positionals then flag", []string{"ih", "2", "high", "--api-addr", "h:2"}, []string{"ih", "2", "high"}, "h:2", false},
		{"flag then positional", []string{"--api-addr", "h:3", "ih"}, []string{"ih"}, "h:3", false},
		{"no args", []string{}, []string{}, "def", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			api := fs.String("api-addr", "def", "")
			b := fs.Bool("flag", false, "")
			pos, err := parseFlagsAllowingLeadingPositionals(fs, tc.args)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !reflect.DeepEqual(pos, tc.wantPos) {
				t.Errorf("positionals = %v, want %v", pos, tc.wantPos)
			}
			if *api != tc.wantAPI {
				t.Errorf("--api-addr = %q, want %q", *api, tc.wantAPI)
			}
			if *b != tc.wantB {
				t.Errorf("--flag = %v, want %v", *b, tc.wantB)
			}
		})
	}
}

// TestParseFlagsRejectsUnknownFlag confirms a genuinely bad flag still errors
// (the helper must not swallow parse failures).
func TestParseFlagsRejectsUnknownFlag(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("api-addr", "def", "")
	if _, err := parseFlagsAllowingLeadingPositionals(fs, []string{"ih", "--nope"}); err == nil {
		t.Error("unknown flag accepted")
	}
}
