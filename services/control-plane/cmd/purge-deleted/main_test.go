package main

import "testing"

func TestParseApplyModeDefaultsToDryRun(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		apply   bool
		wantErr bool
	}{
		{name: "default", apply: false},
		{name: "explicit dry run", args: []string{"--dry-run"}, apply: false},
		{name: "apply", args: []string{"--apply"}, apply: true},
		{name: "conflicting flags", args: []string{"--apply", "--dry-run"}, wantErr: true},
		{name: "positional argument", args: []string{"extra"}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			apply, err := parseApplyMode(test.args)
			if (err != nil) != test.wantErr || apply != test.apply {
				t.Fatalf("parseApplyMode(%v) = %v, %v", test.args, apply, err)
			}
		})
	}
}
