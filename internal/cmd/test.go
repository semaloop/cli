package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
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

	req := &api.SyncTestsRequest{
		Tests:           make([]api.SyncTestInput, 0, len(tests)),
		AllowArchiveAll: api.NewOptBool(opts.AllowArchiveAll),
	}
	for _, t := range tests {
		req.Tests = append(req.Tests, syncInput(t))
	}

	log.Info("Syncing tests.", "dir", dir, "tests", len(tests))
	res, err := c.PostSyncTests(ctx, req)
	if err != nil {
		if statusCode(err) == http.StatusForbidden {
			return SyncResult{}, client.ErrForbidden
		}
		return SyncResult{}, fmt.Errorf("could not connect to Semaloop: %w", err)
	}

	switch r := res.(type) {
	case *api.PostSyncTestsOK:
		result := SyncResult{Revision: r.Result.Revision, Changes: r.Result.Changes}
		writeChanges(out, dir, tests, result.Changes)
		return result, nil
	case *api.PostSyncTestsBadRequest:
		return SyncResult{}, fmt.Errorf("sync rejected: %s", errorMessages(r.Errors))
	case *api.PostSyncTestsConflict:
		return SyncResult{}, fmt.Errorf("sync conflict: %s", errorMessages(r.Errors))
	case *api.PostSyncTestsUnauthorized:
		return SyncResult{}, client.ErrUnauthorized
	default:
		return SyncResult{}, fmt.Errorf("unexpected response from server (%T)", res)
	}
}

// syncInput maps a test file to the sync request's fields.
func syncInput(t testfile.Test) api.SyncTestInput {
	in := api.SyncTestInput{
		Identifier:           t.Identifier,
		Name:                 t.Name,
		Description:          t.Description,
		Tags:                 t.Tags,
		Secrets:              t.Secrets,
		DisabledCapabilities: t.DisabledCapabilities,
	}
	if t.Folder != "" {
		in.Folder = api.NewOptString(t.Folder)
	}
	if t.Draft != nil {
		in.Draft = api.NewOptBool(*t.Draft)
	}
	if t.TakeOverTestID != "" {
		in.TakeOverTestId = api.NewOptString(t.TakeOverTestID)
	}
	return in
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
		var symbol, detail string
		switch ch.Action {
		case api.SyncTestChangeActionCreate:
			symbol, detail = "+", "created"
		case api.SyncTestChangeActionUpdate:
			symbol, detail = "~", "updated"
		case api.SyncTestChangeActionTakeOver:
			symbol, detail = "~", "taken over"
		case api.SyncTestChangeActionArchive:
			symbol, detail = "-", "archived"
		default:
			continue
		}
		if len(ch.Fields) > 0 {
			names := make([]string, len(ch.Fields))
			for i, f := range ch.Fields {
				names[i] = snakeCase(f)
			}
			detail += ": " + strings.Join(names, ", ")
		}
		line := fmt.Sprintf("  %s %s  %s", symbol, ch.Identifier, detail)
		if p, ok := paths[ch.Identifier]; ok {
			line += "  " + p
		}
		fmt.Fprintln(out, line)
	}

	var totals []string
	for _, a := range []struct {
		action api.SyncTestChangeAction
		label  string
	}{
		{api.SyncTestChangeActionCreate, "created"},
		{api.SyncTestChangeActionUpdate, "updated"},
		{api.SyncTestChangeActionTakeOver, "taken over"},
		{api.SyncTestChangeActionArchive, "archived"},
	} {
		if n := counts[a.action]; n > 0 {
			totals = append(totals, fmt.Sprintf("%d %s", n, a.label))
		}
	}
	totals = append(totals, fmt.Sprintf("%d unchanged", counts[api.SyncTestChangeActionUnchanged]))
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
