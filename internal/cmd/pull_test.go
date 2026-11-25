package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/semaloop/cli/internal/testfile"
)

func paths(files []PulledFile) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func TestPlanPullWritesEverySetting(t *testing.T) {
	dir := t.TempDir()
	plan := PlanPull(dir, []DashboardTest{{
		ID:                   "01JABC",
		Name:                 "Sign in with email",
		Description:          "Sign in.",
		Folder:               "Smoke tests",
		Draft:                true,
		Tags:                 []string{"smoke"},
		Secrets:              []string{"test-account"},
		DisabledCapabilities: []string{"audio_input"},
	}}, nil, PullOptions{})
	if len(plan.Files) != 1 {
		t.Fatalf("expected one file, got %+v", plan)
	}
	if err := WritePull(plan); err != nil {
		t.Fatal(err)
	}

	tests, errs, err := testfile.Load(dir)
	if err != nil || len(errs) > 0 {
		t.Fatalf("pulled files don't load: %v %v", err, errs)
	}
	draft := true
	want := testfile.Test{
		Path:                 filepath.Join(dir, "smoke-tests", "sign-in-with-email.md"),
		Identifier:           "sign-in-with-email",
		Name:                 "Sign in with email",
		Description:          "Sign in.",
		Folder:               "Smoke tests",
		Draft:                &draft,
		Tags:                 []string{"smoke"},
		Secrets:              []string{"test-account"},
		DisabledCapabilities: []string{"audio_input"},
		TakeOverTestID:       "01JABC",
	}
	got := tests[0]
	if got.Path != want.Path || got.Identifier != want.Identifier || got.TakeOverTestID != want.TakeOverTestID ||
		!reflect.DeepEqual(got.Secrets, want.Secrets) || !reflect.DeepEqual(got.Tags, want.Tags) ||
		!reflect.DeepEqual(got.DisabledCapabilities, want.DisabledCapabilities) || *got.Draft != true ||
		got.Folder != want.Folder || got.Description != want.Description {
		t.Errorf("got:\n%#v\nwant:\n%#v", got, want)
	}
}

// Secrets are written even when a test has none, so the file is exact.
func TestPlanPullWritesEmptySecrets(t *testing.T) {
	plan := PlanPull(t.TempDir(), []DashboardTest{{ID: "1", Name: "A", Description: "Do A."}}, nil, PullOptions{})
	if s := plan.Files[0].Test.Secrets; s == nil || len(s) != 0 {
		t.Errorf("expected empty secrets, got %#v", s)
	}
}

func TestPlanPullMakesIdentifiersUnique(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "checkout-3.md"), []byte("not a test file yet"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := []testfile.Test{{Path: filepath.Join(dir, "other.md"), Identifier: "checkout"}}
	remote := []DashboardTest{
		{ID: "a", Name: "Checkout"},
		{ID: "b", Name: "Checkout"},
		{ID: "c", Name: "Checkout!"},
		{ID: "d", Name: "Checkout", Folder: "Smoke"},
		{ID: "e", Name: "Sign in"},
		{ID: "f", Name: "Sign in", ExternalID: "sign-in"},
		{ID: "g", Name: "☕"},
	}
	got := paths(PlanPull(dir, remote, local, PullOptions{}).Files)
	want := []string{
		filepath.Join(dir, "checkout-2.md"),
		filepath.Join(dir, "checkout-4.md"),
		filepath.Join(dir, "checkout-5.md"),
		filepath.Join(dir, "sign-in-2.md"),
		filepath.Join(dir, "test.md"),
		filepath.Join(dir, "smoke", "checkout-3.md"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPlanPullSkipsTestsWithAFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mine.md")
	local := []testfile.Test{{Path: path, Identifier: "my-checkout", TakeOverTestID: "a"}}
	remote := []DashboardTest{{ID: "a", Name: "Checkout", Description: "New spec."}}

	plan := PlanPull(dir, remote, local, PullOptions{})
	if len(plan.Files) != 0 || len(plan.Skipped) != 1 {
		t.Fatalf("expected the test to be skipped, got %+v", plan)
	}

	plan = PlanPull(dir, remote, local, PullOptions{Force: true})
	if len(plan.Files) != 1 {
		t.Fatalf("expected one file, got %+v", plan)
	}
	f := plan.Files[0]
	if f.Path != path || f.Test.Identifier != "my-checkout" || !f.Replaces {
		t.Errorf("expected the existing file to be rewritten, got %+v", f)
	}
}

func TestPlanPullFilters(t *testing.T) {
	dir := t.TempDir()
	remote := []DashboardTest{
		{ID: "a", Name: "A", Folder: "Smoke"},
		{ID: "b", Name: "B", Folder: "Checkout"},
		{ID: "c", Name: "C"},
	}
	if got := paths(PlanPull(dir, remote, nil, PullOptions{Folders: []string{"Smoke"}}).Files); len(got) != 1 || filepath.Base(got[0]) != "a.md" {
		t.Errorf("folder filter: got %q", got)
	}
	if got := paths(PlanPull(dir, remote, nil, PullOptions{TestIDs: []string{"c", "b"}}).Files); len(got) != 2 {
		t.Errorf("test filter: got %q", got)
	}
}

func TestWritePullNeverReplacesAnotherFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	if err := os.WriteFile(path, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WritePull(PullPlan{Files: []PulledFile{{Path: path, Test: testfile.Test{Identifier: "a", Name: "A", Description: "A."}}}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if data, _ := os.ReadFile(path); string(data) != "mine" {
		t.Errorf("file was replaced: %q", data)
	}
}
