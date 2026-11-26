package cmd

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

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

// wantsValues reports whether the plan, written with full, would show the
// dashboard's values next to the files', which needs the dashboard's tests.
func (p *Plan) wantsValues(full bool) bool {
	n := p.valueChanges()
	return n > 0 && (full || n <= maxShownUpdates)
}

// setDashboard gives the plan the dashboard's tests, from before the plan is
// applied, so it can show what each changed field changes from.
func (p *Plan) setDashboard(tests []DashboardTest) *Plan {
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
			out = append(out, label+"  "+changed(t, quoteOr(remote.Folder, "none"), quoteOr(local.Folder, "not set")))
		case "draft":
			to := "not set"
			if local.Draft != nil {
				to = strconv.FormatBool(*local.Draft)
			}
			out = append(out, label+"  "+changed(t, strconv.FormatBool(remote.Draft), to))
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

// quoteOr quotes s, or is unset when s is empty.
func quoteOr(s, unset string) string {
	if s == "" {
		return unset
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

// row is one row of a text's changes, with how many of the text's edited,
// removed and added lines it shows.
type row struct {
	text                      string
	edited, deleted, inserted int
}

// textChanges shows how text changes: only the lines that change, and, for a
// line that's edited rather than rewritten, only the words that change and a
// few either side. Unless opts asks for it in full, it's cut to a few rows,
// each no wider than opts allows.
func textChanges(t Theme, from, to string, opts TextOptions) (out []string, shortened bool) {
	width := 0
	if opts.Width > 0 && !opts.Full {
		width = max(opts.Width-len(valueIndent), 1)
	}
	var rows []row
	var deleted, inserted []string
	// Lines past the rows that are shown are only counted, and can't be
	// told apart as edited, so they count as removed and added.
	var hidden row
	flush := func() {
		if opts.Full || len(rows) <= maxValueRows {
			rows = append(rows, hunkRows(t, deleted, inserted, width)...)
		} else {
			hidden.deleted += len(deleted)
			hidden.inserted += len(inserted)
		}
		deleted, inserted = nil, nil
	}
	for _, e := range diffTokens(lines(from), lines(to), false) {
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
		// lines ignores the spaces around the text, and line endings.
		return []string{valueIndent + t.dim("only spaces or line endings change")}, false
	}

	if !opts.Full && len(rows) > maxValueRows {
		for _, r := range rows[maxValueRows:] {
			hidden.edited += r.edited
			hidden.deleted += r.deleted
			hidden.inserted += r.inserted
		}
		rows = append(rows[:maxValueRows], row{text: t.dim("  … " + hiddenLines(hidden))})
		shortened = true
	}
	for _, r := range rows {
		out = append(out, valueIndent+r.text)
	}
	return out, shortened
}

// hiddenLines says how many lines' changes aren't shown, e.g. "3 more lines
// (1 edited, 2 added)".
func hiddenLines(r row) string {
	total := r.edited + r.deleted + r.inserted
	if total == 0 {
		// Only the rest of an edited line is hidden.
		return "more changes to the line above"
	}
	var kinds []string
	for _, k := range []struct {
		n    int
		what string
	}{{r.edited, "edited"}, {r.deleted, "removed"}, {r.inserted, "added"}} {
		if k.n > 0 {
			kinds = append(kinds, fmt.Sprintf("%d %s", k.n, k.what))
		}
	}
	return fmt.Sprintf("%s (%s)", plural(total, "more line"), strings.Join(kinds, ", "))
}

// maxPairings bounds how many pairs of lines hunkRows compares to find the
// ones that are edited. With more, each line is only compared with the one
// in its place.
const maxPairings = 100

// hunkRows shows lines that are replaced by others. A removed line that's
// edited into one of the added lines, in the same order, is shown once, with
// its changed words marked, in rows no wider than width. The rest are shown
// as removed, then added.
func hunkRows(t Theme, deleted, inserted []string, width int) []row {
	var rows []row
	var removed []string
	flush := func(added []string) {
		for _, l := range removed {
			rows = append(rows, row{text: t.red("- " + l), deleted: 1})
		}
		for _, l := range added {
			rows = append(rows, row{text: t.green("+ " + l), inserted: 1})
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
				for n, text := range inlineRows(t, edits, width) {
					r := row{text: text}
					if n == 0 {
						r.edited = 1
					}
					rows = append(rows, r)
				}
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
// either side, in rows no wider than width, unless it's zero. Changes close
// together share a row.
func inlineRows(t Theme, edits []edit, width int) []string {
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

		// Join runs of the same kind, so each change is marked once.
		var runs []edit
		for _, e := range edits[start:stop] {
			if n := len(runs); n > 0 && runs[n-1].kind == e.kind {
				runs[n-1].text += e.text
			} else {
				runs = append(runs, e)
			}
		}
		rows = append(rows, renderRuns(t, runs, start > 0, stop < len(edits), width))
		i = end
	}
	return rows
}

// renderRuns writes an edited line's runs, with an ellipsis for what's left
// out at either end. When they're wider than width, the run that doesn't fit
// is cut short, inside its marks, so it's still clear where a change ends.
func renderRuns(t Theme, runs []edit, lead, trail bool, width int) string {
	style := func(kind editKind, s string) string {
		switch kind {
		case editDelete:
			return t.removed(s)
		case editInsert:
			return t.added(s)
		default:
			return s
		}
	}
	var b strings.Builder
	left := width
	if lead {
		b.WriteString(t.dim("…"))
		left--
	}
	if trail {
		left--
	}
	for _, r := range runs {
		// Without colours, a change's marks take room too.
		marks := ansi.StringWidth(style(r.kind, ""))
		if width == 0 || ansi.StringWidth(r.text)+marks <= left {
			b.WriteString(style(r.kind, r.text))
			left -= ansi.StringWidth(r.text) + marks
			continue
		}
		if trail {
			left++
		}
		if room := left - marks; room > 0 {
			b.WriteString(style(r.kind, ansi.Truncate(r.text, room, "…")))
		} else {
			b.WriteString(t.dim("…"))
		}
		return b.String()
	}
	if trail {
		b.WriteString(t.dim("…"))
	}
	return b.String()
}
