package testfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseAllFields(t *testing.T) {
	src := `---
identifier: sign-in
name: Sign in with email
folder: Smoke
draft: true
tags: [smoke, regression]
secrets:
  - test-account
disabled_capabilities: [audio_input]
take_over_test_id: 01JABC
---

Open the app and sign in.

Check the home screen.
`
	got, errs := Parse("sign-in.md", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	draft := true
	want := Test{
		Path:                 "sign-in.md",
		Identifier:           "sign-in",
		Name:                 "Sign in with email",
		Description:          "Open the app and sign in.\n\nCheck the home screen.",
		Folder:               "Smoke",
		Draft:                &draft,
		Tags:                 []string{"smoke", "regression"},
		Secrets:              []string{"test-account"},
		DisabledCapabilities: []string{"audio_input"},
		TakeOverTestID:       "01JABC",
		identifierLine:       2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseOptionalFieldsLeftOut(t *testing.T) {
	got, errs := Parse("t.md", []byte("---\nidentifier: a\nname: A\n---\nDo it.\n"))
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got.Draft != nil || got.Tags != nil || got.Secrets != nil || got.DisabledCapabilities != nil || got.Folder != "" {
		t.Errorf("expected optional fields to be unset, got %+v", got)
	}
}

// An empty secrets list must stay distinct from a missing one: the API keeps
// a test's secrets when the field is left out, and clears them when it's empty.
func TestParseEmptySecretsIsNotNil(t *testing.T) {
	got, errs := Parse("t.md", []byte("---\nidentifier: a\nname: A\nsecrets: []\n---\nDo it.\n"))
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got.Secrets == nil {
		t.Error("expected empty, non-nil secrets")
	}
}

func TestParseNumericIdentifierIsText(t *testing.T) {
	got, errs := Parse("t.md", []byte("---\nidentifier: 404\nname: Not found\n---\nDo it.\n"))
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got.Identifier != "404" {
		t.Errorf("expected identifier 404, got %q", got.Identifier)
	}
}

func TestParseCRLFAndBOM(t *testing.T) {
	src := "\xef\xbb\xbf---\r\nidentifier: a\r\nname: A\r\n---\r\nDo it.\r\n"
	got, errs := Parse("t.md", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got.Description != "Do it." {
		t.Errorf("unexpected description %q", got.Description)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "no frontmatter",
			src:  "Just a test.\n",
			want: []string{"t.md:1: missing frontmatter"},
		},
		{
			name: "indented opening fence",
			src:  "  ---\nidentifier: a\nname: A\n---\nDo it.\n",
			want: []string{"t.md:1: missing frontmatter"},
		},
		{
			name: "indented closing fence",
			src:  "---\nidentifier: a\nname: A\n  ---\nDo it.\n",
			want: []string{"t.md:1: frontmatter is never closed"},
		},
		{
			name: "duplicate tags",
			src:  "---\nidentifier: a\nname: A\ntags: [smoke, smoke, Login, login]\n---\nDo it.\n",
			want: []string{
				`t.md:4: tag "smoke" is listed more than once`,
				`t.md:4: tags "Login" and "login" are the same tag: tags ignore case`,
			},
		},
		{
			name: "unclosed frontmatter",
			src:  "---\nidentifier: a\nname: A\n",
			want: []string{"t.md:1: frontmatter is never closed"},
		},
		{
			name: "invalid yaml",
			src:  "---\nidentifier: a\nname: [A\n---\nDo it.\n",
			want: []string{"invalid frontmatter"},
		},
		{
			name: "not a mapping",
			src:  "---\n- a\n- b\n---\nDo it.\n",
			want: []string{"t.md:2: frontmatter must be a list of settings"},
		},
		{
			name: "missing required fields and body",
			src:  "---\nfolder: Smoke\n---\n\n",
			want: []string{
				"t.md:1: missing identifier",
				"t.md:1: missing name",
				"t.md:3: missing test",
			},
		},
		{
			name: "empty name",
			src:  "---\nidentifier: a\nname: \"  \"\n---\nDo it.\n",
			want: []string{"t.md:3: name must not be empty"},
		},
		{
			name: "old id field",
			src:  "---\nid: a\nname: A\n---\nDo it.\n",
			want: []string{
				"t.md:1: missing identifier",
				"t.md:2: unknown field id: use identifier instead",
			},
		},
		{
			name: "misspelt fields",
			src:  "---\nidentifier: a\nname: A\ntag: [x]\nsecret: [y]\ndisabledCapabilities: [z]\ntake-over-test-id: t\n---\nDo it.\n",
			want: []string{
				"t.md:4: unknown field tag: did you mean tags?",
				"t.md:5: unknown field secret: did you mean secrets?",
				"t.md:6: unknown field disabledCapabilities: did you mean disabled_capabilities?",
				"t.md:7: unknown field take-over-test-id: did you mean take_over_test_id?",
			},
		},
		{
			name: "unrelated field",
			src:  "---\nidentifier: a\nname: A\npriority: high\n---\nDo it.\n",
			want: []string{"t.md:4: unknown field priority: expected one of identifier, name"},
		},
		{
			name: "description in frontmatter",
			src:  "---\nidentifier: a\nname: A\ndescription: Do it.\n---\nDo it.\n",
			want: []string{"t.md:4: unknown field description: write the test below the frontmatter"},
		},
		{
			name: "duplicate key",
			src:  "---\nidentifier: a\nname: A\nname: B\n---\nDo it.\n",
			want: []string{"t.md:4: name is set more than once (first on line 3)"},
		},
		{
			name: "wrong types",
			src:  "---\nidentifier: [a]\nname: A\ndraft: maybe\ntags: smoke\nsecrets: [\"\", ok]\nfolder: true\n---\nDo it.\n",
			want: []string{
				"t.md:2: identifier must be text",
				"t.md:4: draft must be true or false",
				"t.md:5: tags must be a list",
				"t.md:6: each item in secrets must be non-empty text",
				"t.md:7: folder must be text",
			},
		},
		{
			name: "tag too long",
			src:  "---\nidentifier: a\nname: A\ntags: [" + strings.Repeat("x", MaxTagLength+1) + "]\n---\nDo it.\n",
			want: []string{"t.md:4: tag \"xxxx"},
		},
		{
			name: "too many tags",
			src:  "---\nidentifier: a\nname: A\ntags: [" + manyTags(MaxTags+1) + "]\n---\nDo it.\n",
			want: []string{"t.md:4: a test can have at most 20 tags, but this one has 21"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := Parse("t.md", []byte(tt.src))
			if len(errs) != len(tt.want) {
				t.Fatalf("expected %d errors, got %d: %v", len(tt.want), len(errs), errs)
			}
			for i, w := range tt.want {
				if got := errs[i].Error(); !strings.Contains(got, w) {
					t.Errorf("error %d: expected %q to contain %q", i, got, w)
				}
			}
		})
	}
}

func manyTags(n int) string {
	tags := make([]string, n)
	for i := range tags {
		tags[i] = "t" + string(rune('a'+i))
	}
	return strings.Join(tags, ", ")
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "b.md", "---\nidentifier: b\nname: B\n---\nDo B.\n")
	write(t, dir, "smoke/a.md", "---\nidentifier: a\nname: A\n---\nDo A.\n")
	write(t, dir, "README.txt", "not a test")

	tests, errs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	var ids []string
	for _, tt := range tests {
		ids = append(ids, tt.Identifier)
	}
	if !reflect.DeepEqual(ids, []string{"b", "a"}) {
		t.Errorf("expected tests b, a in lexical path order, got %v", ids)
	}
	if want := filepath.Join(dir, "smoke", "a.md"); tests[1].Path != want {
		t.Errorf("expected path %q, got %q", want, tests[1].Path)
	}
}

func TestLoadReportsEveryErrorSorted(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.md", "---\nid: x\nidentifier: dup\nname: A\n---\n")
	write(t, dir, "b.md", "---\nidentifier: dup\nname: B\ntag: [x]\n---\nDo B.\n")
	write(t, dir, "c.md", "---\nidentifier: c\nname: C\n---\nDo C.\n")

	tests, errs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")
	want := []string{
		a + ":2: unknown field id: use identifier instead",
		a + ":5: missing test: write the test in natural language below the frontmatter",
		b + ":2: identifier \"dup\" is already used by " + a,
		b + ":4: unknown field tag: did you mean tags?",
	}
	var got []string
	for _, e := range errs {
		got = append(got, e.Error())
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(tests) != 1 || tests[0].Identifier != "c" {
		t.Errorf("expected only c to load, got %+v", tests)
	}
}

func TestLoadEmptyDirectory(t *testing.T) {
	tests, errs, err := Load(t.TempDir())
	if err != nil || len(errs) > 0 || len(tests) > 0 {
		t.Errorf("expected nothing, got tests=%v errs=%v err=%v", tests, errs, err)
	}
}

func TestLoadMissingDirectory(t *testing.T) {
	_, _, err := Load(filepath.Join(t.TempDir(), "nope"))
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected a does-not-exist error, got %v", err)
	}
}

func TestLoadFileInsteadOfDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.md", "")
	_, _, err := Load(filepath.Join(dir, "a.md"))
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("expected a not-a-directory error, got %v", err)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
