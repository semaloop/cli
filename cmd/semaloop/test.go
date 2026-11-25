package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/semaloop/cli/internal/client"
	icmd "github.com/semaloop/cli/internal/cmd"
	"github.com/semaloop/cli/internal/config"
)

type TestCmd struct {
	Check TestCheckCmd `cmd:"" help:"Check test files offline and report every error."`
	Sync  TestSyncCmd  `cmd:"" help:"Make Semaloop's tests in code match the test files."`
}

// TestCheckCmd checks test files without an API key or any network calls.
type TestCheckCmd struct {
	Path string `arg:"" default:".semaloop/tests" help:"Directory of test files, relative to the current directory (default: ${default})." completion-predictor:"dir"`
}

func (c *TestCheckCmd) Run() error {
	tests, err := icmd.CheckTests(c.Path, os.Stdout)
	if err != nil {
		return err
	}
	log.Info("Test files are valid.", "dir", c.Path, "tests", len(tests))
	return nil
}

// TestSyncCmd creates, updates and archives the app's tests in code so they
// match the test files.
type TestSyncCmd struct {
	Path            string `arg:"" default:".semaloop/tests" help:"Directory of test files, relative to the current directory (default: ${default})." completion-predictor:"dir"`
	AllowArchiveAll bool   `help:"Allow a sync that would archive every test in code, e.g. when the directory is empty." name:"allow-archive-all"`
}

func (c *TestSyncCmd) Run(g *Globals) error {
	apiKey, err := config.APIKey()
	if err != nil {
		return fmt.Errorf("could not load configuration: %w", err)
	}
	// --dry-run makes no network calls, so it doesn't need an API key.
	if apiKey == "" && !g.DryRun {
		return fmt.Errorf("not authenticated, run `semaloop auth login` first")
	}

	_, err = icmd.SyncTests(context.Background(), apiKey, client.ServerURL(), c.Path, os.Stdout, icmd.SyncOptions{
		DryRun:          g.DryRun,
		AllowArchiveAll: c.AllowArchiveAll,
	})
	if err != nil {
		switch {
		case errors.Is(err, icmd.ErrInvalidTests):
			return err
		case errors.Is(err, client.ErrUnauthorized):
			return fmt.Errorf("invalid API key, run `semaloop auth login` to update it")
		case errors.Is(err, client.ErrForbidden):
			return fmt.Errorf("your account does not have permission to sync tests")
		default:
			return fmt.Errorf("test sync failed: %w", err)
		}
	}

	if g.DryRun {
		log.Info("Dry run complete. No changes made.")
	} else {
		log.Info("Tests synced successfully.", "event", "test_sync_succeeded")
	}
	return nil
}
