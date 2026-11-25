package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semaloop/cli/internal/api"
	"github.com/semaloop/cli/internal/client"
)

func writeTestFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func jsonResponse(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

const signInFile = `---
identifier: sign-in
name: Sign in
folder: Smoke
draft: false
tags: [smoke]
secrets: []
disabled_capabilities: [audio_input]
take_over_test_id: 01JABC
---
Sign in.
`

const checkoutFile = `---
identifier: checkout
name: Checkout
---
Buy something.
`

func TestCheckTestsWritesEveryError(t *testing.T) {
	dir := writeTestFiles(t, map[string]string{
		"a.md": "---\nid: a\nname: A\n---\nDo A.\n",
		"b.md": checkoutFile,
	})
	var out bytes.Buffer
	_, err := CheckTests(dir, &out)
	if !errors.Is(err, ErrInvalidTests) {
		t.Fatalf("expected ErrInvalidTests, got %v", err)
	}
	if !strings.Contains(err.Error(), "found 2 errors") {
		t.Errorf("expected error count in %q", err)
	}
	a := filepath.Join(dir, "a.md")
	want := a + ":1: missing identifier\n" + a + ":2: unknown field id: use identifier instead\n"
	if out.String() != want {
		t.Errorf("got output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestSyncTestsSendsFiles(t *testing.T) {
	var body map[string]any
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("could not decode request: %v", err)
		}
		jsonResponse(w, http.StatusOK, `{"success":true,"revision":3,"changes":[]}`)
	}))
	defer srv.Close()

	dir := writeTestFiles(t, map[string]string{"smoke/sign-in.md": signInFile, "checkout.md": checkoutFile})
	res, err := SyncTests(context.Background(), "key", srv.URL, dir, io.Discard, SyncOptions{AllowArchiveAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 3 {
		t.Errorf("expected revision 3, got %d", res.Revision)
	}
	if path != "/api/v1/tests/sync" {
		t.Errorf("unexpected path %q", path)
	}
	if auth != "Bearer key" {
		t.Errorf("unexpected Authorization header %q", auth)
	}

	got, _ := json.Marshal(body)
	want := `{"allowArchiveAll":true,"tests":[` +
		`{"description":"Buy something.","disabledCapabilities":[],"identifier":"checkout","name":"Checkout","tags":[]},` +
		`{"description":"Sign in.","disabledCapabilities":["audioInput"],"draft":false,"folder":"Smoke","identifier":"sign-in","name":"Sign in","secrets":[],"tags":["smoke"],"takeOverTestId":"01JABC"}` +
		`]}`
	if string(got) != want {
		t.Errorf("got request:\n%s\nwant:\n%s", got, want)
	}
}

// An empty directory still sends an empty list, so the API can archive every
// test in code when --allow-archive-all is set (and refuse otherwise).
func TestSyncTestsEmptyDirectorySendsEmptyList(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		jsonResponse(w, http.StatusOK, `{"success":true,"revision":1,"changes":[]}`)
	}))
	defer srv.Close()

	if _, err := SyncTests(context.Background(), "key", srv.URL, t.TempDir(), io.Discard, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if want := `{"allowArchiveAll":false,"tests":[]}`; string(raw) != want {
		t.Errorf("got request %s, want %s", raw, want)
	}
}

// Leaving out tags or disabled_capabilities means none, so they're sent as
// empty lists. A left-out secrets is not sent.
func TestSyncTestsSendsEmptyListsForLeftOutFields(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		jsonResponse(w, http.StatusOK, `{"success":true,"revision":1,"changes":[]}`)
	}))
	defer srv.Close()

	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})
	if _, err := SyncTests(context.Background(), "key", srv.URL, dir, io.Discard, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	want := `{"allowArchiveAll":false,"tests":[{"description":"Buy something.","disabledCapabilities":[],"identifier":"checkout","name":"Checkout","tags":[]}]}`
	if string(raw) != want {
		t.Errorf("got request %s, want %s", raw, want)
	}
}

func TestSyncTestsWritesChanges(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, `{"success":true,"revision":4,"changes":[
			{"identifier":"checkout","action":"create","testId":null},
			{"identifier":"sign-in","action":"update","fields":["description","disabledCapabilities"],"testId":null},
			{"identifier":"old","action":"archive","testId":null},
			{"identifier":"other","action":"unchanged","testId":null}
		]}`)
	}))
	defer srv.Close()

	dir := writeTestFiles(t, map[string]string{"smoke/sign-in.md": signInFile, "checkout.md": checkoutFile})
	var out bytes.Buffer
	if _, err := SyncTests(context.Background(), "key", srv.URL, dir, &out, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	want := "  + checkout  created  checkout.md\n" +
		"  ~ sign-in  updated: description, disabled_capabilities  " + filepath.Join("smoke", "sign-in.md") + "\n" +
		"  - old  archived\n" +
		"1 created, 1 updated, 1 archived, 1 unchanged.\n"
	if out.String() != want {
		t.Errorf("got output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestSyncTestsInvalidFilesMakeNoRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()

	dir := writeTestFiles(t, map[string]string{"a.md": "no frontmatter"})
	_, err := SyncTests(context.Background(), "key", srv.URL, dir, io.Discard, SyncOptions{})
	if !errors.Is(err, ErrInvalidTests) {
		t.Errorf("expected ErrInvalidTests, got %v", err)
	}
	if called {
		t.Error("expected no request")
	}
}

func TestSyncTestsDryRunMakesNoRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()

	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})
	if _, err := SyncTests(context.Background(), "key", srv.URL, dir, io.Discard, SyncOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("expected no request")
	}
}

func TestSyncTestsErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantIs  error
		wantMsg string
	}{
		{
			name:    "rejected",
			status:  http.StatusBadRequest,
			body:    `{"success":false,"errors":[{"code":400,"message":"sign-in: unknown secret slug \"nope\""},{"code":400,"message":"would archive every test in code"}]}`,
			wantMsg: `sync rejected: sign-in: unknown secret slug "nope"; would archive every test in code`,
		},
		{
			name:    "conflict",
			status:  http.StatusConflict,
			body:    `{"success":false,"errors":[{"code":409,"message":"another sync is in progress"}]}`,
			wantMsg: "sync conflict: another sync is in progress",
		},
		{
			name:   "unauthorized",
			status: http.StatusUnauthorized,
			body:   `{"success":false,"errors":[]}`,
			wantIs: client.ErrUnauthorized,
		},
		{
			name:    "server error",
			status:  http.StatusInternalServerError,
			body:    `{}`,
			wantMsg: "unexpected response from Semaloop: 500 Internal Server Error, so the sync may have been applied",
		},
		{
			name:    "unexpected status",
			status:  http.StatusTeapot,
			body:    `{}`,
			wantMsg: "unexpected response from Semaloop: 418 I'm a teapot",
		},
		{
			name:   "forbidden",
			status: http.StatusForbidden,
			body:   `{"success":false,"errors":[{"code":403,"message":"App not found or access denied"}]}`,
			wantIs: client.ErrForbidden,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(w, tt.status, tt.body)
			}))
			defer srv.Close()

			dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})
			_, err := SyncTests(context.Background(), "key", srv.URL, dir, io.Discard, SyncOptions{})
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("expected %v, got %v", tt.wantIs, err)
			}
			if tt.wantMsg != "" && err.Error() != tt.wantMsg {
				t.Errorf("expected %q, got %q", tt.wantMsg, err)
			}
		})
	}
}

func TestSyncTestsUnreadableResponseMayHaveApplied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, `{"success":true,"revision":4,"changes":[{"identifier":"checkout","action":"restore","testId":null}]}`)
	}))
	defer srv.Close()

	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})
	_, err := SyncTests(context.Background(), "key", srv.URL, dir, io.Discard, SyncOptions{})
	if err == nil || !strings.Contains(err.Error(), "the sync may have been applied") {
		t.Errorf("expected a may-have-been-applied error, got %v", err)
	}
}

func TestSyncTestsConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})
	_, err := SyncTests(context.Background(), "key", srv.URL, dir, io.Discard, SyncOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "could not connect to Semaloop") {
		t.Errorf("expected a connection error, got %v", err)
	}
}

func TestWriteChangesShowsUnknownActions(t *testing.T) {
	var out bytes.Buffer
	writeChanges(&out, ".", nil, []api.SyncTestChange{
		{Identifier: "a", Action: "restore"},
		{Identifier: "b", Action: api.SyncTestChangeActionCreate},
		{Identifier: "c", Action: "soft_delete"},
	})
	want := "  ? a  restore\n" +
		"  + b  created\n" +
		"  ? c  soft delete\n" +
		"1 created, 1 restore, 1 soft delete, 0 unchanged.\n"
	if out.String() != want {
		t.Errorf("got output:\n%s\nwant:\n%s", out.String(), want)
	}
}
