package main

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
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
		{"bash", "complete -C semaloop semaloop"},
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
			if strings.Contains(out, "/semaloop") {
				t.Errorf("want script without absolute paths, got:\n%s", out)
			}
		})
	}
}

// TestCompletionShellsMatchScripts keeps the enum tag, which Kong uses to
// validate and complete the shell argument, in step with completionScripts.
func TestCompletionShellsMatchScripts(t *testing.T) {
	field, _ := reflect.TypeFor[CompletionCmd]().FieldByName("Shell")
	enum := strings.Split(field.Tag.Get("enum"), ",")
	slices.Sort(enum)
	scripts := slices.Sorted(maps.Keys(completionScripts))
	if !slices.Equal(enum, scripts) {
		t.Errorf("enum tag lists %v, but completionScripts has %v", enum, scripts)
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
	dir := t.TempDir()
	for _, name := range []string{"MyApp.ipa", "MyApp.apk"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "MyTests"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	tests := []struct {
		line string
		want []string
	}{
		{"semaloop ", []string{"auth", "build", "completion", "test"}},
		{"semaloop test ", []string{"check", "diff", "list", "pull", "sync"}},
		{"semaloop test sync --allow", []string{"--allow-archive-all"}},
		{"semaloop auth lo", []string{"login", "logout"}},
		{"semaloop completion ", []string{"bash", "fish", "zsh"}},
		{"semaloop build push --git-r", []string{"--git-ref", "--git-repo"}},
		// Paths complete only where a path is expected.
		{"semaloop build push My", []string{"MyApp.apk", "MyApp.ipa", "MyTests/"}},
		{"semaloop test check My", []string{"MyTests/"}},
		{"semaloop completion zsh ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			t.Setenv("COMP_LINE", tt.line)
			var stdout bytes.Buffer
			parser := newTestParser(t, &stdout)
			exited := false
			kongcompletion.Register(parser,
				kongcompletion.WithPredictors(completionPredictors),
				kongcompletion.WithExitFunc(func(int) { exited = true }),
			)
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
