package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	kongcompletion "github.com/jotaen/kong-completion"
)

func newTestParser(t *testing.T, stdout *bytes.Buffer) *kong.Kong {
	t.Helper()
	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("semaloop"),
		kong.Vars{"version": "test"},
		kong.Writers(stdout, &bytes.Buffer{}),
		kong.Exit(func(int) {}),
	)
	if err != nil {
		t.Fatalf("could not build parser: %v", err)
	}
	return parser
}

// TestCompletionPrintsScript verifies each supported shell gets a script that
// calls `semaloop` by name, not by an absolute path that breaks when the
// binary moves (e.g. across `brew upgrade`).
func TestCompletionPrintsScript(t *testing.T) {
	tests := []struct {
		shell string
		want  string
	}{
		{"bash", "complete -o default -o bashdefault -C semaloop semaloop"},
		{"zsh", "#compdef semaloop"},
		{"fish", `complete -c semaloop -f -a "(__complete_semaloop)"`},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			var stdout bytes.Buffer
			parser := newTestParser(t, &stdout)
			ctx, err := parser.Parse([]string{"completion", tt.shell})
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if err := ctx.Run(); err != nil {
				t.Fatalf("unexpected run error: %v", err)
			}
			out := stdout.String()
			if !strings.Contains(out, tt.want) {
				t.Errorf("want output to contain %q, got:\n%s", tt.want, out)
			}
			if strings.Contains(out, "{{") || strings.Contains(out, "/semaloop") {
				t.Errorf("want script without placeholders or absolute paths, got:\n%s", out)
			}
		})
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	for _, args := range [][]string{{"completion"}, {"completion", "tcsh"}} {
		parser := newTestParser(t, &bytes.Buffer{})
		if _, err := parser.Parse(args); err == nil {
			t.Errorf("args=%v: expected a parse error", args)
		}
	}
}

// TestCompletionRegisterAnswersTab simulates the shell invoking `semaloop`
// on Tab, and checks the candidates come from the Kong model.
func TestCompletionRegisterAnswersTab(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{"semaloop ", []string{"auth", "build", "completion"}},
		{"semaloop auth lo", []string{"login", "logout"}},
		{"semaloop completion ", []string{"bash", "fish", "zsh"}},
		{"semaloop build push --git-r", []string{"--git-ref", "--git-repo"}},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			t.Setenv("COMP_LINE", tt.line)
			var stdout bytes.Buffer
			parser := newTestParser(t, &stdout)
			exited := false
			kongcompletion.Register(parser, kongcompletion.WithExitFunc(func(int) { exited = true }))
			if !exited {
				t.Fatal("expected Register to handle the completion request and exit")
			}
			got := strings.Fields(stdout.String())
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("want %v, got %v", tt.want, got)
			}
		})
	}
}
