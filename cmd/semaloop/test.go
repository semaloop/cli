package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/semaloop/cli/internal/client"
	icmd "github.com/semaloop/cli/internal/cmd"
	"github.com/semaloop/cli/internal/config"
	"golang.org/x/term"
)

type TestCmd struct {
	Check TestCheckCmd `cmd:"" help:"Check test files offline and report every error."`
	Diff  TestDiffCmd  `cmd:"" help:"Show what a sync would change, without changing anything."`
	Sync  TestSyncCmd  `cmd:"" help:"Make Semaloop's tests in code match the test files."`
}

// TestOutput is the --output flag of the test commands. Results go to
// stdout in this format, and logs go to stderr.
type TestOutput struct {
	Output string `short:"o" enum:"text,json" default:"text" help:"Format of the results: text or json."`
}

func (o TestOutput) json() bool { return o.Output == "json" }

// TestCheckCmd checks test files without an API key or any network calls.
type TestCheckCmd struct {
	Path string `arg:"" default:".semaloop/tests" help:"Directory of test files, relative to the current directory (default: ${default})." completion-predictor:"dir"`
	TestOutput
}

func (c *TestCheckCmd) Run() error {
	tests, err := icmd.CheckTests(c.Path, os.Stdout, c.json())
	if err != nil {
		return err
	}
	log.Info("Test files are valid.", "dir", c.Path, "tests", len(tests))
	return nil
}

// TestDiffCmd shows what a sync would change.
type TestDiffCmd struct {
	Path string `arg:"" default:".semaloop/tests" help:"Directory of test files, relative to the current directory (default: ${default})." completion-predictor:"dir"`
	TestOutput
}

func (c *TestDiffCmd) Run(g *Globals) error {
	if g.DryRun {
		// --dry-run makes no network calls, so all diff can do is check.
		tests, err := icmd.CheckTests(c.Path, os.Stdout, c.json())
		if err != nil {
			return err
		}
		log.Info("Dry run: test files checked, no plan made.", "dir", c.Path, "tests", len(tests))
		return nil
	}
	return diff(c.Path, c.TestOutput)
}

func diff(path string, out TestOutput) error {
	s, err := newSyncClient(path, out)
	if err != nil {
		return err
	}
	_, err = icmd.Diff(context.Background(), s, os.Stdout, icmd.DiffOptions{
		JSON: out.json(),
		// Only a person at a terminal can act on the hint. In CI, how a plan
		// gets applied is up to the workflow.
		Hint: term.IsTerminal(int(os.Stdout.Fd())),
	})
	return syncError("test diff", err)
}

// TestSyncCmd creates, updates and archives the app's tests in code so they
// match the test files.
type TestSyncCmd struct {
	Path            string `arg:"" default:".semaloop/tests" help:"Directory of test files, relative to the current directory (default: ${default})." completion-predictor:"dir"`
	Yes             bool   `short:"y" help:"Apply without asking. Sync only asks in a terminal, and never in CI."`
	AllowArchiveAll bool   `help:"Allow a sync that archives every test in code where sync doesn't ask, e.g. in CI. In a terminal, sync asks you to confirm it instead." name:"allow-archive-all"`
	TestOutput
}

func (c *TestSyncCmd) Help() string {
	return "In a terminal, sync shows the plan and asks before applying it. Pass --dry-run to only show the plan, like `semaloop test diff`."
}

func (c *TestSyncCmd) Run(g *Globals) error {
	// For sync, --dry-run is `test diff`: it shows the plan, which needs a
	// network call but changes nothing.
	if g.DryRun {
		return diff(c.Path, c.TestOutput)
	}

	confirm := !c.Yes && canAsk()
	if confirm && c.json() {
		return fmt.Errorf("sync asks before applying in a terminal, which --output json can't show: pass --yes as well")
	}

	s, err := newSyncClient(c.Path, c.TestOutput)
	if err != nil {
		return err
	}
	_, err = icmd.Sync(context.Background(), s, os.Stdout, icmd.SyncOptions{
		Confirm:         confirm,
		In:              os.Stdin,
		Prompt:          os.Stderr,
		AllowArchiveAll: c.AllowArchiveAll,
		JSON:            c.json(),
	})
	if err != nil {
		return syncError("test sync", err)
	}
	log.Info("Tests synced successfully.", "event", "test_sync_succeeded")
	return nil
}

// newSyncClient checks the test files in path and returns a client to sync
// them.
func newSyncClient(path string, out TestOutput) (*icmd.SyncClient, error) {
	apiKey, err := config.APIKey()
	if err != nil {
		return nil, fmt.Errorf("could not load configuration: %w", err)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("not authenticated, run `semaloop auth login` first")
	}
	tests, err := icmd.LoadTests(path, os.Stdout, out.json())
	if err != nil {
		return nil, err
	}
	log.Debug("Test files are valid.", "dir", path, "tests", len(tests))
	return icmd.NewSyncClient(apiKey, client.ServerURL(), path, tests)
}

func syncError(command string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, icmd.ErrInvalidTests), errors.Is(err, icmd.ErrCancelled):
		return err
	case errors.Is(err, client.ErrUnauthorized):
		return fmt.Errorf("invalid API key, run `semaloop auth login` to update it")
	case errors.Is(err, client.ErrForbidden):
		return fmt.Errorf("your account does not have permission to sync tests")
	default:
		return fmt.Errorf("%s failed: %w", command, err)
	}
}

// canAsk reports whether sync can ask before applying: a person can see the
// question and answer it, and it's not run in CI. Stdout doesn't count, so
// piping the results somewhere doesn't skip the question.
func canAsk() bool {
	if ci := os.Getenv("CI"); ci != "" && ci != "false" && ci != "0" {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}
