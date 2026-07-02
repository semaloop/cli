package main

import (
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// TestBuildPushGitFlagsAllOrNone verifies the `and:"gitref"` group makes Kong
// accept all three git flags together or none, and reject any partial set at
// parse time.
func TestBuildPushGitFlagsAllOrNone(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"none", []string{"build", "push", "App.app"}, false},
		{"all three", []string{"build", "push", "App.app", "--git-repo=owner/name", "--git-commit=abc123", "--git-ref=refs/heads/main"}, false},
		{"repo only", []string{"build", "push", "App.app", "--git-repo=owner/name"}, true},
		{"missing ref", []string{"build", "push", "App.app", "--git-repo=owner/name", "--git-commit=abc123"}, true},
		{"missing repo", []string{"build", "push", "App.app", "--git-commit=abc123", "--git-ref=refs/heads/main"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cli CLI
			parser, err := kong.New(&cli, kong.Vars{"version": "test"}, kong.Exit(func(int) {}))
			if err != nil {
				t.Fatalf("could not build parser: %v", err)
			}
			_, err = parser.Parse(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("args=%v: wantErr=%v, got %v", tt.args, tt.wantErr, err)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), "must be used together") {
				t.Errorf("expected 'must be used together' error, got %v", err)
			}
		})
	}
}

// TestBuildPushAllowDuplicateVersionFlag verifies --allow-duplicate-version
// defaults to false and is set to true when passed.
func TestBuildPushAllowDuplicateVersionFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"default", []string{"build", "push", "App.app"}, false},
		{"flag set", []string{"build", "push", "App.app", "--allow-duplicate-version"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cli CLI
			parser, err := kong.New(&cli, kong.Vars{"version": "test"}, kong.Exit(func(int) {}))
			if err != nil {
				t.Fatalf("could not build parser: %v", err)
			}
			if _, err := parser.Parse(tt.args); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cli.Build.Push.AllowDuplicateVersion != tt.want {
				t.Errorf("args=%v: want AllowDuplicateVersion=%v, got %v", tt.args, tt.want, cli.Build.Push.AllowDuplicateVersion)
			}
		})
	}
}
