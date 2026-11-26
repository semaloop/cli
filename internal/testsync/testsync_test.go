package testsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/semaloop/cli/internal/client"
)

func TestParse(t *testing.T) {
	got, err := Parse("a.md", []byte("---\r\nid: sign-in\r\nname: Sign in\r\nfolder: Smoke\r\ndraft: true\r\n---\r\n\r\nSign in.\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := Test{Path: "a.md", ID: "sign-in", Name: "Sign in", Description: "Sign in.", Folder: "Smoke", Draft: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"no frontmatter":        "Sign in.",
		"unterminated":          "---\nid: a\nname: A\nSign in.",
		"missing id":            "---\nname: A\n---\nSign in.",
		"bad id":                "---\nid: has space\nname: A\n---\nSign in.",
		"missing name":          "---\nid: a\n---\nSign in.",
		"empty body":            "---\nid: a\nname: A\n---\n\n",
		"unknown field":         "---\nid: a\nname: A\nfoldr: Smoke\n---\nSign in.",
		"folder too long":       "---\nid: a\nname: A\nfolder: " + strings.Repeat("x", 41) + "\n---\nSign in.",
		"emoji folder too long": "---\nid: a\nname: A\nfolder: " + strings.Repeat("🚀", 21) + "\n---\nSign in.",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse("a.md", []byte(raw)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestLoadExamples(t *testing.T) {
	tests, err := Load([]string{"../../examples/tests"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tt := range tests {
		ids = append(ids, tt.ID)
	}
	if got := strings.Join(ids, ","); got != "referral-code,install-esim,sign-in" {
		t.Errorf("got ids %s", got)
	}
}

func TestLoadRejectsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.md"} {
		writeFile(t, dir+"/"+name, "---\nid: same\nname: A\n---\nSign in.")
	}
	_, err := Load([]string{dir})
	if err == nil || !strings.Contains(err.Error(), `id "same" is already used`) {
		t.Errorf("got %v", err)
	}
}

func TestSync(t *testing.T) {
	var got syncRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/sync" {
			t.Errorf("path %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"success":true,"revision":4,"changes":[{"id":"a","action":"update","testId":"t1","fields":["name"]}]}`))
	}))
	defer srv.Close()

	base := 3
	result, err := Sync(context.Background(), "key", srv.URL,
		[]Test{{ID: "a", Name: "A", Description: "d"}, {ID: "b", Name: "B", Description: "d", Folder: "Smoke"}},
		SyncOptions{DryRun: true, BaseRevision: &base})
	if err != nil {
		t.Fatal(err)
	}
	if !got.DryRun || *got.BaseRevision != 3 || got.Tests[0].Folder != nil || *got.Tests[1].Folder != "Smoke" {
		t.Errorf("unexpected request %+v", got)
	}
	changes := result.Changes
	if result.Revision != 4 {
		t.Errorf("revision %d", result.Revision)
	}
	if len(changes) != 1 || changes[0].Action != "update" || changes[0].Fields[0] != "name" {
		t.Errorf("unexpected changes %+v", changes)
	}
}

func TestSyncErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"success":false,"errors":[{"code":10001,"message":"Invalid API key"}]}`, client.ErrUnauthorized.Error()},
		{400, `{"success":false,"errors":[{"code":7001,"message":"Active test limit reached"}]}`, "sync rejected: Active test limit reached"},
		{409, `{"success":false,"errors":[{"code":7003,"message":"Another sync is in progress"}]}`, "sync conflict: Another sync is in progress"},
		{500, `oops`, "HTTP 500"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		}))
		_, err := Sync(context.Background(), "key", srv.URL, nil, SyncOptions{})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("status %d: got %v, want %q", c.status, err, c.want)
		}
		if c.status == 401 && !errors.Is(err, client.ErrUnauthorized) {
			t.Errorf("401 should wrap ErrUnauthorized")
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
