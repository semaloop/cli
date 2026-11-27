package cmd

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/log"
	"github.com/charmbracelet/x/ansi"
	"github.com/semaloop/cli/internal/api"
	"github.com/semaloop/cli/internal/testfile"
)

// Plan is what a sync would change, or, once applied, what it changed.
type Plan struct {
	Dir string
	// App is the app the API key belongs to, whose tests the plan changes.
	App      api.SyncTestsResponseApp
	Revision int
	// Applied is true when the changes have been made.
	Applied bool
	// Changes has one change per test, grouped by action in the order
	// actions lists them.
	Changes []api.SyncTestChange
	// RemovedTakeOvers are the files, relative to Dir, that
	// RemoveTakeOvers removed take_over_test_id from.
	RemovedTakeOvers []string

	tests map[string]testfile.Test
	// dashboard is the app's tests from before the plan, when they're known.
	dashboard []DashboardTest
}

func newPlan(dir string, app api.SyncTestsResponseApp, tests []testfile.Test, revision int, applied bool, changes []api.SyncTestChange) *Plan {
	p := &Plan{
		Dir:      dir,
		App:      app,
		Revision: revision,
		Applied:  applied,
		Changes:  slices.Clone(changes),
		tests:    map[string]testfile.Test{},
	}
	for _, t := range tests {
		p.tests[t.Identifier] = t
	}
	slices.SortStableFunc(p.Changes, func(a, b api.SyncTestChange) int {
		ra, rb := actionRank(a.Action), actionRank(b.Action)
		if ra != rb || ra < len(actions) {
			return cmp.Compare(ra, rb)
		}
		return strings.Compare(string(a.Action), string(b.Action))
	})
	return p
}

// syncAction is what a sync does to one test. It's a plain string because
// the API may add actions that this version of the CLI doesn't know.
type syncAction = string

const (
	actionCreate    syncAction = "create"
	actionUpdate    syncAction = "update"
	actionTakeOver  syncAction = "take_over"
	actionArchive   syncAction = "archive"
	actionUnchanged syncAction = "unchanged"
)

type changeAction struct {
	action  syncAction
	symbol  string
	planned string
	applied string
}

// actions lists the changes a sync can make, in the order they're shown, with
// the symbol and labels each is shown with.
var actions = []changeAction{
	{actionCreate, "+", "to create", "created"},
	{actionUpdate, "~", "to update", "updated"},
	{actionTakeOver, "~", "to take over", "taken over"},
	{actionArchive, "-", "to archive", "archived"},
	{actionUnchanged, " ", "unchanged", "unchanged"},
}

func actionRank(action syncAction) int {
	if i := slices.IndexFunc(actions, func(a changeAction) bool { return a.action == action }); i >= 0 {
		return i
	}
	return len(actions)
}

// lookupAction returns how to show action. An action this version of the CLI
// doesn't know is shown by its name, so no change goes unreported.
func lookupAction(action syncAction) changeAction {
	if i := actionRank(action); i < len(actions) {
		return actions[i]
	}
	name := strings.ReplaceAll(string(action), "_", " ")
	return changeAction{action, "?", name, name}
}

func (a changeAction) label(applied bool) string {
	if applied {
		return a.applied
	}
	return a.planned
}

// HasChanges reports whether the plan changes anything.
func (p *Plan) HasChanges() bool {
	return slices.ContainsFunc(p.Changes, func(ch api.SyncTestChange) bool {
		return ch.Action != actionUnchanged
	})
}

// ArchivesAll reports whether the plan archives every test in code, which
// needs extra confirmation.
func (p *Plan) ArchivesAll() bool {
	archived := false
	for _, ch := range p.Changes {
		switch ch.Action {
		case actionArchive:
			archived = true
		case actionCreate:
		case actionTakeOver:
			// A redundant take over names a test that's already in code,
			// and stays there.
			if ch.TakeOverRedundant.Value {
				return false
			}
		default:
			// The test is in code, and stays there.
			return false
		}
	}
	return archived
}

// Warnings lists what's worth knowing before applying the plan.
func (p *Plan) Warnings() []string {
	var warnings []string
	// A test that looks renamed: archived under one identifier and created
	// under another with the same name.
	created := map[string][]string{}
	for _, ch := range p.Changes {
		if ch.Action == actionCreate {
			created[p.name(ch)] = append(created[p.name(ch)], ch.Identifier)
		}
	}
	for _, ch := range p.Changes {
		if ch.Action != actionArchive || p.name(ch) == "" {
			continue
		}
		for _, id := range created[p.name(ch)] {
			warnings = append(warnings, fmt.Sprintf(
				"%s looks renamed to %s. Changing a test's identifier creates a new test, so its history won't carry over.",
				ch.Identifier, id))
		}
	}
	var redundant []api.SyncTestChange
	for _, ch := range p.Changes {
		if bool(ch.TakeOverRedundant.Value) && !slices.Contains(p.RemovedTakeOvers, p.path(ch)) {
			redundant = append(redundant, ch)
		}
	}
	switch len(redundant) {
	case 0:
	case 1:
		warnings = append(warnings, fmt.Sprintf(
			"%s is already in code, so take_over_test_id can be removed from %s.",
			redundant[0].Identifier, cmp.Or(p.path(redundant[0]), "its file")))
	default:
		paths := make([]string, len(redundant))
		for i, ch := range redundant {
			paths[i] = cmp.Or(p.path(ch), ch.Identifier)
		}
		warnings = append(warnings, fmt.Sprintf(
			"%d tests are already in code, so take_over_test_id can be removed from their files: %s.",
			len(redundant), strings.Join(paths, ", ")))
	}
	return warnings
}

// doneTakeOvers are the test files whose take_over_test_id is no longer
// needed: the test is already in code, or, once the plan is applied, has just
// been taken over.
func (p *Plan) doneTakeOvers() []testfile.Test {
	var done []testfile.Test
	for _, ch := range p.Changes {
		t, ok := p.tests[ch.Identifier]
		if !ok || t.TakeOverTestID == "" {
			continue
		}
		if bool(ch.TakeOverRedundant.Value) || (p.Applied && ch.Action == actionTakeOver) {
			done = append(done, t)
		}
	}
	return done
}

// RemoveTakeOvers removes take_over_test_id from the files that no longer
// need it, adding each one to RemovedTakeOvers. A file that can't be changed
// is logged and left as it is: the field does no harm, so the sync has still
// succeeded.
func (p *Plan) RemoveTakeOvers() {
	for _, t := range p.doneTakeOvers() {
		if err := removeTakeOver(t); err != nil {
			log.Warn("Could not remove take_over_test_id, which is no longer needed.", "path", t.Path, "reason", err)
			continue
		}
		p.RemovedTakeOvers = append(p.RemovedTakeOvers, p.relPath(t))
	}
}

var errFileChanged = errors.New("the file has changed since the sync read it")

// removeTakeOver removes take_over_test_id from t's file, if the file still
// says what it did when it was loaded.
func removeTakeOver(t testfile.Test) error {
	data, err := os.ReadFile(t.Path)
	if err != nil {
		return err
	}
	if now, errs := testfile.Parse(t.Path, data); len(errs) > 0 || now.Identifier != t.Identifier || now.TakeOverTestID != t.TakeOverTestID {
		return errFileChanged
	}
	out, err := testfile.RemoveTakeOver(t.Path, data)
	if err != nil {
		return err
	}
	err = replaceFile(t.Path, data, out)
	if errors.Is(err, ErrPullConflict) {
		return errFileChanged
	}
	return err
}

// removedText says which files RemoveTakeOvers changed, or is empty when it
// changed none.
func (p *Plan) removedText() string {
	switch n := len(p.RemovedTakeOvers); n {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("Removed take_over_test_id from %s, as its test is now in code. Commit the change.", p.RemovedTakeOvers[0])
	default:
		return fmt.Sprintf("Removed take_over_test_id from %d files, as their tests are now in code: %s. Commit the change.",
			n, strings.Join(p.RemovedTakeOvers, ", "))
	}
}

// name is the test's name, from the file when there is one.
func (p *Plan) name(ch api.SyncTestChange) string {
	if t, ok := p.tests[ch.Identifier]; ok {
		return t.Name
	}
	return ch.Name.Value
}

// path is the test's file, relative to the plan's directory, or empty when
// there's no file.
func (p *Plan) path(ch api.SyncTestChange) string {
	t, ok := p.tests[ch.Identifier]
	if !ok {
		return ""
	}
	return p.relPath(t)
}

// relPath is t's path relative to Dir.
func (p *Plan) relPath(t testfile.Test) string {
	if rel, err := filepath.Rel(p.Dir, t.Path); err == nil {
		return rel
	}
	return t.Path
}

// detail describes ch. withFields names the fields it changes, for when they
// aren't shown below it.
func (p *Plan) detail(t Theme, ch api.SyncTestChange, withFields bool) string {
	fields := make([]string, len(ch.Fields))
	for i, f := range ch.Fields {
		fields[i] = snakeCase(string(f))
	}
	switch ch.Action {
	case actionCreate:
		return p.name(ch)
	case actionUpdate:
		if !withFields {
			return ""
		}
		return t.yellow(strings.Join(fields, ", "))
	case actionTakeOver:
		d := "take over " + cmp.Or(ch.TestId.Value, "dashboard test")
		if withFields && len(fields) > 0 {
			d += ": " + t.yellow(strings.Join(fields, ", "))
		}
		return d
	case actionArchive:
		if p.Applied {
			return "file removed, archived"
		}
		return "file removed, will be archived"
	default:
		d := lookupAction(ch.Action).label(p.Applied)
		if len(fields) > 0 {
			d += ": " + strings.Join(fields, ", ")
		}
		return d
	}
}

// WriteText writes the plan for a person to read: the directory and app, one
// line per changed test, any warnings, then totals. When the plan has the
// dashboard's tests, it shows how each changed field changes under its test.
func (p *Plan) WriteText(out io.Writer, opts TextOptions) {
	t := opts.Theme
	fmt.Fprintf(out, "%s → %s %s\n\n", t.bold(p.Dir), t.bold(p.App.Name), t.dim("("+p.App.BundleId+")"))

	if p.HasChanges() {
		if p.dashboard != nil && p.wantsValues(opts.Full) {
			p.writeValues(out, opts)
		} else {
			p.writeTable(out, t)
			// Once a plan's applied, there's nothing left to see in full.
			if !p.Applied && !opts.Full && p.valueChanges() > maxShownUpdates {
				fmt.Fprintln(out)
				fmt.Fprintln(out, t.dim("Pass --full to see how each test changes."))
			}
		}
		fmt.Fprintln(out)
	}

	for _, w := range p.Warnings() {
		fmt.Fprintln(out, t.yellow("Warning:")+" "+w)
	}
	if !p.Applied && p.ArchivesAll() {
		fmt.Fprintln(out, t.yellow("Warning:")+" this archives every test in code. `semaloop test sync` asks you to confirm it, or pass --allow-archive-all where it can't ask.")
	}
	p.writeSummary(out, t)
}

// writeSummary writes which files RemoveTakeOvers changed, if any, and the
// totals.
func (p *Plan) writeSummary(out io.Writer, t Theme) {
	if r := p.removedText(); r != "" {
		fmt.Fprintln(out, r)
	}
	fmt.Fprintln(out, p.totals(t))
}

// writeTable writes one line per changed test, in aligned columns.
func (p *Plan) writeTable(out io.Writer, t Theme) {
	var rows [][3]string
	var widths [3]int
	for _, ch := range p.Changes {
		if ch.Action == actionUnchanged {
			continue
		}
		r := [3]string{t.action(ch.Action, lookupAction(ch.Action).symbol+" "+ch.Identifier), p.detail(t, ch, true), t.dim(p.path(ch))}
		for i, c := range r {
			widths[i] = max(widths[i], ansi.StringWidth(c))
		}
		rows = append(rows, r)
	}
	for _, r := range rows {
		// Pad every column but the last that has something in it, so a test
		// without a file has no padding at the end of its line.
		last := len(r) - 1
		for last > 0 && r[last] == "" {
			last--
		}
		line := "  "
		for i := range last + 1 {
			line += r[i]
			if i < last {
				line += strings.Repeat(" ", widths[i]-ansi.StringWidth(r[i])+3)
			}
		}
		fmt.Fprintln(out, line)
	}
}

// writeValues writes each changed test, with how each of its fields changes
// below it and a blank line either side of those.
func (p *Plan) writeValues(out io.Writer, opts TextOptions) {
	t := opts.Theme
	shortened := false
	first, gap := true, false
	for _, ch := range p.Changes {
		if ch.Action == actionUnchanged {
			continue
		}
		values, short := p.valueLines(ch, opts)
		shortened = shortened || short
		if gap || (values != nil && !first) {
			fmt.Fprintln(out)
		}
		parts := []string{t.action(ch.Action, lookupAction(ch.Action).symbol+" "+ch.Identifier)}
		for _, part := range []string{p.detail(t, ch, values == nil), t.dim(p.path(ch))} {
			if part != "" {
				parts = append(parts, part)
			}
		}
		fmt.Fprintln(out, "  "+strings.Join(parts, "   "))
		for _, v := range values {
			fmt.Fprintln(out, v)
		}
		first, gap = false, values != nil
	}
	if shortened {
		fmt.Fprintln(out)
		fmt.Fprintln(out, t.dim("Some changes are shortened. Pass --full to see them in full."))
	}
}

// totals counts the changes, e.g. "1 to create, 2 unchanged.".
func (p *Plan) totals(t Theme) string {
	if !p.HasChanges() {
		return fmt.Sprintf("No changes, %d unchanged.", len(p.Changes))
	}
	var totals []string
	for action, n := range p.counts() {
		total := fmt.Sprintf("%d %s", n, lookupAction(action).label(p.Applied))
		if action != actionUnchanged {
			total = t.action(action, total)
		}
		totals = append(totals, total)
	}
	return strings.Join(totals, ", ") + "."
}

// counts yields each action in the plan, in the order they're shown, with how
// many tests it applies to. Unchanged tests are always counted.
func (p *Plan) counts() func(func(syncAction, int) bool) {
	return func(yield func(syncAction, int) bool) {
		counts := map[syncAction]int{actionUnchanged: 0}
		order := []syncAction{}
		for _, ch := range p.Changes {
			if _, ok := counts[ch.Action]; !ok {
				order = append(order, ch.Action)
			}
			counts[ch.Action]++
		}
		// Changes are sorted, so order is too, apart from unchanged, which
		// is always last.
		for _, action := range append(order, actionUnchanged) {
			if !yield(action, counts[action]) {
				return
			}
		}
	}
}

type jsonChange struct {
	Action            string   `json:"action"`
	Identifier        string   `json:"identifier"`
	Name              string   `json:"name,omitempty"`
	Fields            []string `json:"fields,omitempty"`
	Path              string   `json:"path,omitempty"`
	TestID            string   `json:"testId,omitempty"`
	TakeOverRedundant bool     `json:"takeOverRedundant,omitempty"`
}

type jsonApp struct {
	Name     string `json:"name"`
	BundleID string `json:"bundleId"`
}

type jsonPlan struct {
	Dir         string         `json:"dir"`
	App         jsonApp        `json:"app"`
	Revision    int            `json:"revision"`
	Applied     bool           `json:"applied"`
	ArchivesAll bool           `json:"archivesAll"`
	Changes     []jsonChange   `json:"changes"`
	Totals      map[string]int `json:"totals"`
	Warnings    []string       `json:"warnings"`
	// RemovedTakeOvers lists the files take_over_test_id was removed from.
	RemovedTakeOvers []string `json:"removedTakeOvers"`
}

// WriteJSON writes the plan for a program to read.
func (p *Plan) WriteJSON(out io.Writer) {
	res := jsonPlan{
		Dir:              p.Dir,
		App:              jsonApp{p.App.Name, p.App.BundleId},
		Revision:         p.Revision,
		Applied:          p.Applied,
		ArchivesAll:      p.ArchivesAll(),
		Changes:          []jsonChange{},
		Totals:           map[string]int{},
		Warnings:         append([]string{}, p.Warnings()...),
		RemovedTakeOvers: append([]string{}, p.RemovedTakeOvers...),
	}
	for _, ch := range p.Changes {
		c := jsonChange{
			Action:            string(ch.Action),
			Identifier:        ch.Identifier,
			Name:              p.name(ch),
			Path:              p.path(ch),
			TestID:            ch.TestId.Value,
			TakeOverRedundant: bool(ch.TakeOverRedundant.Value),
		}
		for _, f := range ch.Fields {
			c.Fields = append(c.Fields, snakeCase(string(f)))
		}
		res.Changes = append(res.Changes, c)
	}
	for action, n := range p.counts() {
		res.Totals[string(action)] = n
	}
	writeJSON(out, res)
}

// snakeCase turns the API's camelCase field names into the files' snake_case
// ones, e.g. disabledCapabilities into disabled_capabilities.
func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
