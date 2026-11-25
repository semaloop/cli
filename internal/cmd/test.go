package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/log"
	"github.com/semaloop/cli/internal/api"
	"github.com/semaloop/cli/internal/client"
	"github.com/semaloop/cli/internal/testfile"
)

// ErrInvalidTests is returned when test files fail their checks. The errors
// themselves have already been written out.
var ErrInvalidTests = errors.New("test files have errors")

// CheckTests loads the test files under dir and writes every error to out, one
// per line as `file:line: message`. It returns ErrInvalidTests if there were
// any.
func CheckTests(dir string, out io.Writer) ([]testfile.Test, error) {
	tests, errs, err := testfile.Load(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range errs {
		fmt.Fprintln(out, e.Error())
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%w: found %s", ErrInvalidTests, plural(len(errs), "error"))
	}
	return tests, nil
}

// SyncOptions configures SyncTests.
type SyncOptions struct {
	// DryRun checks the files but makes no network calls.
	DryRun bool
	// AllowArchiveAll lets a sync archive every test in code.
	AllowArchiveAll bool
}

// SyncResult is the outcome of a sync.
type SyncResult struct {
	Revision int
	Changes  []api.SyncTestChange
}

// SyncTests checks the test files under dir and, if they pass, makes the
// app's tests in code match them. Check errors and the applied changes are
// written to out.
func SyncTests(ctx context.Context, apiKey, serverURL, dir string, out io.Writer, opts SyncOptions) (SyncResult, error) {
	tests, err := CheckTests(dir, out)
	if err != nil {
		return SyncResult{}, err
	}
	log.Debug("Test files are valid.", "dir", dir, "count", len(tests))

	if opts.DryRun {
		log.Info("Dry run: test files checked; skipping sync.", "dir", dir, "tests", len(tests))
		return SyncResult{}, nil
	}

	c, err := api.NewClient(serverURL, api.WithClient(&client.AuthClient{APIKey: apiKey}))
	if err != nil {
		return SyncResult{}, fmt.Errorf("could not initialise API client: %w", err)
	}

	req := api.PostSyncTestsReq{
		Tests:           make([]api.ManagedTestInput, 0, len(tests)),
		AllowArchiveAll: api.NewOptBool(opts.AllowArchiveAll),
	}
	for _, t := range tests {
		req.Tests = append(req.Tests, syncInput(t))
	}

	log.Info("Syncing tests.", "dir", dir, "tests", len(tests))
	res, err := c.PostSyncTests(ctx, api.NewOptPostSyncTestsReq(req))
	if err != nil {
		switch code := statusCode(err); {
		case code == http.StatusForbidden:
			return SyncResult{}, client.ErrForbidden
		case code >= 500:
			// A server that failed partway may have applied part of the
			// sync.
			return SyncResult{}, fmt.Errorf("unexpected response from Semaloop: %d %s, so the sync may have been applied", code, http.StatusText(code))
		case code != 0:
			// The server was reached but answered with a status the client
			// doesn't expect, so this isn't a connection problem.
			return SyncResult{}, fmt.Errorf("unexpected response from Semaloop: %d %s", code, http.StatusText(code))
		}
		// net/http reports every transport failure as a *url.Error. Anything
		// else happened after the server answered, so it may have applied
		// the sync.
		if urlErr := (*url.Error)(nil); errors.As(err, &urlErr) {
			return SyncResult{}, fmt.Errorf("could not connect to Semaloop: %w", err)
		}
		return SyncResult{}, fmt.Errorf("could not read the response from Semaloop, so the sync may have been applied: %w", err)
	}

	switch r := res.(type) {
	case *api.SyncTestsResponse:
		result := SyncResult{Revision: r.Revision, Changes: r.Changes}
		writeChanges(out, dir, tests, result.Changes)
		return result, nil
	case *api.PostSyncTestsBadRequest:
		return SyncResult{}, fmt.Errorf("sync rejected: %s", errorMessages(r.Errors))
	case *api.PostSyncTestsForbidden:
		return SyncResult{}, client.ErrForbidden
	case *api.PostSyncTestsConflict:
		return SyncResult{}, fmt.Errorf("sync conflict: %s", errorMessages(r.Errors))
	case *api.PostSyncTestsUnauthorized:
		return SyncResult{}, client.ErrUnauthorized
	default:
		return SyncResult{}, fmt.Errorf("unexpected response from server (%T)", res)
	}
}

// syncInput maps a test file to the sync request's fields.
func syncInput(t testfile.Test) api.ManagedTestInput {
	in := api.ManagedTestInput{
		Identifier:  t.Identifier,
		Name:        t.Name,
		Description: t.Description,
		// A left-out tags or disabled_capabilities means none, so send an
		// empty list rather than leaving the field out. Only secrets treats
		// a missing field differently from an empty one.
		Tags:                 orEmpty(t.Tags),
		Secrets:              t.Secrets,
		DisabledCapabilities: capabilities(t.DisabledCapabilities),
	}
	if t.Folder != "" {
		in.Folder = api.NewOptNilString(t.Folder)
	}
	if t.Draft != nil {
		in.Draft = api.NewOptBool(*t.Draft)
	}
	if t.TakeOverTestID != "" {
		in.TakeOverTestId = api.NewOptString(t.TakeOverTestID)
	}
	return in
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// capabilities maps the files' snake_case capability names, e.g.
// audio_input, to the API's camelCase ones, e.g. audioInput.
func capabilities(names []string) []api.TestCapabilityName {
	caps := make([]api.TestCapabilityName, len(names))
	for i, n := range names {
		caps[i] = api.TestCapabilityName(camelCase(n))
	}
	return caps
}

func camelCase(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func errorMessages(errs []api.ErrorResponseErrorsItem) string {
	var msgs []string
	for _, e := range errs {
		if e.Message != "" {
			msgs = append(msgs, e.Message)
		}
	}
	if len(msgs) == 0 {
		return "no reason given"
	}
	return strings.Join(msgs, "; ")
}

type changeAction struct {
	action api.SyncTestChangeAction
	symbol string
	label  string
}

// actions lists the changes a sync can make, in the order totals are given,
// with the symbol and label each is shown with.
var actions = []changeAction{
	{api.SyncTestChangeActionCreate, "+", "created"},
	{api.SyncTestChangeActionUpdate, "~", "updated"},
	{api.SyncTestChangeActionTakeOver, "~", "taken over"},
	{api.SyncTestChangeActionArchive, "-", "archived"},
}

// lookupAction returns how to show action. An action this version of the CLI
// doesn't know is shown by its name, so no change goes unreported.
func lookupAction(action api.SyncTestChangeAction) changeAction {
	if i := slices.IndexFunc(actions, func(a changeAction) bool { return a.action == action }); i >= 0 {
		return actions[i]
	}
	return changeAction{action, "?", strings.ReplaceAll(string(action), "_", " ")}
}

// writeChanges lists what changed, one test per line, followed by totals.
// Unchanged tests are only counted.
func writeChanges(out io.Writer, dir string, tests []testfile.Test, changes []api.SyncTestChange) {
	paths := map[string]string{}
	for _, t := range tests {
		if rel, err := filepath.Rel(dir, t.Path); err == nil {
			paths[t.Identifier] = rel
		}
	}

	counts := map[api.SyncTestChangeAction]int{}
	for _, ch := range changes {
		counts[ch.Action]++
		if ch.Action == api.SyncTestChangeActionUnchanged {
			continue
		}
		a := lookupAction(ch.Action)
		detail := a.label
		if len(ch.Fields) > 0 {
			names := make([]string, len(ch.Fields))
			for j, f := range ch.Fields {
				names[j] = snakeCase(string(f))
			}
			detail += ": " + strings.Join(names, ", ")
		}
		line := fmt.Sprintf("  %s %s  %s", a.symbol, ch.Identifier, detail)
		if p, ok := paths[ch.Identifier]; ok {
			line += "  " + p
		}
		fmt.Fprintln(out, line)
	}

	var totals []string
	for _, a := range actions {
		if n := counts[a.action]; n > 0 {
			totals = append(totals, fmt.Sprintf("%d %s", n, a.label))
		}
		delete(counts, a.action)
	}
	// Actions this version of the CLI doesn't know, in a stable order.
	unchanged := counts[api.SyncTestChangeActionUnchanged]
	delete(counts, api.SyncTestChangeActionUnchanged)
	for _, action := range slices.Sorted(maps.Keys(counts)) {
		totals = append(totals, fmt.Sprintf("%d %s", counts[action], lookupAction(action).label))
	}
	totals = append(totals, fmt.Sprintf("%d unchanged", unchanged))
	fmt.Fprintln(out, strings.Join(totals, ", ")+".")
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

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
