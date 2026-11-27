package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/semaloop/cli/internal/api"
	"github.com/semaloop/cli/internal/client"
	"github.com/semaloop/cli/internal/testfile"
)

const listedTests = `{"success":true,"tests":[
	{"id":"t-checkout","externalId":"checkout","name":"Checkout","description":"Buy an eSIM.","folder":"Smoke","draft":false,"tags":["smoke","payments"],"secrets":["test-account"],"capabilities":[{"name":"audioInput","disabled":false}]},
	{"id":"t-sign-in","externalId":null,"name":"Sign in with email","description":"Sign in.","folder":"Smoke tests","draft":true,"tags":["smoke"],"secrets":["test-account"],"capabilities":[{"name":"audioInput","disabled":false},{"name":"dynamicIsland","disabled":true}]},
	{"id":"t-search","externalId":null,"name":"Search","description":"Search for Spain.","folder":null,"draft":false,"tags":[],"secrets":[],"capabilities":[]}
]}`

func listServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/tests" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		jsonResponse(w, status, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func fetch(t *testing.T) []DashboardTest {
	t.Helper()
	tests, err := FetchTests(context.Background(), "key", listServer(t, http.StatusOK, listedTests))
	if err != nil {
		t.Fatal(err)
	}
	return tests
}

func TestFetchTestsMapsEverySetting(t *testing.T) {
	got := fetch(t)
	want := DashboardTest{
		ID:                   "t-sign-in",
		Name:                 "Sign in with email",
		Description:          "Sign in.",
		Folder:               "Smoke tests",
		Draft:                true,
		Tags:                 []string{"smoke"},
		Secrets:              []string{"test-account"},
		DisabledCapabilities: []string{"dynamic_island"},
	}
	if len(got) != 3 || !reflect.DeepEqual(got[1], want) {
		t.Fatalf("got %+v, want second test %+v", got, want)
	}
	if got[0].ExternalID != "checkout" || got[0].DisabledCapabilities != nil {
		t.Errorf("unexpected test in code %+v", got[0])
	}
	if got[2].Folder != "" || got[2].ExternalID != "" {
		t.Errorf("unexpected dashboard test %+v", got[2])
	}
}

func TestFetchTestsErrors(t *testing.T) {
	for status, want := range map[int]error{
		http.StatusUnauthorized: client.ErrUnauthorized,
		http.StatusForbidden:    client.ErrForbidden,
	} {
		url := listServer(t, status, `{"success":false,"errors":[{"code":1,"message":"no"}]}`)
		if _, err := FetchTests(context.Background(), "key", url); !errors.Is(err, want) {
			t.Errorf("status %d: expected %v, got %v", status, want, err)
		}
	}
}

func TestFilterTests(t *testing.T) {
	tests := fetch(t)
	ids := func(f ListFilter) string {
		var out []string
		for _, t := range FilterTests(tests, f) {
			out = append(out, t.ID)
		}
		return strings.Join(out, ",")
	}
	for _, tt := range []struct {
		name   string
		filter ListFilter
		want   string
	}{
		{"all, by folder then name", ListFilter{}, "t-search,t-checkout,t-sign-in"},
		{"any tag", ListFilter{Tags: []string{"payments", "nope"}}, "t-checkout"},
		{"folder", ListFilter{Folders: []string{"Smoke tests"}}, "t-sign-in"},
		{"in code", ListFilter{InCode: true}, "t-checkout"},
		{"in the dashboard", ListFilter{InDashboard: true}, "t-search,t-sign-in"},
		{"every filter must match", ListFilter{Tags: []string{"smoke"}, InDashboard: true}, "t-sign-in"},
	} {
		if got := ids(tt.filter); got != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestWriteTestList(t *testing.T) {
	var out bytes.Buffer
	WriteTestList(&out, FilterTests(fetch(t), ListFilter{}), false)
	want := "" +
		"ID           NAME                         FOLDER        TAGS              IN CODE AS\n" +
		"t-search     Search\n" +
		"t-checkout   Checkout                     Smoke         smoke, payments   checkout\n" +
		"t-sign-in    Sign in with email (draft)   Smoke tests   smoke\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}

	out.Reset()
	WriteTestList(&out, nil, true)
	if out.String() != "[]\n" {
		t.Errorf("expected an empty JSON list, got %q", out.String())
	}
}

func TestPullWritesFilesThatSyncTakesOver(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".semaloop", "tests")
	var out bytes.Buffer
	if _, err := Pull(dir, fetch(t), &out, PullOptions{}, false); err != nil {
		t.Fatal(err)
	}
	want := "" +
		"  + " + filepath.Join(dir, "search.md") + "\n" +
		"  + " + filepath.Join(dir, "smoke-tests", "sign-in-with-email.md") + "\n" +
		"\n" +
		"Wrote 2 files. Commit them, then run `semaloop test sync` to move the tests into code. Run in a terminal, the sync then removes take_over_test_id, which is no longer needed.\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}

	tests, err := CheckTests(dir, &out, false)
	if err != nil {
		t.Fatalf("pulled files don't check: %v\n%s", err, out.String())
	}
	var signIn testfile.Test
	for _, t := range tests {
		if t.TakeOverTestID == "t-sign-in" {
			signIn = t
		}
	}
	in := syncInput(signIn)
	if in.TakeOverTestId.Value != "t-sign-in" || !reflect.DeepEqual(in.DisabledCapabilities, []api.TestCapabilityName{"dynamicIsland"}) {
		t.Errorf("unexpected sync input %+v", in)
	}
}

func TestPullAgainSkipsPulledTests(t *testing.T) {
	dir := t.TempDir()
	if _, err := Pull(dir, fetch(t), &bytes.Buffer{}, PullOptions{}, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := Pull(dir, fetch(t), &out, PullOptions{}, true); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Written []jsonPulledFile `json:"written"`
		Skipped []jsonPulledFile `json:"skipped"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output isn't JSON: %v\n%s", err, out.String())
	}
	if len(got.Written) != 0 || len(got.Skipped) != 2 {
		t.Errorf("expected nothing written and 2 skipped, got %+v", got)
	}
}

func TestPullRefusesWithInvalidFiles(t *testing.T) {
	dir := writeTestFiles(t, map[string]string{"broken.md": "---\nname: No identifier\n---\n\nBody.\n"})
	var out bytes.Buffer
	if _, err := Pull(dir, fetch(t), &out, PullOptions{}, false); !errors.Is(err, ErrInvalidTests) {
		t.Fatalf("expected ErrInvalidTests, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "search.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no files written, got %v", err)
	}
}
