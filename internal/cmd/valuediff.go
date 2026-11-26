package cmd

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/semaloop/cli/internal/api"
)

// TextOptions configures how a plan is written for a person to read.
type TextOptions struct {
	Theme Theme
	// Width is how wide a line can be before it's shortened. Zero means no
	// limit.
	Width int
	// Full shows every change in full, however long.
	Full bool
}

const (
	// maxShownUpdates is how many changed tests have their values shown,
	// unless the options ask for them in full. Any more, and the plan is too
	// long to read, so it only names the fields.
	maxShownUpdates = 5
	// maxValueRows is how many rows a field's changes are shown in, unless
	// the options ask for them in full.
	maxValueRows = 5
	// contextTokens is how much of a changed line is shown either side of a
	// change: words and the spaces between them.
	contextTokens = 8

	valueIndent = "      "
)

// hasValues reports whether a change can show the values it changes.
func hasValues(ch api.SyncTestChange) bool {
	return (ch.Action == actionUpdate || ch.Action == actionTakeOver) && len(ch.Fields) > 0
}

// valueChanges counts the changes that can show values.
func (p *Plan) valueChanges() int {
	n := 0
	for _, ch := range p.Changes {
		if hasValues(ch) {
			n++
		}
	}
	return n
}

// WantsValues reports whether the plan, written with full, would show the
// dashboard's values next to the files', which needs the dashboard's tests.
func (p *Plan) WantsValues(full bool) bool {
	n := p.valueChanges()
	return n > 0 && (full || n <= maxShownUpdates)
}

// WithDashboard gives the plan the dashboard's tests, from before the plan is
// applied, so it can show what each changed field changes from.
func (p *Plan) WithDashboard(tests []DashboardTest) *Plan {
	p.dashboard = tests
	return p
}

// dashboardTest is the test ch changes, as it is in the dashboard.
func (p *Plan) dashboardTest(ch api.SyncTestChange) (DashboardTest, bool) {
	if id := ch.TestId.Value; id != "" {
		if i := slices.IndexFunc(p.dashboard, func(t DashboardTest) bool { return t.ID == id }); i >= 0 {
			return p.dashboard[i], true
		}
	}
	if ch.Action == actionUpdate {
		if i := slices.IndexFunc(p.dashboard, func(t DashboardTest) bool { return t.ExternalID == ch.Identifier }); i >= 0 {
			return p.dashboard[i], true
		}
	}
	return DashboardTest{}, false
}

// valueLines shows how each of ch's fields changes, a few lines per field,
// or nothing when the values aren't known. shortened is true when some of
// the change isn't shown.
func (p *Plan) valueLines(ch api.SyncTestChange, opts TextOptions) (out []string, shortened bool) {
	if !hasValues(ch) {
		return nil, false
	}
	remote, ok := p.dashboardTest(ch)
	local, inCode := p.tests[ch.Identifier]
	if !ok || !inCode {
		return nil, false
	}
	t := opts.Theme
	for _, f := range ch.Fields {
		label := valueIndent + t.yellow(snakeCase(string(f)))
		switch f {
		case "name":
			out = append(out, label+"  "+changed(t, strconv.Quote(remote.Name), strconv.Quote(local.Name)))
		case "folder":
			out = append(out, label+"  "+changed(t, orNone(remote.Folder), orNone(local.Folder)))
		case "draft":
			out = append(out, label+"  "+changed(t, strconv.FormatBool(remote.Draft), strconv.FormatBool(local.Draft != nil && *local.Draft)))
		case "tags":
			out = append(out, label+"  "+listChanges(t, remote.Tags, local.Tags))
		case "secrets":
			out = append(out, label+"  "+listChanges(t, remote.Secrets, local.Secrets))
		case "disabledCapabilities":
			out = append(out, label+"  "+listChanges(t, remote.DisabledCapabilities, local.DisabledCapabilities))
		case "description":
			rows, short := textChanges(t, remote.Description, local.Description, opts)
			out = append(append(out, label), rows...)
			shortened = shortened || short
		default:
			// A field this version of the CLI doesn't know: name it, at
			// least.
			out = append(out, label)
		}
	}
	if width := opts.Width; width > 0 && !opts.Full {
		for i, l := range out {
			if ansi.StringWidth(l) > width {
				out[i] = ansi.Truncate(l, width, "…")
				shortened = true
			}
		}
	}
	return out, shortened
}

func changed(t Theme, from, to string) string {
	return t.red(from) + " → " + t.green(to)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return strconv.Quote(s)
}

// listChanges shows what's added to and removed from a list, e.g.
// "+smoke -flaky".
func listChanges(t Theme, from, to []string) string {
	var parts []string
	for _, s := range to {
		if !slices.Contains(from, s) {
			parts = append(parts, t.green("+"+s))
		}
	}
	for _, s := range from {
		if !slices.Contains(to, s) {
			parts = append(parts, t.red("-"+s))
		}
	}
	if len(parts) == 0 {
		return t.dim("reordered")
	}
	return strings.Join(parts, " ")
}

// textChanges shows how text changes: only the lines that change, and, for a
// line that's edited rather than rewritten, only the words that change and a
// few either side. Unless opts asks for it in full, it's cut to a few rows,
// each no wider than opts allows.
func textChanges(t Theme, from, to string, opts TextOptions) (rows []string, shortened bool) {
	a, b := lines(from), lines(to)
	edits, ok := diffTokens(a, b)
	if !ok {
		edits = nil
		for _, l := range a {
			edits = append(edits, edit{editDelete, l})
		}
		for _, l := range b {
			edits = append(edits, edit{editInsert, l})
		}
	}

	var deleted, inserted []string
	flush := func() {
		rows = append(rows, hunkRows(t, deleted, inserted)...)
		deleted, inserted = nil, nil
	}
	for _, e := range edits {
		switch e.kind {
		case editEqual:
			flush()
		case editDelete:
			deleted = append(deleted, e.text)
		case editInsert:
			inserted = append(inserted, e.text)
		}
	}
	flush()
	if len(rows) == 0 {
		// The text only differs in the spaces around it.
		rows = []string{t.dim("only spaces at the start or end change")}
	}

	if opts.Full {
		return indent(rows), false
	}
	if len(rows) > maxValueRows {
		hidden := len(rows) - maxValueRows
		rows = append(rows[:maxValueRows], t.dim(fmt.Sprintf("  … %s", plural(hidden, "more line"))))
		shortened = true
	}
	return indent(rows), shortened
}

func indent(rows []string) []string {
	for i, r := range rows {
		rows[i] = valueIndent + r
	}
	return rows
}

// maxPairings bounds how many pairs of lines hunkRows compares to find the
// ones that are edited. With more, each line is only compared with the one
// in its place.
const maxPairings = 100

// hunkRows shows lines that are replaced by others. A removed line that's
// edited into one of the added lines, in the same order, is shown once, with
// its changed words marked. The rest are shown as removed, then added.
func hunkRows(t Theme, deleted, inserted []string) []string {
	var rows, removed []string
	flush := func(added []string) {
		for _, l := range removed {
			rows = append(rows, t.red("- "+l))
		}
		for _, l := range added {
			rows = append(rows, t.green("+ "+l))
		}
		removed = nil
	}
	pair := len(deleted)*len(inserted) <= maxPairings
	j := 0
	for i, d := range deleted {
		paired := false
		// Without comparing every pair, compare each line with the one in
		// its place.
		k := j
		if !pair {
			k = max(j, i)
		}
		for ; k < len(inserted) && (pair || k == max(j, i)); k++ {
			if edits, ok := wordDiff(d, inserted[k]); ok {
				flush(inserted[j:k])
				rows = append(rows, inlineRows(t, edits)...)
				j, paired = k+1, true
				break
			}
		}
		if !paired {
			removed = append(removed, d)
		}
	}
	flush(inserted[j:])
	return rows
}

// inlineRows shows an edited line one change at a time, each with a few words
// either side. Changes close together share a row.
func inlineRows(t Theme, edits []edit) []string {
	var rows []string
	for i := 0; i < len(edits); {
		first := i
		for first < len(edits) && edits[first].kind == editEqual {
			first++
		}
		if first == len(edits) {
			break
		}
		// The row ends after the last change that isn't far from the one
		// before it.
		end := first
		for k := first; k < len(edits) && k-end < 2*contextTokens; k++ {
			if edits[k].kind != editEqual {
				end = k + 1
			}
		}
		start, stop := max(0, first-contextTokens), min(len(edits), end+contextTokens)
		for start < first && isSpace(edits[start].text) {
			start++
		}
		for stop > end && isSpace(edits[stop-1].text) {
			stop--
		}

		var b strings.Builder
		if start > 0 {
			b.WriteString(t.dim("…"))
		}
		for k := start; k < stop; {
			kind, run := edits[k].kind, ""
			for ; k < stop && edits[k].kind == kind; k++ {
				run += edits[k].text
			}
			switch kind {
			case editEqual:
				b.WriteString(run)
			case editDelete:
				b.WriteString(t.removed(run))
			case editInsert:
				b.WriteString(t.added(run))
			}
		}
		if stop < len(edits) {
			b.WriteString(t.dim("…"))
		}
		rows = append(rows, b.String())
		i = end
	}
	return rows
}

func isSpace(s string) bool {
	return strings.TrimFunc(s, unicode.IsSpace) == ""
}
