package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/semaloop/cli/internal/api"
	"github.com/semaloop/cli/internal/testfile"
)

const longLine = "Open the app and sign in with the test account from the secrets. Go to Settings, then Workspaces, and add a second workspace called Marketing. Switch back to the first workspace, open the search bar, type the word roadmap and press enter. Check that results from every workspace are listed, grouped by workspace name, and that tapping a result from Marketing switches to that workspace and opens the document. Finally, clear the search and check the recent searches list shows roadmap at the top."

func TestDiffTokens(t *testing.T) {
	edits, ok := diffTokens([]string{"a", "b", "c", "d"}, []string{"a", "x", "c", "y", "d"})
	if !ok {
		t.Fatal("expected a diff")
	}
	var got []string
	for _, e := range edits {
		got = append(got, string("=-+"[e.kind])+e.text)
	}
	if want := "=a -b +x =c +y =d"; strings.Join(got, " ") != want {
		t.Errorf("got %s, want %s", strings.Join(got, " "), want)
	}
}

// An edit to a long line shows only the words around each change.
func TestTextChangesShowsWordsAroundEdits(t *testing.T) {
	to := strings.Replace(longLine, "every workspace are", "all open workspaces are", 1)
	to = strings.Replace(to, "opens the document.", "opens the document at the matching paragraph.", 1)
	rows, shortened := textChanges(Theme{}, longLine, to, TextOptions{Width: 120})
	want := []string{
		valueIndent + "…Check that results from [-every workspace-]{+all open workspaces+} are listed, grouped…",
		valueIndent + "…and opens the document{+ at the matching paragraph+}. Finally, clear the…",
	}
	if shortened || strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("got (shortened %v):\n%s\nwant:\n%s", shortened, strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
}

// valueLinesFor shows how ch changes test from remote.
func valueLinesFor(remote DashboardTest, local testfile.Test, ch api.SyncTestChange, opts TextOptions) ([]string, bool) {
	local.Path, local.Identifier = "t.md", ch.Identifier
	p := newPlan(".", testApp, []testfile.Test{local}, 1, false, []api.SyncTestChange{ch})
	return p.WithDashboard([]DashboardTest{remote}).valueLines(ch, opts)
}

// A line that's rewritten is shown removed, then added, cut to the width.
func TestValueLinesShortensRewrittenLines(t *testing.T) {
	to := "Open the cart and add a product from the recommendations, then tap Checkout and pay with Apple Pay, then check the receipt."
	ch := api.SyncTestChange{Identifier: "t", Action: actionUpdate, Fields: []string{"name", "description"}, TestId: api.NewNilString("t1")}
	remote := DashboardTest{ID: "t1", Name: strings.Repeat("Old ", 20), Description: longLine}
	local := testfile.Test{Name: "New", Description: to}

	lines, shortened := valueLinesFor(remote, local, ch, TextOptions{Width: 46})
	want := []string{
		valueIndent + `name  "Old Old Old Old Old Old Old Old …`,
		valueIndent + "description",
		valueIndent + "- Open the app and sign in with the tes…",
		valueIndent + "+ Open the cart and add a product from …",
	}
	if !shortened || strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("got (shortened %v):\n%s\nwant:\n%s", shortened, strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}

	full, shortened := valueLinesFor(remote, local, ch, TextOptions{Width: 46, Full: true})
	if shortened || full[2] != valueIndent+"- "+longLine {
		t.Errorf("expected the whole line with Full, got %q", full[2])
	}
}

func TestTextChangesOnlySpaces(t *testing.T) {
	rows, _ := textChanges(Theme{}, "Step one.\r\n\n ", "Step one.", TextOptions{})
	if want := valueIndent + "only spaces at the start or end change"; len(rows) != 1 || rows[0] != want {
		t.Errorf("got %q", rows)
	}
}

// With too many lines to compare every pair, each is compared with the one
// in its place, so edited lines still show as edits.
func TestHunkRowsWithManyLines(t *testing.T) {
	var deleted, inserted []string
	for i := range 12 {
		deleted = append(deleted, fmt.Sprintf("Step %d: tap the blue button.", i))
		inserted = append(inserted, fmt.Sprintf("Step %d: tap the green button.", i))
	}
	deleted[0], inserted[0] = "Something old entirely.", "Brand new words here."
	rows := hunkRows(Theme{}, deleted, inserted)
	if want := "…11: tap the [-blue-]{+green+} button."; len(rows) != 13 || rows[12] != want {
		t.Errorf("got %d rows:\n%s", len(rows), strings.Join(rows, "\n"))
	}
}

func TestTextChangesCapsRows(t *testing.T) {
	var from, to []string
	for i := range 8 {
		from = append(from, fmt.Sprintf("Step %d: tap the button.", i))
		to = append(to, fmt.Sprintf("Something else entirely, number %d.", i))
	}
	rows, shortened := textChanges(Theme{}, strings.Join(from, "\n"), strings.Join(to, "\n"), TextOptions{})
	if !shortened || len(rows) != maxValueRows+1 || rows[maxValueRows] != valueIndent+"  … 11 more lines" {
		t.Errorf("got (shortened %v):\n%s", shortened, strings.Join(rows, "\n"))
	}
	rows, _ = textChanges(Theme{}, strings.Join(from, "\n"), strings.Join(to, "\n"), TextOptions{Full: true})
	if len(rows) != 16 {
		t.Errorf("expected every row with Full, got %d", len(rows))
	}
}

const updateResult = `{"success":true,"app":{"name":"Sim Shop","bundleId":"com.example.simshop"},"revision":4,"changes":[
	{"identifier":"sign-in","action":"update","fields":["name","description","tags","draft"],"testId":"t1"},
	{"identifier":"checkout","action":"create","testId":null}
]}`

const updateList = `{"success":true,"tests":[{"id":"t1","name":"Log in","description":"Sign in with email.","folder":"Smoke","draft":true,"tags":["flaky","smoke"],"secrets":[],"capabilities":[],"externalId":"sign-in"}]}`

func TestDiffShowsValues(t *testing.T) {
	srv := newSyncServer(t, ok(updateResult))
	srv.list = updateList
	dir := writeTestFiles(t, map[string]string{"sign-in.md": signInFile, "checkout.md": checkoutFile})

	var out bytes.Buffer
	if _, err := Diff(context.Background(), srv.client(dir), &out, DiffOptions{}); err != nil {
		t.Fatal(err)
	}
	want := dir + " → Sim Shop (com.example.simshop)\n\n" +
		"  + checkout   Checkout   checkout.md\n" +
		"\n" +
		"  ~ sign-in   sign-in.md\n" +
		"      name  \"Log in\" → \"Sign in\"\n" +
		"      description\n" +
		"      - Sign in with email.\n" +
		"      + Sign in.\n" +
		"      tags  -flaky\n" +
		"      draft  true → false\n" +
		"\n" +
		"1 to create, 1 to update, 0 unchanged.\n"
	if out.String() != want {
		t.Errorf("got output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestDiffWithManyUpdatesOnlyNamesFields(t *testing.T) {
	files := map[string]string{}
	var changes []string
	for i := range maxShownUpdates + 1 {
		id := fmt.Sprintf("t%d", i)
		files[id+".md"] = fmt.Sprintf("---\nidentifier: %s\nname: T\n---\nDo it.\n", id)
		changes = append(changes, fmt.Sprintf(`{"identifier":%q,"action":"update","fields":["description"],"testId":%q}`, id, id))
	}
	srv := newSyncServer(t, ok(`{"success":true,"app":{"name":"Sim Shop","bundleId":"com.example.simshop"},"revision":4,"changes":[`+strings.Join(changes, ",")+`]}`))
	srv.list = updateList
	dir := writeTestFiles(t, files)

	var out bytes.Buffer
	if _, err := Diff(context.Background(), srv.client(dir), &out, DiffOptions{}); err != nil {
		t.Fatal(err)
	}
	if srv.lists != 0 {
		t.Errorf("expected no list request, got %d", srv.lists)
	}
	if !strings.Contains(out.String(), "  ~ t0   description   t0.md\n") || !strings.Contains(out.String(), "Pass --full to see how each test changes.") {
		t.Errorf("expected one line per test, got:\n%s", out.String())
	}
}

// Without the dashboard's tests, a plan still shows which fields change.
func TestDiffWithoutListNamesFields(t *testing.T) {
	srv := newSyncServer(t, ok(updateResult))
	dir := writeTestFiles(t, map[string]string{"sign-in.md": signInFile, "checkout.md": checkoutFile})
	var out bytes.Buffer
	if _, err := Diff(context.Background(), srv.client(dir), &out, DiffOptions{}); err != nil {
		t.Fatal(err)
	}
	if srv.lists != 1 || !strings.Contains(out.String(), "  ~ sign-in    name, description, tags, draft   sign-in.md\n") {
		t.Errorf("expected the fields named, got (lists %d):\n%s", srv.lists, out.String())
	}
}

// A sync that doesn't ask gets the values it changes before applying.
func TestSyncShowsValuesFromBeforeApplying(t *testing.T) {
	srv := newSyncServer(t, ok(updateResult))
	srv.list = updateList
	dir := writeTestFiles(t, map[string]string{"sign-in.md": signInFile, "checkout.md": checkoutFile})
	var out bytes.Buffer
	if _, err := Sync(context.Background(), srv.client(dir), &out, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `name  "Log in" → "Sign in"`) || !strings.Contains(out.String(), "1 created, 1 updated") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
}

func TestThemeColoursOnlyWithColours(t *testing.T) {
	r := lipgloss.NewRenderer(&bytes.Buffer{})
	r.SetColorProfile(termenv.Ascii)
	if NewTheme(r) != (Theme{}) {
		t.Error("expected no colours for the Ascii profile")
	}
	r.SetColorProfile(termenv.ANSI)
	th := NewTheme(r)
	if got := th.action(actionCreate, "+ a"); got == "+ a" || !strings.Contains(got, "\x1b[") {
		t.Errorf("expected a coloured create, got %q", got)
	}
	if got := th.removed("x"); strings.Contains(got, "[-") {
		t.Errorf("expected no markers with colours, got %q", got)
	}
}
