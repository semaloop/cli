package main

import (
	"context"
	"errors"

	"github.com/semaloop/cli/internal/client"
	icmd "github.com/semaloop/cli/internal/cmd"
	"github.com/semaloop/cli/internal/config"
)

type BuildCmd struct {
	Push BuildPushCmd `cmd:"" help:"Upload a build artifact."`
}

// BuildPushCmd uploads a build artifact to Semaloop.
//
// The git context flags are optional but all-or-nothing: the `and:"gitref"`
// group makes Kong reject a partial set (e.g. --git-repo without --git-commit)
// at parse time.
type BuildPushCmd struct {
	File      string `arg:"" help:"Path to the build artifact to upload (.app or .ipa)." type:"path"`
	GitRepo   string `help:"Source repository (owner/name) the build was produced from." name:"git-repo" and:"gitref"`
	GitCommit string `help:"Commit SHA the build was produced from." name:"git-commit" and:"gitref"`
	GitRef    string `help:"Git ref (e.g. refs/heads/main) the build was produced from." name:"git-ref" and:"gitref"`
}

func (c *BuildPushCmd) Run(g *Globals) error {
	apiKey, err := config.APIKey()
	if err != nil {
		log.Error("Could not load configuration.", "err", err)
		return nil
	}
	if apiKey == "" {
		log.Error("Not authenticated. Run `semaloop auth login` first.")
		return nil
	}

	result, err := icmd.Push(context.Background(), apiKey, client.ServerURL(), c.File, icmd.PushOptions{
		DryRun: g.DryRun,
		Repo:   c.GitRepo,
		Commit: c.GitCommit,
		Ref:    c.GitRef,
	})
	if err != nil {
		switch {
		case errors.Is(err, client.ErrUnauthorized):
			log.Error("Invalid API key. Run `semaloop auth login` to update it.")
		case errors.Is(err, client.ErrForbidden):
			log.Error("Your account does not have permission to upload builds.")
		default:
			log.Error("Build push failed.", "err", err)
		}
		return nil
	}

	if g.DryRun {
		log.Info("Dry run complete. No upload performed.")
	} else {
		log.Info("Build uploaded successfully.", "id", result.UploadID)
	}
	return nil
}
