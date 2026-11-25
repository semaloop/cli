package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/semaloop/cli/internal/api"
	"github.com/semaloop/cli/internal/client"
	"github.com/semaloop/cli/internal/testfile"
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
	_, err := CheckTests(dir, &out, false)
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

func TestCheckTestsWritesJSON(t *testing.T) {
	dir := writeTestFiles(t, map[string]string{"a.md": "---\nname: A\n---\nDo A.\n"})
	var out bytes.Buffer
	if _, err := CheckTests(dir, &out, true); !errors.Is(err, ErrInvalidTests) {
		t.Fatalf("expected ErrInvalidTests, got %v", err)
	}
	var got struct {
		Tests  int
		Errors []map[string]any
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output isn't JSON: %v\n%s", err, out.String())
	}
	if got.Tests != 0 || len(got.Errors) != 1 || got.Errors[0]["message"] != "missing identifier" || got.Errors[0]["line"] != 1.0 {
		t.Errorf("unexpected output %s", out.String())
	}
}

// syncServer answers each sync request with the next of responses, and
// records the requests' bodies.
type syncServer struct {
	*httptest.Server
	t         *testing.T
	responses []syncResponse
	requests  []string
}

type syncResponse struct {
	status int
	body   string
}

func ok(body string) syncResponse { return syncResponse{http.StatusOK, body} }

func newSyncServer(t *testing.T, responses ...syncResponse) *syncServer {
	t.Helper()
	s := &syncServer{t: t, responses: responses}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/sync" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		raw, _ := io.ReadAll(r.Body)
		s.requests = append(s.requests, string(raw))
		if len(s.responses) == 0 {
			t.Errorf("unexpected request %s", raw)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		res := s.responses[0]
		s.responses = s.responses[1:]
		jsonResponse(w, res.status, res.body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *syncServer) client(dir string) *SyncClient {
	s.t.Helper()
	tests, err := CheckTests(dir, io.Discard, false)
	if err != nil {
		s.t.Fatal(err)
	}
	c, err := NewSyncClient("key", s.URL, dir, tests)
	if err != nil {
		s.t.Fatal(err)
	}
	return c
}

// requestOptions is a request's body without its tests.
func requestOptions(t *testing.T, raw string) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	delete(body, "tests")
	got, _ := json.Marshal(body)
	return string(got)
}

const emptyResult = `{"success":true,"revision":3,"changes":[]}`

func TestSyncSendsFiles(t *testing.T) {
	srv := newSyncServer(t, ok(emptyResult))
	dir := writeTestFiles(t, map[string]string{"smoke/sign-in.md": signInFile, "checkout.md": checkoutFile})

	p, err := Sync(context.Background(), srv.client(dir), io.Discard, SyncOptions{AllowArchiveAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Revision != 3 || !p.Applied {
		t.Errorf("expected applied revision 3, got %+v", p)
	}
	want := `{"allowArchiveAll":true,"tests":[` +
		`{"description":"Buy something.","disabledCapabilities":[],"identifier":"checkout","name":"Checkout","tags":[]},` +
		`{"description":"Sign in.","disabledCapabilities":["audio_input"],"draft":false,"folder":"Smoke","identifier":"sign-in","name":"Sign in","secrets":[],"tags":["smoke"],"takeOverTestId":"01JABC"}` +
		`]}`
	if srv.requests[0] != want {
		t.Errorf("got request:\n%s\nwant:\n%s", srv.requests[0], want)
	}
}

// An empty directory still sends an empty list, so the API can archive every
// test in code when --allow-archive-all is set (and refuse otherwise).
func TestSyncEmptyDirectorySendsEmptyList(t *testing.T) {
	srv := newSyncServer(t, ok(emptyResult))
	if _, err := Sync(context.Background(), srv.client(t.TempDir()), io.Discard, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if want := `{"allowArchiveAll":false,"tests":[]}`; srv.requests[0] != want {
		t.Errorf("got request %s, want %s", srv.requests[0], want)
	}
}

// Leaving out tags or disabled_capabilities means none, so they're sent as
// empty lists. A left-out secrets is not sent.
func TestSyncSendsEmptyListsForLeftOutFields(t *testing.T) {
	srv := newSyncServer(t, ok(emptyResult))
	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})
	if _, err := Sync(context.Background(), srv.client(dir), io.Discard, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	want := `{"allowArchiveAll":false,"tests":[{"description":"Buy something.","disabledCapabilities":[],"identifier":"checkout","name":"Checkout","tags":[]}]}`
	if srv.requests[0] != want {
		t.Errorf("got request %s, want %s", srv.requests[0], want)
	}
}

const mixedResult = `{"success":true,"revision":4,"dryRun":true,"changes":[
	{"identifier":"old","action":"archive","testId":null},
	{"identifier":"other","action":"unchanged","testId":null},
	{"identifier":"sign-in","action":"update","fields":["description","disabledCapabilities"],"testId":null},
	{"identifier":"checkout","action":"create","testId":null}
]}`

func TestDiffWritesPlan(t *testing.T) {
	srv := newSyncServer(t, ok(mixedResult))
	dir := writeTestFiles(t, map[string]string{"smoke/sign-in.md": signInFile, "checkout.md": checkoutFile})

	var out bytes.Buffer
	if _, err := Diff(context.Background(), srv.client(dir), &out, false); err != nil {
		t.Fatal(err)
	}
	if got, want := requestOptions(t, srv.requests[0]), `{"allowArchiveAll":true,"dryRun":true}`; got != want {
		t.Errorf("got request options %s, want %s", got, want)
	}
	want := dir + " → the app for your API key\n\n" +
		"  + checkout   Checkout                             checkout.md\n" +
		"  ~ sign-in    description, disabled_capabilities   " + filepath.Join("smoke", "sign-in.md") + "\n" +
		"  - old        file removed, will be archived\n" +
		"\n" +
		"1 to create, 1 to update, 1 to archive, 1 unchanged.\n" +
		"Run `semaloop test sync` to apply.\n"
	if out.String() != want {
		t.Errorf("got output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestDiffWithoutChanges(t *testing.T) {
	srv := newSyncServer(t, ok(`{"success":true,"revision":4,"changes":[{"identifier":"checkout","action":"unchanged","testId":null}]}`))
	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})

	var out bytes.Buffer
	if _, err := Diff(context.Background(), srv.client(dir), &out, false); err != nil {
		t.Fatal(err)
	}
	if want := dir + " → the app for your API key\n\nNo changes, 1 unchanged.\n"; out.String() != want {
		t.Errorf("got output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestDiffWritesJSON(t *testing.T) {
	srv := newSyncServer(t, ok(mixedResult))
	dir := writeTestFiles(t, map[string]string{"smoke/sign-in.md": signInFile, "checkout.md": checkoutFile})

	var out bytes.Buffer
	if _, err := Diff(context.Background(), srv.client(dir), &out, true); err != nil {
		t.Fatal(err)
	}
	var got jsonPlan
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output isn't JSON: %v\n%s", err, out.String())
	}
	if got.Revision != 4 || got.Applied || got.ArchivesAll || len(got.Changes) != 4 || len(got.Warnings) != 0 {
		t.Errorf("unexpected plan %+v", got)
	}
	if c := got.Changes[1]; c.Identifier != "sign-in" || c.Path != filepath.Join("smoke", "sign-in.md") || strings.Join(c.Fields, ",") != "description,disabled_capabilities" {
		t.Errorf("unexpected change %+v", c)
	}
	if want := map[string]int{"create": 1, "update": 1, "archive": 1, "unchanged": 1}; !maps.Equal(got.Totals, want) {
		t.Errorf("got totals %v, want %v", got.Totals, want)
	}
}

func TestPlanWarnsAboutRedundantTakeOvers(t *testing.T) {
	dir := "tests"
	p := newPlan(dir, []testfile.Test{
		{Path: filepath.Join(dir, "sign-in.md"), Identifier: "sign-in", Name: "Sign in"},
		{Path: filepath.Join(dir, "checkout.md"), Identifier: "checkout", Name: "Checkout"},
	}, 1, false, []api.SyncTestChange{
		{Identifier: "login", Action: api.SyncTestChangeActionArchive},
		{Identifier: "sign-in", Action: api.SyncTestChangeActionCreate},
		{Identifier: "checkout", Action: api.SyncTestChangeActionUnchanged, TakeOverRedundant: api.NewOptSyncTestChangeTakeOverRedundant(true)},
	})
	// The API doesn't name archived tests, so there's no name to match a
	// rename against.
	want := []string{
		"checkout is already in code, so take_over_test_id can be removed from checkout.md.",
	}
	if got := p.Warnings(); !slices.Equal(got, want) {
		t.Errorf("got warnings:\n%q\nwant:\n%q", got, want)
	}
}

func TestPlanArchivesAll(t *testing.T) {
	change := func(action api.SyncTestChangeAction) api.SyncTestChange {
		return api.SyncTestChange{Identifier: string(action), Action: action}
	}
	tests := []struct {
		name    string
		changes []api.SyncTestChange
		want    bool
	}{
		{"nothing in code", nil, false},
		{"only creates", []api.SyncTestChange{change(api.SyncTestChangeActionCreate)}, false},
		{"archives all", []api.SyncTestChange{change(api.SyncTestChangeActionArchive), change(api.SyncTestChangeActionArchive)}, true},
		{"archives all and creates", []api.SyncTestChange{change(api.SyncTestChangeActionArchive), change(api.SyncTestChangeActionCreate)}, true},
		{"one stays", []api.SyncTestChange{change(api.SyncTestChangeActionArchive), change(api.SyncTestChangeActionUnchanged)}, false},
		{"one updated", []api.SyncTestChange{change(api.SyncTestChangeActionArchive), change(api.SyncTestChangeActionUpdate)}, false},
		{"redundant take over", []api.SyncTestChange{
			change(api.SyncTestChangeActionArchive),
			{Identifier: "t", Action: api.SyncTestChangeActionTakeOver, TakeOverRedundant: api.NewOptSyncTestChangeTakeOverRedundant(true)},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newPlan(".", nil, 1, false, tt.changes).ArchivesAll(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

const plannedCreate = `{"success":true,"revision":%d,"dryRun":true,"changes":[{"identifier":"checkout","action":"create","testId":null}]}`
const appliedCreate = `{"success":true,"revision":%d,"changes":[{"identifier":"checkout","action":"create","testId":null}]}`

func confirmOptions(answers string, prompt io.Writer) SyncOptions {
	return SyncOptions{Confirm: true, In: strings.NewReader(answers), Prompt: prompt}
}

func TestSyncConfirmedAppliesThePlan(t *testing.T) {
	srv := newSyncServer(t, ok(fmt.Sprintf(plannedCreate, 7)), ok(fmt.Sprintf(appliedCreate, 8)))
	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})

	var out, prompt bytes.Buffer
	p, err := Sync(context.Background(), srv.client(dir), &out, confirmOptions("y\n", &prompt))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Applied || p.Revision != 8 {
		t.Errorf("expected applied revision 8, got %+v", p)
	}
	if len(srv.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(srv.requests))
	}
	if got, want := requestOptions(t, srv.requests[0]), `{"allowArchiveAll":true,"dryRun":true}`; got != want {
		t.Errorf("got plan options %s, want %s", got, want)
	}
	if got, want := requestOptions(t, srv.requests[1]), `{"allowArchiveAll":false,"baseRevision":7}`; got != want {
		t.Errorf("got apply options %s, want %s", got, want)
	}
	if prompt.String() != "Apply these changes? [y/N] " {
		t.Errorf("unexpected prompt %q", prompt.String())
	}
	if !strings.Contains(out.String(), "1 to create, 0 unchanged.\n") || !strings.HasSuffix(out.String(), "1 created, 0 unchanged.\n") {
		t.Errorf("expected the plan and then the totals, got:\n%s", out.String())
	}
}

func TestSyncNotConfirmedChangesNothing(t *testing.T) {
	for _, answer := range []string{"n\n", "\n", "", "nope\n"} {
		srv := newSyncServer(t, ok(fmt.Sprintf(plannedCreate, 7)))
		dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})

		_, err := Sync(context.Background(), srv.client(dir), io.Discard, confirmOptions(answer, io.Discard))
		if !errors.Is(err, ErrCancelled) {
			t.Errorf("answer %q: expected ErrCancelled, got %v", answer, err)
		}
		if len(srv.requests) != 1 {
			t.Errorf("answer %q: expected only the plan request, got %d", answer, len(srv.requests))
		}
	}
}

func TestSyncWithoutChangesDoesntAsk(t *testing.T) {
	srv := newSyncServer(t, ok(`{"success":true,"revision":7,"changes":[{"identifier":"checkout","action":"unchanged","testId":null}]}`))
	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})

	var prompt bytes.Buffer
	if _, err := Sync(context.Background(), srv.client(dir), io.Discard, confirmOptions("", &prompt)); err != nil {
		t.Fatal(err)
	}
	if prompt.Len() != 0 || len(srv.requests) != 1 {
		t.Errorf("expected one request and no prompt, got %d requests and prompt %q", len(srv.requests), prompt.String())
	}
}

// When another sync lands between the plan and applying it, the new plan is
// shown and confirmed before anything is applied.
func TestSyncConflictPlansAgain(t *testing.T) {
	srv := newSyncServer(t,
		ok(fmt.Sprintf(plannedCreate, 7)),
		syncResponse{http.StatusConflict, `{"success":false,"errors":[{"code":409,"message":"the app's tests have changed since revision 7"}]}`},
		ok(fmt.Sprintf(plannedCreate, 9)),
		ok(fmt.Sprintf(appliedCreate, 10)),
	)
	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})

	var out, prompt bytes.Buffer
	if _, err := Sync(context.Background(), srv.client(dir), &out, confirmOptions("y\ny\n", &prompt)); err != nil {
		t.Fatal(err)
	}
	if len(srv.requests) != 4 {
		t.Fatalf("expected 4 requests, got %d", len(srv.requests))
	}
	if got, want := requestOptions(t, srv.requests[3]), `{"allowArchiveAll":false,"baseRevision":9}`; got != want {
		t.Errorf("got apply options %s, want %s", got, want)
	}
	if n := strings.Count(prompt.String(), "Apply these changes?"); n != 2 {
		t.Errorf("expected to be asked twice, was asked %d times", n)
	}
	if n := strings.Count(out.String(), "1 to create"); n != 2 {
		t.Errorf("expected the plan twice, got:\n%s", out.String())
	}
}

const plannedArchiveAll = `{"success":true,"revision":7,"dryRun":true,"changes":[{"identifier":"a","action":"archive","testId":null},{"identifier":"b","action":"archive","testId":null}]}`

func TestSyncArchiveAllNeedsTypedConfirmation(t *testing.T) {
	t.Run("y isn't enough", func(t *testing.T) {
		srv := newSyncServer(t, ok(plannedArchiveAll))
		var out, prompt bytes.Buffer
		_, err := Sync(context.Background(), srv.client(t.TempDir()), &out, confirmOptions("y\n", &prompt))
		if !errors.Is(err, ErrCancelled) {
			t.Errorf("expected ErrCancelled, got %v", err)
		}
		if len(srv.requests) != 1 {
			t.Errorf("expected only the plan request, got %d", len(srv.requests))
		}
		if !strings.Contains(prompt.String(), `Type "archive all" to confirm`) {
			t.Errorf("unexpected prompt %q", prompt.String())
		}
		if !strings.Contains(out.String(), "Warning: this archives every test in code.") {
			t.Errorf("expected an archive-all warning, got:\n%s", out.String())
		}
	})

	t.Run("typed", func(t *testing.T) {
		srv := newSyncServer(t, ok(plannedArchiveAll), ok(`{"success":true,"revision":8,"changes":[{"identifier":"a","action":"archive","testId":null},{"identifier":"b","action":"archive","testId":null}]}`))
		if _, err := Sync(context.Background(), srv.client(t.TempDir()), io.Discard, confirmOptions("archive all\n", io.Discard)); err != nil {
			t.Fatal(err)
		}
		if got, want := requestOptions(t, srv.requests[1]), `{"allowArchiveAll":true,"baseRevision":7}`; got != want {
			t.Errorf("got apply options %s, want %s", got, want)
		}
	})
}

func TestSyncErrors(t *testing.T) {
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
			wantIs:  ErrConflict,
			wantMsg: "sync conflict: another sync is in progress",
		},
		{
			name:   "unauthorized",
			status: http.StatusUnauthorized,
			body:   `{"success":false,"errors":[]}`,
			wantIs: client.ErrUnauthorized,
		},
		{
			name:    "unexpected status",
			status:  http.StatusInternalServerError,
			body:    `{}`,
			wantMsg: "unexpected response from Semaloop: 500 Internal Server Error",
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
			srv := newSyncServer(t, syncResponse{tt.status, tt.body})
			dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})
			_, err := Sync(context.Background(), srv.client(dir), io.Discard, SyncOptions{})
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

func TestSyncUnreadableResponseMayHaveApplied(t *testing.T) {
	srv := newSyncServer(t, ok(`{"success":true,"revision":4,"changes":[{"identifier":"checkout","action":"restore","testId":null}]}`))
	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})
	_, err := Sync(context.Background(), srv.client(dir), io.Discard, SyncOptions{})
	if err == nil || !strings.Contains(err.Error(), "the sync may have been applied") {
		t.Errorf("expected a may-have-been-applied error, got %v", err)
	}
}

func TestSyncConnectionClosedAfterRequestMayHaveApplied(t *testing.T) {
	// The server reads the whole request, as if it applied the sync, then
	// closes the connection without answering.
	received := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("could not hijack: %v", err)
			return
		}
		conn.Close()
	}))
	t.Cleanup(srv.Close)

	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})
	tests, err := CheckTests(dir, io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewSyncClient("key", srv.URL, dir, tests)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Sync(context.Background(), c, io.Discard, SyncOptions{})
	if err == nil || !strings.Contains(err.Error(), "the sync may have been applied") {
		t.Errorf("expected a may-have-been-applied error, got %v", err)
	}
	if body := <-received; !strings.Contains(body, `"checkout"`) {
		t.Errorf("expected the server to get the sync, got %s", body)
	}
}

func TestSyncConnectionError(t *testing.T) {
	srv := newSyncServer(t)
	c := srv.client(writeTestFiles(t, map[string]string{"checkout.md": checkoutFile}))
	srv.Close()
	_, err := Sync(context.Background(), c, io.Discard, SyncOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "could not connect to Semaloop") {
		t.Errorf("expected a connection error, got %v", err)
	}
}

func TestWriteTextShowsUnknownActions(t *testing.T) {
	var out bytes.Buffer
	newPlan(".", []testfile.Test{{Path: "b.md", Identifier: "b", Name: "B"}}, 1, true, []api.SyncTestChange{
		{Identifier: "a", Action: "restore"},
		{Identifier: "b", Action: api.SyncTestChangeActionCreate},
		{Identifier: "c", Action: "soft_delete"},
	}).WriteText(&out)
	want := ". → the app for your API key\n\n" +
		"  + b   B             b.md\n" +
		"  ? a   restore\n" +
		"  ? c   soft delete\n" +
		"\n" +
		"1 created, 1 restore, 1 soft delete, 0 unchanged.\n"
	if out.String() != want {
		t.Errorf("got output:\n%q\nwant:\n%q", out.String(), want)
	}
}

func TestLoadTestsOnlyWritesErrors(t *testing.T) {
	dir := writeTestFiles(t, map[string]string{"checkout.md": checkoutFile})
	var out bytes.Buffer
	if _, err := LoadTests(dir, &out, true); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output, got %q", out.String())
	}
}
