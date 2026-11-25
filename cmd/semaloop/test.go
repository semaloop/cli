package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/semaloop/cli/internal/client"
	"github.com/semaloop/cli/internal/config"
	"github.com/semaloop/cli/internal/testsync"
)

type TestCmd struct {
	Sync TestSyncCmd `cmd:"" help:"Sync tests from Markdown files in your repository."`
}

// TestSyncCmd makes the app's code-managed tests match a set of Markdown files.
// Tests created in the web dashboard are never touched.
type TestSyncCmd struct {
	Paths        []string `arg:"" help:"Markdown files or directories (searched recursively) to sync." type:"existingpath"`
	AllowEmpty   bool     `help:"Allow syncing zero tests, which archives every code-managed test." name:"allow-empty"`
	BaseRevision *int     `help:"Apply only if the tests are still at this revision, as printed by --dry-run." name:"base-revision"`
}

func (c *TestSyncCmd) Run(g *Globals) error {
	tests, err := testsync.Load(c.Paths)
	if err != nil {
		return fmt.Errorf("invalid test files:\n%w", err)
	}
	if len(tests) == 0 && !c.AllowEmpty {
		return errors.New("no .md files found; pass --allow-empty to archive every code-managed test")
	}
	log.Info("Loaded tests.", "count", len(tests))

	apiKey, err := config.APIKey()
	if err != nil {
		return fmt.Errorf("could not load configuration: %w", err)
	}
	if apiKey == "" {
		return fmt.Errorf("not authenticated, run `semaloop auth login` first")
	}

	result, err := testsync.Sync(context.Background(), apiKey, client.ServerURL(), tests,
		testsync.SyncOptions{DryRun: g.DryRun, BaseRevision: c.BaseRevision})
	if err != nil {
		if errors.Is(err, client.ErrUnauthorized) {
			return fmt.Errorf("invalid API key, run `semaloop auth login` to update it")
		}
		return err
	}

	out := io.Writer(os.Stdout)
	if g.Quiet {
		out = io.Discard
	}
	counts := printPlan(out, tests, result.Changes)

	verb := "Synced tests."
	if g.DryRun {
		verb = "Dry run: no changes made."
	}
	log.Info(verb, "event", "test_sync_succeeded", "revision", result.Revision, "created", counts["create"],
		"updated", counts["update"], "archived", counts["archive"], "unchanged", counts["unchanged"])
	return nil
}

func printPlan(w io.Writer, tests []testsync.Test, changes []testsync.Change) map[string]int {
	paths := map[string]string{}
	for _, t := range tests {
		paths[t.ID] = t.Path
	}
	counts := map[string]int{}
	for _, ch := range changes {
		counts[ch.Action]++
		switch ch.Action {
		case "create":
			fmt.Fprintf(w, "  + %s (%s)\n", ch.ID, paths[ch.ID])
		case "update":
			fmt.Fprintf(w, "  ~ %s: %s (%s)\n", ch.ID, strings.Join(ch.Fields, ", "), paths[ch.ID])
		case "archive":
			fmt.Fprintf(w, "  - %s\n", ch.ID)
		}
	}
	return counts
}
