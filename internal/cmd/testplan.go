package cmd

import (
	"cmp"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/semaloop/cli/internal/api"
	"github.com/semaloop/cli/internal/testfile"
)

// appLabel names the app a plan is for.
//
// TODO: name the app and its bundle id, once the sync response includes them.
const appLabel = "the app for your API key"

// Plan is what a sync would change, or, once applied, what it changed.
type Plan struct {
	Dir      string
	Revision int
	// Applied is true when the changes have been made.
	Applied bool
	// Changes has one change per test, grouped by action in the order
	// actions lists them.
	Changes []api.SyncTestChange

	tests map[string]testfile.Test
}

func newPlan(dir string, tests []testfile.Test, revision int, applied bool, changes []api.SyncTestChange) *Plan {
	p := &Plan{
		Dir:      dir,
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
	for _, ch := range p.Changes {
		if ch.TakeOverRedundant.Value {
			warnings = append(warnings, fmt.Sprintf(
				"%s is already in code, so take_over_test_id can be removed from %s.",
				ch.Identifier, cmp.Or(p.path(ch), "its file")))
		}
	}
	return warnings
}

// name is the test's name, or empty when it has no file.
func (p *Plan) name(ch api.SyncTestChange) string {
	return p.tests[ch.Identifier].Name
}

// path is the test's file, relative to the plan's directory, or empty when
// there's no file.
func (p *Plan) path(ch api.SyncTestChange) string {
	t, ok := p.tests[ch.Identifier]
	if !ok {
		return ""
	}
	if rel, err := filepath.Rel(p.Dir, t.Path); err == nil {
		return rel
	}
	return t.Path
}

func (p *Plan) detail(ch api.SyncTestChange) string {
	fields := make([]string, len(ch.Fields))
	for i, f := range ch.Fields {
		fields[i] = snakeCase(string(f))
	}
	switch ch.Action {
	case actionCreate:
		return p.name(ch)
	case actionUpdate:
		return strings.Join(fields, ", ")
	case actionTakeOver:
		d := "take over " + cmp.Or(ch.TestId.Value, "dashboard test")
		if len(fields) > 0 {
			d += ": " + strings.Join(fields, ", ")
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
// line per changed test, any warnings, then totals.
func (p *Plan) WriteText(out io.Writer) {
	fmt.Fprintf(out, "%s → %s\n\n", p.Dir, appLabel)

	if p.HasChanges() {
		var b strings.Builder
		tw := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
		for _, ch := range p.Changes {
			if ch.Action == actionUnchanged {
				continue
			}
			fmt.Fprintf(tw, "  %s %s\t%s\t%s\n", lookupAction(ch.Action).symbol, ch.Identifier, p.detail(ch), p.path(ch))
		}
		tw.Flush()
		// A test without a file leaves padding at the end of its line.
		for line := range strings.Lines(b.String()) {
			fmt.Fprintln(out, strings.TrimRight(line, " \n"))
		}
		fmt.Fprintln(out)
	}

	for _, w := range p.Warnings() {
		fmt.Fprintln(out, "Warning: "+w)
	}
	if !p.Applied && p.ArchivesAll() {
		fmt.Fprintln(out, "Warning: this archives every test in code. `semaloop test sync` asks you to confirm it, or pass --allow-archive-all where it can't ask.")
	}
	fmt.Fprintln(out, p.Totals())
}

// Totals counts the changes, e.g. "1 to create, 2 unchanged.".
func (p *Plan) Totals() string {
	if !p.HasChanges() {
		return fmt.Sprintf("No changes, %d unchanged.", len(p.Changes))
	}
	var totals []string
	for action, n := range p.counts() {
		totals = append(totals, fmt.Sprintf("%d %s", n, lookupAction(action).label(p.Applied)))
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

type jsonPlan struct {
	Dir         string         `json:"dir"`
	Revision    int            `json:"revision"`
	Applied     bool           `json:"applied"`
	ArchivesAll bool           `json:"archivesAll"`
	Changes     []jsonChange   `json:"changes"`
	Totals      map[string]int `json:"totals"`
	Warnings    []string       `json:"warnings"`
}

// WriteJSON writes the plan for a program to read.
func (p *Plan) WriteJSON(out io.Writer) {
	res := jsonPlan{
		Dir:         p.Dir,
		Revision:    p.Revision,
		Applied:     p.Applied,
		ArchivesAll: p.ArchivesAll(),
		Changes:     []jsonChange{},
		Totals:      map[string]int{},
		Warnings:    append([]string{}, p.Warnings()...),
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
