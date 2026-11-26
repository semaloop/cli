package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/semaloop/cli/internal/testfile"
)

func paths(files []PulledFile) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func mustPlan(t *testing.T, dir string, remote []DashboardTest, local []testfile.Test, opts PullOptions) PullPlan {
	t.Helper()
	plan, err := PlanPull(dir, remote, local, opts)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPlanPullWritesEverySetting(t *testing.T) {
	dir := t.TempDir()
	plan := mustPlan(t, dir, []DashboardTest{{
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
	plan := mustPlan(t, t.TempDir(), []DashboardTest{{ID: "1", Name: "A", Description: "Do A."}}, nil, PullOptions{})
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
	got := paths(mustPlan(t, dir, remote, local, PullOptions{}).Files)
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
	if err := os.WriteFile(path, []byte("---\nidentifier: my-checkout\nname: Checkout\ntake_over_test_id: a\n---\n\nOld spec.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := []testfile.Test{{Path: path, Identifier: "my-checkout", TakeOverTestID: "a"}}
	remote := []DashboardTest{{ID: "a", Name: "Checkout", Description: "New spec."}}

	plan := mustPlan(t, dir, remote, local, PullOptions{})
	if len(plan.Files) != 0 || len(plan.Skipped) != 1 {
		t.Fatalf("expected the test to be skipped, got %+v", plan)
	}

	plan = mustPlan(t, dir, remote, local, PullOptions{Force: true})
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
	if got := paths(mustPlan(t, dir, remote, nil, PullOptions{Folders: []string{"Smoke"}}).Files); len(got) != 1 || filepath.Base(got[0]) != "a.md" {
		t.Errorf("folder filter: got %q", got)
	}
	if got := paths(mustPlan(t, dir, remote, nil, PullOptions{TestIDs: []string{"c", "b"}}).Files); len(got) != 2 {
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

// A file where a folder's directory should be can't be avoided by another
// file name, so planning fails instead of trying names forever.
func TestPlanPullFileInTheWayOfFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "smoke"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := PlanPull(dir, []DashboardTest{{ID: "a", Name: "Checkout", Folder: "Smoke"}}, nil, PullOptions{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PlanPull didn't return")
	}
}

const pulledA = "---\nidentifier: a\nname: A\ntake_over_test_id: test-a\n---\n\nOld spec.\n"

// forcePlan writes test-a's file at dir/a.md and plans refreshing it.
func forcePlan(t *testing.T) (string, PullPlan) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	if err := os.WriteFile(path, []byte(pulledA), 0o644); err != nil {
		t.Fatal(err)
	}
	local, errs, err := testfile.Load(dir)
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	plan := mustPlan(t, dir, []DashboardTest{{ID: "test-a", Name: "A", Description: "New spec."}}, local, PullOptions{Force: true})
	if len(plan.Files) != 1 || !plan.Files[0].Replaces {
		t.Fatalf("expected a.md to be replaced, got %+v", plan)
	}
	return path, plan
}

func TestWritePullForceReplacesTheTestsFile(t *testing.T) {
	path, plan := forcePlan(t)
	if err := WritePull(plan); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if test, errs := testfile.Parse(path, data); len(errs) > 0 || test.Description != "New spec." || test.TakeOverTestID != "test-a" {
		t.Errorf("file wasn't refreshed: %q", data)
	}
}

// A file that's changed since the pull was planned isn't replaced, even if
// it's a valid test file.
func TestWritePullForceRejectsChangedFile(t *testing.T) {
	path, plan := forcePlan(t)
	other := "---\nidentifier: b\nname: B\n---\n\nSomething else.\n"
	if err := os.WriteFile(path, []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WritePull(plan); !errors.Is(err, ErrPullConflict) {
		t.Fatalf("expected a conflict, got %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != other {
		t.Errorf("file was replaced: %q", data)
	}
}

// A file swapped for a link isn't written through, even when the link's
// target has the planned contents.
func TestWritePullForceRejectsLink(t *testing.T) {
	path, plan := forcePlan(t)
	target := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := os.WriteFile(target, []byte(pulledA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skip("can't make links:", err)
	}
	if err := WritePull(plan); !errors.Is(err, ErrPullConflict) {
		t.Fatalf("expected a conflict, got %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != pulledA {
		t.Errorf("link target was written: %q", data)
	}
}

// A file that stops being the test's between loading and planning isn't
// planned to be replaced.
func TestPlanPullForceRejectsFileChangedSinceLoading(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	local := []testfile.Test{{Path: path, Identifier: "a", TakeOverTestID: "test-a"}}
	if err := os.WriteFile(path, []byte("---\nidentifier: b\nname: B\n---\n\nB.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanPull(dir, []DashboardTest{{ID: "test-a", Name: "A"}}, local, PullOptions{Force: true}); err == nil {
		t.Error("expected an error")
	}
}

// A replacement that wasn't planned by PlanPull has nothing to check the file
// against, so it isn't written.
func TestWritePullRejectsUnplannedReplace(t *testing.T) {
	path, _ := forcePlan(t)
	err := WritePull(PullPlan{Files: []PulledFile{{Path: path, Test: testfile.Test{Identifier: "a", Name: "A"}, Replaces: true}}})
	if !errors.Is(err, ErrPullConflict) {
		t.Fatalf("expected a conflict, got %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != pulledA {
		t.Errorf("file was replaced: %q", data)
	}
}

func TestPlanPullKeepsIdentifiersWithinTheLimit(t *testing.T) {
	long := strings.Repeat("word ", 40)
	plan := mustPlan(t, t.TempDir(), []DashboardTest{
		{ID: "a", Name: long, Description: "A."},
		{ID: "b", Name: long, Description: "B."},
	}, nil, PullOptions{})
	for _, f := range plan.Files {
		if n := len(f.Test.Identifier); n > 128 || strings.HasSuffix(strings.TrimSuffix(f.Test.Identifier, "-2"), "-") {
			t.Errorf("identifier %q is %d characters", f.Test.Identifier, n)
		}
		data := testfile.Format(f.Test)
		if _, errs := testfile.Parse(f.Path, data); len(errs) > 0 {
			t.Errorf("pulled file doesn't check: %v", errs)
		}
	}
}
