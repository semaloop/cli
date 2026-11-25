package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/log"
	"github.com/semaloop/cli/internal/api"
	"github.com/semaloop/cli/internal/client"
	"github.com/semaloop/cli/internal/testfile"
)

// ErrInvalidTests is returned when test files fail their checks. The errors
// themselves have already been written out.
var ErrInvalidTests = errors.New("test files have errors")

// ErrConflict is returned when the API answers a sync with a 409: another
// sync is running, or the app's tests have changed since the plan the sync
// was based on.
var ErrConflict = errors.New("sync conflict")

// ErrCancelled is returned when a sync isn't confirmed.
var ErrCancelled = errors.New("sync cancelled, nothing was changed")

// ErrUnnamedApp is returned when a plan archives every test in code but the
// API didn't name the app, so there's nothing to ask a person to type.
var ErrUnnamedApp = errors.New("can't confirm archiving every test in code: Semaloop didn't name the app for your API key, so nothing was changed")

// CheckTests loads the test files under dir and writes every error to out.
// As text, that's one per line as `file:line: message`. As JSON, it's an
// object with the number of valid tests and the errors. It returns
// ErrInvalidTests if there were any.
func CheckTests(dir string, out io.Writer, asJSON bool) ([]testfile.Test, error) {
	return loadTests(dir, out, asJSON, true)
}

// LoadTests is CheckTests for commands with results of their own: it only
// writes to out when there are errors.
func LoadTests(dir string, out io.Writer, asJSON bool) ([]testfile.Test, error) {
	return loadTests(dir, out, asJSON, false)
}

func loadTests(dir string, out io.Writer, asJSON, always bool) ([]testfile.Test, error) {
	tests, errs, err := testfile.Load(dir)
	if err != nil {
		return nil, err
	}
	if !always && len(errs) == 0 {
		return tests, nil
	}
	if asJSON {
		writeCheckJSON(out, tests, errs)
	} else {
		for _, e := range errs {
			fmt.Fprintln(out, e.Error())
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%w: found %s", ErrInvalidTests, plural(len(errs), "error"))
	}
	return tests, nil
}

type jsonCheckError struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

func writeCheckJSON(out io.Writer, tests []testfile.Test, errs []testfile.Error) {
	res := struct {
		Tests  int              `json:"tests"`
		Errors []jsonCheckError `json:"errors"`
	}{Errors: []jsonCheckError{}}
	if len(errs) == 0 {
		res.Tests = len(tests)
	}
	for _, e := range errs {
		res.Errors = append(res.Errors, jsonCheckError{e.Path, e.Line, e.Message})
	}
	writeJSON(out, res)
}

func writeJSON(out io.Writer, v any) {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	// Encoding these types can't fail.
	_ = enc.Encode(v)
}

// SyncClient plans and applies syncs of one directory of test files.
type SyncClient struct {
	api   *api.Client
	dir   string
	tests []testfile.Test
}

// NewSyncClient returns a client that syncs tests, the checked contents of
// dir.
func NewSyncClient(apiKey, serverURL, dir string, tests []testfile.Test) (*SyncClient, error) {
	c, err := api.NewClient(serverURL, api.WithClient(&client.AuthClient{APIKey: apiKey}))
	if err != nil {
		return nil, fmt.Errorf("could not initialise API client: %w", err)
	}
	return &SyncClient{api: c, dir: dir, tests: tests}, nil
}

// Plan asks the API what a sync would change, without changing anything.
func (s *SyncClient) Plan(ctx context.Context) (*Plan, error) {
	log.Debug("Planning sync.", "dir", s.dir, "tests", len(s.tests))
	// A dry run never writes, so allow archiving every test in code. The
	// plan then shows what would be archived, and the CLI decides whether
	// the sync needs extra confirmation.
	return s.post(ctx, true, api.OptInt{}, true)
}

// ApplyOptions configures SyncClient.Apply.
type ApplyOptions struct {
	// BaseRevision, when set, makes the API reject the sync with a 409 if the
	// app's tests have changed since the plan with this revision.
	BaseRevision api.OptInt
	// AllowArchiveAll lets the sync archive every test in code.
	AllowArchiveAll bool
}

// Apply makes the app's tests in code match the files.
func (s *SyncClient) Apply(ctx context.Context, opts ApplyOptions) (*Plan, error) {
	log.Info("Syncing tests.", "dir", s.dir, "tests", len(s.tests))
	return s.post(ctx, false, opts.BaseRevision, opts.AllowArchiveAll)
}

func (s *SyncClient) post(ctx context.Context, dryRun bool, baseRevision api.OptInt, allowArchiveAll bool) (*Plan, error) {
	req := api.PostSyncTestsReq{
		Tests:           make([]api.ManagedTestInput, 0, len(s.tests)),
		AllowArchiveAll: api.NewOptBool(allowArchiveAll),
		BaseRevision:    baseRevision,
	}
	if dryRun {
		req.DryRun = api.NewOptBool(true)
	}
	for _, t := range s.tests {
		req.Tests = append(req.Tests, syncInput(t))
	}

	// Whether any of the request reached the connection. Until it has, a
	// failure means the server can't have seen the sync.
	var sent atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteHeaders: func() { sent.Store(true) },
	})

	res, err := s.api.PostSyncTests(ctx, api.NewOptPostSyncTestsReq(req))
	if err != nil {
		switch code := statusCode(err); {
		case code == http.StatusForbidden:
			return nil, client.ErrForbidden
		case code != 0:
			// The server was reached but answered with a status the client
			// doesn't expect, so this isn't a connection problem.
			return nil, fmt.Errorf("unexpected response from Semaloop: %d %s", code, http.StatusText(code))
		}
		// A failure before the request was sent, such as a failed dial,
		// can't have changed anything. Once it's been sent, the server may
		// have applied the sync before the connection broke, or before an
		// answer that couldn't be read.
		if !sent.Load() {
			return nil, fmt.Errorf("could not connect to Semaloop: %w", err)
		}
		if dryRun {
			return nil, fmt.Errorf("could not get a response from Semaloop: %w", err)
		}
		return nil, fmt.Errorf("could not get a response from Semaloop, so the sync may have been applied: %w", err)
	}

	switch r := res.(type) {
	case *api.SyncTestsResponse:
		return newPlan(s.dir, r.App, s.tests, r.Revision, !dryRun, r.Changes), nil
	case *api.PostSyncTestsBadRequest:
		return nil, fmt.Errorf("sync rejected: %s", errorMessages(r.Errors))
	case *api.PostSyncTestsForbidden:
		return nil, client.ErrForbidden
	case *api.PostSyncTestsConflict:
		return nil, fmt.Errorf("%w: %s", ErrConflict, errorMessages(r.Errors))
	case *api.PostSyncTestsUnauthorized:
		return nil, client.ErrUnauthorized
	default:
		return nil, fmt.Errorf("unexpected response from server (%T)", res)
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

// DiffOptions configures Diff.
type DiffOptions struct {
	// JSON writes the plan as JSON.
	JSON bool
	// Hint ends a plan with changes by saying how to apply it.
	Hint bool
}

// Diff writes what a sync would change to out, without changing anything.
func Diff(ctx context.Context, s *SyncClient, out io.Writer, opts DiffOptions) (*Plan, error) {
	p, err := s.Plan(ctx)
	if err != nil {
		return nil, err
	}
	if opts.JSON {
		p.WriteJSON(out)
	} else {
		p.WriteText(out)
		if opts.Hint && p.HasChanges() {
			fmt.Fprintln(out, "Run `semaloop test sync` to apply.")
		}
	}
	return p, nil
}

// SyncOptions configures Sync.
type SyncOptions struct {
	// Confirm makes Sync show the plan and ask before applying it. Otherwise
	// it applies straight away, in one request.
	Confirm bool
	// In is where answers are read from, and Prompt where questions are
	// written, when Confirm is set.
	In     io.Reader
	Prompt io.Writer
	// AllowArchiveAll lets a sync archive every test in code without being
	// asked to confirm it.
	AllowArchiveAll bool
	// JSON writes the result as JSON. It can't be used with Confirm.
	JSON bool
}

// Sync makes the app's tests in code match the files, and writes the result
// to out.
//
// With Confirm set, it plans first, shows the plan and asks before applying.
// It then applies with the plan's revision as the base, so what's applied is
// exactly what was confirmed. If another sync has landed in between, it shows
// the new plan and asks again.
func Sync(ctx context.Context, s *SyncClient, out io.Writer, opts SyncOptions) (*Plan, error) {
	if !opts.Confirm {
		p, err := s.Apply(ctx, ApplyOptions{AllowArchiveAll: opts.AllowArchiveAll})
		if err != nil {
			return nil, err
		}
		if opts.JSON {
			p.WriteJSON(out)
		} else {
			p.WriteText(out)
		}
		return p, nil
	}

	in := bufio.NewReader(opts.In)
	for {
		plan, err := s.Plan(ctx)
		if err != nil {
			return nil, err
		}
		plan.WriteText(out)
		if !plan.HasChanges() {
			return plan, nil
		}

		allowArchiveAll := opts.AllowArchiveAll
		if plan.ArchivesAll() {
			appName := strings.TrimSpace(plan.App.Name)
			if appName == "" {
				return nil, ErrUnnamedApp
			}
			if !confirmArchiveAll(in, opts.Prompt, appName) {
				return nil, ErrCancelled
			}
			allowArchiveAll = true
		} else if !confirm(in, opts.Prompt) {
			return nil, ErrCancelled
		}

		applied, err := s.Apply(ctx, ApplyOptions{
			BaseRevision:    api.NewOptInt(plan.Revision),
			AllowArchiveAll: allowArchiveAll,
		})
		if errors.Is(err, ErrConflict) {
			log.Warn("The app's tests changed after this plan was made, so nothing was applied. Planning again.", "reason", err)
			fmt.Fprintln(out)
			continue
		}
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(out, applied.Totals())
		return applied, nil
	}
}

func confirm(in *bufio.Reader, prompt io.Writer) bool {
	fmt.Fprint(prompt, "Apply these changes? [y/N] ")
	answer := strings.ToLower(readLine(in))
	return answer == "y" || answer == "yes"
}

// confirmArchiveAll asks for the app's name, exactly, so archiving a whole
// suite can't be confirmed by habit or against the wrong app.
func confirmArchiveAll(in *bufio.Reader, prompt io.Writer, appName string) bool {
	fmt.Fprintf(prompt, "This archives every test in code for %s. Type the app's name to confirm: ", appName)
	return readLine(in) == appName
}

// readLine reads one answer. An error, such as the end of the input, reads as
// an empty answer, which never confirms anything.
func readLine(in *bufio.Reader) string {
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	return strings.TrimSpace(line)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
